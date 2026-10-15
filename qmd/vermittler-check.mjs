#!/usr/bin/env node
// The image check (#198), run inside a laib-qmd container next to its
// Vermittler, after `qmd update && qmd embed`: node vermittler-check.mjs
// <query>… . Each query must answer qmd's /query shape plus `vec`, and
// `vec` must be the cosine qmd's own vector search reports for that note
// (0 for a hit outside its top list). Then the refusals and the Host check.
import http from "node:http";
import { createStore } from "@tobilu/qmd";

const sdk = import.meta.resolve("@tobilu/qmd");
const { enableProductionMode, getDefaultDbPath } = await import(new URL("./store.js", sdk));
const { getConfigDir } = await import(new URL("./collections.js", sdk));
const base = `http://127.0.0.1:${process.env.QMD_PORT || 8181}`;
const failed = [];
const check = (cond, msg) => { if (!cond) failed.push(msg); };
const post = (body) =>
  fetch(`${base}/query`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(body) });

check((await fetch(`${base}/health`)).status === 200, "/health is not 200");
const status = await (await fetch(`${base}/status`)).json();
check(status.collections?.length > 0 && status.collections.every((c) => c.documents > 0 && c.embedded === c.documents),
  `/status does not show every note embedded: ${JSON.stringify(status)}`);

enableProductionMode();
const store = await createStore({ dbPath: getDefaultDbPath(), configPath: `${getConfigDir()}/index.yml` });
const collection = (await store.getDefaultCollectionNames())[0];
const shape = "docid,file,title,score,context,line,snippet,vec";
let compared = 0;
for (const q of process.argv.slice(2)) {
  const res = await post({ searches: [{ type: "lex", query: q }, { type: "vec", query: q }], limit: 50 });
  const { results } = await res.json();
  check(res.status === 200 && results?.length > 0, `${q}: no results (${res.status})`);
  // the reference: qmd's vector search over the same collection, the same top list of 20
  const ref = new Map((await store.searchVector(q, { limit: 20, collection }))
    .map((r) => [`qmd://${r.displayPath.split("/").map(encodeURIComponent).join("/")}`, r.score]));
  for (const r of results ?? []) {
    check(Object.keys(r).join(",") === shape, `${q}: ${r.file} has ${Object.keys(r)}, not ${shape}`);
    const want = ref.has(r.file) ? Math.round(ref.get(r.file) * 1000) / 1000 : 0;
    check(typeof r.vec === "number" && Math.abs(r.vec - want) < 0.0015, `${q}: ${r.file} vec ${r.vec}, qmd's vector search says ${want}`);
    compared++;
  }
  console.log(`${q}: ${(results ?? []).slice(0, 3).map((r) => `${r.file} vec ${r.vec}`).join(", ")}`);
}
await store.close();
check(compared > 0, "no hit compared");

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
