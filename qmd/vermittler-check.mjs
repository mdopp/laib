#!/usr/bin/env node
// The image check (#198), run inside a laib-qmd container next to its
// Vermittler, after `qmd update && qmd embed`: node vermittler-check.mjs
// <query>… , with qmd's own server beside it at $QMD_REF_URL. Each query
// must answer exactly what qmd's /query answers — every field, `file`
// percent-encoded as qmd's encodeQmdPath does — plus `vec`, the cosine
// qmd's own vector search reports for that note (0 for a hit outside its
// top list). Then the refusals and the Host check. Run it on a Vermittler
// just started with an embedded index: the first search after /health
// turns 200 must not wait for the embed model (Haus's budget is 2.5 s).
import http from "node:http";
import { createStore } from "@tobilu/qmd";

const sdk = import.meta.resolve("@tobilu/qmd");
const { enableProductionMode, getDefaultDbPath } = await import(new URL("./store.js", sdk));
const { getConfigDir } = await import(new URL("./collections.js", sdk));
const base = `http://127.0.0.1:${process.env.QMD_PORT || 8181}`;
const failed = [];
const check = (cond, msg) => { if (!cond) failed.push(msg); };
const post = (body, url = base) =>
  fetch(`${url}/query`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(body) });
const qmd = process.env.QMD_REF_URL;
if (!qmd) throw new Error("QMD_REF_URL (qmd mcp --http beside the Vermittler) is required");

for (let i = 0; ; i++) {
  const up = await fetch(`${base}/health`).then((r) => r.status === 200, () => false);
  if (up) break;
  if (i > 240) throw new Error("/health did not turn 200 within 2 minutes");
  await new Promise((r) => setTimeout(r, 500));
}
{
  const q = process.argv[2], t = performance.now();
  const r = await post({ searches: [{ type: "lex", query: q }, { type: "vec", query: q }], limit: 10 });
  await r.json();
  const ms = Math.round(performance.now() - t);
  check(r.status === 200 && ms < 500, `the first search after /health took ${ms} ms (${r.status}), not under 500: the embed model was not loaded`);
  console.log(`first search after /health: ${ms} ms`);
}
const status = await (await fetch(`${base}/status`)).json();
check(status.collections?.length > 0 && status.collections.every((c) => c.documents > 0 && c.embedded === c.documents),
  `/status does not show every note embedded: ${JSON.stringify(status)}`);

enableProductionMode();
const store = await createStore({ dbPath: getDefaultDbPath(), configPath: `${getConfigDir()}/index.yml` });
const collection = (await store.getDefaultCollectionNames())[0];
const shape = "docid,file,title,score,context,line,snippet,vec";
let compared = 0;
const files = [];
for (const q of process.argv.slice(2)) {
  const body = { searches: [{ type: "lex", query: q }, { type: "vec", query: q }], limit: 50, rerank: false };
  const res = await post(body);
  const { results } = await res.json();
  check(res.status === 200 && results?.length > 0, `${q}: no results (${res.status})`);
  const own = (await (await post(body, qmd)).json()).results;
  const strip = (rs) => JSON.stringify((rs ?? []).map(({ vec, ...r }) => r));
  check(strip(results) === strip(own), `${q}: not what qmd's /query answers:\n  vermittler ${strip(results)}\n  qmd        ${strip(own)}`);
  // the reference: qmd's vector search over the same collection, the same top list of 20
  const ref = new Map((await store.searchVector(q, { limit: 20, collection }))
    .map((r) => [`qmd://${r.displayPath.split("/").map(encodeURIComponent).join("/")}`, r.score]));
  for (const r of results ?? []) {
    check(Object.keys(r).join(",") === shape, `${q}: ${r.file} has ${Object.keys(r)}, not ${shape}`);
    const want = ref.has(r.file) ? Math.round(ref.get(r.file) * 1000) / 1000 : 0;
    check(typeof r.vec === "number" && Math.abs(r.vec - want) < 0.0015, `${q}: ${r.file} vec ${r.vec}, qmd's vector search says ${want}`);
    compared++;
    files.push(r.file);
  }
  console.log(`${q}: ${(results ?? []).slice(0, 3).map((r) => `${r.file} vec ${r.vec}`).join(", ")}`);
}
await store.close();
check(compared > 0, "no hit compared");
// the notes must have exercised qmd's encoding: an umlaut and an & in a file name
check(files.some((f) => f.includes("%C3")) && files.some((f) => f.includes("%26")), `no hit with an umlaut and an & in its name: ${[...new Set(files)]}`);

const lex = [{ type: "lex", query: "haus" }];
for (const [body, why] of [
  [{ searches: lex, rerank: true }, "rerank:true"],
  [{ query: "haus" }, "a plain query"],
  [{ searches: [{ type: "hyde", query: "haus" }] }, "an expanded (hyde) search"],
  [{ searches: lex, intent: "haus" }, "an intent"],
]) check((await post(body)).status === 400, `${why} is not refused with 400`);
check((await post({ searches: lex, rerank: false, limit: 1 })).status === 200, "the health probe's lex search is not 200");
// fetch sets Host itself; a DNS-rebound page's request carries its own name
const foreign = await new Promise((resolve, reject) => http.request(`${base}/query`, { method: "POST", headers: { host: "haus.example:8181" } },
  (r) => { r.resume(); resolve(r.statusCode); }).on("error", reject).end(JSON.stringify({ searches: lex })));
check(foreign === 403, `a foreign Host is answered with ${foreign}, not 403`);

if (failed.length) {
  console.error(failed.join("\n"));
  process.exit(1);
}
console.log(`ok: ${compared} hits, vec equals qmd's own cosine; refusals and the Host check hold`);
