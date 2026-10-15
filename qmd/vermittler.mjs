#!/usr/bin/env node
// The Vermittler (#198, haus#13): qmd's POST /query on qmd's own SDK, plus
// each hit's cosine. qmd 2.8.3 answers only a rank as `score`, so a
// nonsense query's top hit scores 1 (upstream: tobi/qmd#697); the SDK's
// explain carries the vector backend's 1 - distance per hit. Typed lex/vec
// searches only, rerank always off: the embed model is the only one that
// loads. It goes when qmd returns the cosine itself.
import http from "node:http";
import { createStore, extractSnippet, addLineNumbers } from "@tobilu/qmd";

const sdk = import.meta.resolve("@tobilu/qmd"); // dist/index.js; the rest is not in the exports map
const { enableProductionMode, getDefaultDbPath, getEmbeddingFingerprint } = await import(new URL("./store.js", sdk));
const { getConfigDir } = await import(new URL("./collections.js", sdk));
const { resolveOriginGuard, checkRequestOrigin } = await import(new URL("./mcp/origin-guard.js", sdk));

const HOST = "127.0.0.1"; // no auth: the pod's loopback only
const PORT = Number(process.env.QMD_PORT || 8181);
enableProductionMode(); // the CLI's paths, so `qmd update` and `qmd embed` fill this index: $XDG_CACHE_HOME/qmd, $QMD_CONFIG_DIR
const store = await createStore({ dbPath: getDefaultDbPath(), configPath: `${getConfigDir()}/index.yml` });
const db = store.internal.db;
const guard = resolveOriginGuard({ host: HOST }); // qmd's own Host/Origin check for a loopback bind

// The embed model loads at the first vec search (~3 s on the CPU) and qmd
// drops it after 5 idle minutes; Haus waits 2.5 s. So it stays loaded, and
// is loaded before /health answers. Until the first embed (which downloads
// it) there are no vectors and no search loads it.
store.internal.llm.inactivityTimeoutMs = 0;
let warm = null;
function warmed() {
  if (!warm && db.prepare("SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'vectors_vec'").get()) {
    const t = Date.now();
    warm = store.search({ queries: [{ type: "vec", query: "Haus" }], limit: 1, rerank: false })
      .then(() => console.error(`vermittler: embed model loaded in ${Date.now() - t} ms`), (e) => console.error(`vermittler: warm-up: ${e.message}`));
  }
  // a model that cannot load (not cached, offline) must not keep the library down
  return warm && Promise.race([warm, new Promise((r) => setTimeout(r, 60000).unref())]);
}
await warmed();

class Refused extends Error {}
const refuse = (msg) => { throw new Refused(msg); };

async function query(p) {
  if (!p || typeof p !== "object" || Array.isArray(p)) refuse("the body is a JSON object");
  for (const k of Object.keys(p)) {
    if (!["searches", "limit", "collections", "rerank"].includes(k)) refuse(`${k} is not taken: typed lex/vec searches only, no plain or expanded query`);
  }
  if (p.rerank !== undefined && p.rerank !== false) refuse("rerank is always off");
  if (!Array.isArray(p.searches) || p.searches.length === 0) refuse("Missing required field: searches (array)");
  const queries = p.searches.map((s) => {
    if (!s || !["lex", "vec"].includes(s.type) || typeof s.query !== "string" || !s.query.trim()) refuse('each search is {type: "lex"|"vec", query}');
    return { type: s.type, query: s.query };
  });
  if (p.limit !== undefined && typeof p.limit !== "number") refuse("limit is a number");
  if (p.collections !== undefined && !Array.isArray(p.collections)) refuse("collections is an array");
  const collections = p.collections ? p.collections.map(String) : await store.getDefaultCollectionNames();
  const results = await store.search({
    queries, limit: p.limit ?? 10, minScore: 0, rerank: false,
    collections: collections.length > 0 ? collections : undefined,
    explain: true, // carries the vector backend's score of every vec-list hit
  });
  const primary = queries.find((q) => q.type === "lex")?.query || queries[0].query;
  return results.map((r) => {
    const { line, snippet } = extractSnippet(r.body, primary, 300, r.bestChunkPos, r.bestChunk.length);
    const v = r.explain?.vectorScores ?? [];
    return { // qmd's /query shape (mcp/server.js) …
      docid: `#${r.docid}`,
      file: `qmd://${r.displayPath.split("/").map(encodeURIComponent).join("/")}`, // qmd's encodeQmdPath, which the SDK does not export
      title: r.title,
      score: Math.round(r.score * 100) / 100,
      context: r.context,
      line,
      snippet: addLineNumbers(snippet, line),
      vec: v.length ? Math.round(Math.max(...v) * 1000) / 1000 : 0, // … plus the cosine; 0: not in a vec top list
    };
  });
}

// per collection, its documents and how many of them are fully embedded with the configured model
function status() {
  const model = store.internal.llm.embedModelName;
  const rows = db.prepare(`
    SELECT c.name AS name, COUNT(d.id) AS documents,
      COALESCE(SUM(v.chunks >= v.total), 0) AS embedded
    FROM store_collections c
    LEFT JOIN documents d ON d.collection = c.name AND d.active = 1
    LEFT JOIN (SELECT hash, COUNT(*) AS chunks, MAX(total_chunks) AS total FROM content_vectors
               WHERE model = ? AND embed_fingerprint = ? GROUP BY hash) v ON v.hash = d.hash
    GROUP BY c.name ORDER BY c.name`).all(model, getEmbeddingFingerprint(model));
  return { collections: rows };
}

function send(res, code, body) {
  res.writeHead(code, { "Content-Type": "application/json" });
  res.end(JSON.stringify(body));
}

http.createServer(async (req, res) => {
  try {
    const verdict = checkRequestOrigin({ origin: req.headers.origin, host: req.headers.host }, guard);
    if (!verdict.ok) return send(res, 403, { error: `Forbidden: ${verdict.reason}` });
    const path = (req.url || "/").split("?")[0];
    if (req.method === "GET" && path === "/health") {
      await warmed(); // also right after the first embed
      db.prepare("SELECT 1").get(); // the store is open
      return send(res, 200, { status: "ok" });
    }
    if (req.method === "GET" && path === "/status") return send(res, 200, status());
    if (req.method !== "POST" || path !== "/query") return send(res, 404, { error: "Not Found" });
    let body = "";
    for await (const c of req) body += c;
    let p;
    try { p = JSON.parse(body); } catch { refuse("the body is not JSON"); }
    send(res, 200, { results: await query(p) });
  } catch (e) {
    // qmd's own query checks (one line, lex syntax) are the caller's mistake too
    if (e instanceof Refused || /^(Structured search|Line \d+) \(/.test(e.message)) return send(res, 400, { error: e.message });
    console.error(`vermittler: ${req.method} ${req.url}: ${e.stack || e}`);
    send(res, 500, { error: String(e.message || e) });
  }
}).listen(PORT, HOST, () => console.error(`vermittler: qmd's /query with vec on http://${HOST}:${PORT}`));

for (const s of ["SIGINT", "SIGTERM"]) process.on(s, async () => { await store.close(); process.exit(0); });
