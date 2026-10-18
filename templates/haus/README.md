# haus — the surface (ADR 0014)

`ghcr.io/mdopp/haus`, source `mdopp/haus`. The family's pages and `/napi`,
the API of the Haus app (`mdopp/haus-android`, a widget client, ADR 0022):
the Box keys under `/napi/box/*` come from Laib. Nothing of the Solaris app
is carried. Port 8650 is published beyond loopback for the Bote's pod
(below); the LAN reaches it only through Caddy.

With `HAUS_HA_TOKEN`, an administrator's long-lived token that
home-assistant's post-deploy mints and hands back (#349; nothing to click
in HA), Haus serves the house paths itself from Home Assistant: `ha/call` with the
companion contract's safety rules, `portal/{state,energy,entity-history,active}`
and the live stream that keeps the app's widgets current while the screen
is on (ADR 0022). `ENERGY_FLOW`/`ENERGY_TOTALS` name the energy sensors by
entity id.

The Haushalt (haus#11): Haus serves `/mcp/haushalt` to the Bote, with the
generated `HAUS_HAUSHALT_TOKEN` as its bearer (ADR 0017). The Bote reaches it
on the host's port 8650: Haus publishes 8650 beyond loopback, `lan: false`
keeps the LAN out, and pages, `/napi` and `/mcp` each check their own secret
(#170). Notes are Markdown files in the file share's `notes` folder.
Calendar and reminders need a Radicale account: set `RADICALE_USER` and
`laib secret set HAUS_RADICALE_PASSWORD`; without it those tools are absent.

The memory library (haus#13, #195): qmd (github.com/tobi/qmd, MIT) as
released, in the haus pod beside Haus (`haus-qmd`), searching the notes
by words and by meaning. Image `ghcr.io/mdopp/laib-qmd:<qmd version>`
(`qmd/`), CPU only. It serves the Vermittler (#198, `qmd/vermittler.mjs`)
instead of qmd's own server: qmd's 2.8.3 `/query` scores only by rank,
so a nonsense query's top hit scores 1, and Haus could not cut weak
hits. The Vermittler runs the same search on qmd's SDK:
- `POST /query` takes qmd's body (`searches` of type `lex`/`vec`,
  `limit`, `collections`), always without rerank; a plain or expanded
  query, `rerank: true` or any other field is refused with 400. It
  answers qmd's shape plus `vec`, the hit's cosine similarity (0 when
  the hit is outside the vector top list).
- `GET /status` lists each collection's documents and how many are
  embedded; `GET /health` answers while the index is open.
- Only the embed model loads, and it stays loaded: loading takes about
  3 s on the CPU, more than Haus waits. `/health`, and so the HealthCmd
  and `Notify=healthy`, answer only once it is loaded; before the first
  embed there is nothing to load.
- It goes when qmd returns the cosine itself (tobi/qmd#697).

It listens on the pod's loopback only — it has no auth — and Haus
reaches it at `HAUS_QMD_URL=http://127.0.0.1:8181` (like qmd it answers
only a loopback Host, 403 otherwise, so `host.containers.internal` would
not do), collection `HAUS_QMD_COLLECTION=notizen`. `qmd.json` is its
`index.yml`: the collection `notizen` over the notes folder, and
`models.embed` pinned to embeddinggemma-300M (it overrides
`QMD_EMBED_MODEL`). qmd watches no files: `haus-qmd-refresh.timer` runs
`qmd update` and `qmd embed` every quarter hour; a failed step fails
`haus-qmd-refresh.service` and `laib status` shows haus red until a run
succeeds. The first run downloads the model (~330 MB) into
`${DATA_DIR}/haus-qmd`; nothing ever runs `qmd pull`, which deletes the
cached models when offline. That folder is a cache, not backed up, and
lies beside `${DATA_DIR}/haus`, not in it: Haus relabels its own folder
(`:Z`) on every start. `haus-qmd` is active only once it answers a word search, and
is restarted when it stops answering. Serving takes about 700 MB; a
refresh with new notes about 1 GB more for a moment. embeddinggemma comes
under the Gemma Terms of Use, which fit a family's own use.

Documents (#219): the library indexes Paperless's documents too. Haus
mirrors what its Paperless user sees (the OCR text Paperless already
has) into `${DATA_DIR}/haus-dokumente`, one `<id>.md` per document
(title, date, sender, tags, then the text): `haus export-documents` in
Haus's container, as its own oneshot (`haus-documents-export.service`)
that the refresh wants and runs after. A document changed in Paperless
is written again, one deleted there is deleted here; Paperless not
answering leaves the folder as it was. The export failing (a folder Haus
cannot write, or a Haus image older than the command, which stops at
once without `LAIB_TOKEN`) shows haus red but never stops the notes'
refresh.
qmd indexes the folder read-only as the collection `dokumente`
(`HAUS_QMD_DOCUMENTS`); Haus checks each hit against Paperless and links
it to `haus.<domain>/dokumente/<id>`. Without `HAUS_PAPERLESS_TOKEN` the
export does nothing and the library is the notes. The folder is a cache
(Paperless is the documents' home), not backed up, and beside
`${DATA_DIR}/haus` and `haus-qmd`, mounted `:z` since both containers use it.

The house model (#198): with a model on the box (the `model` role, or
`box.yml model.url`) Haus gets `HAUS_LLM_URL` (the proxy's OpenAI base
as the pod reaches it, `${MODEL_URL}`), `HAUS_LLM_MODEL=qwen3.6-35b-a3b` and
the model key as `HAUS_LLM_KEY`, and lets the model tidy the library's
hits, never blocking. Without a model URL and key are empty and the
judge is off.

The Sprechen field (haus#12, #215): Haus gets the Bote's OpenAI-compatible
API as `HAUS_BOTE_URL` (`BOTE_URL`, default
`http://host.containers.internal:8642/v1`) and its key as `HAUS_BOTE_KEY`
(`BOTE_KEY: !secret BOTE_API_KEY`, generated by `laib add bote`). Without
the Bote the key renders empty and Haus shows no field. A haus service
added before #215 gets both lines from the template update, which adds
every new key of `values.example.yml` to `values.yml`; no hand edit.

1. `laib add haus` mints `HAUS_LAIB_TOKEN` (hand `haus`: status, inbox,
   journal, restart) and generates `HAUS_PROXY_SECRET`, which Caddy sends
   as `X-Haus-Proxy` to the pages — nothing else reaches them (haus#1).
2. Haus decides nothing: the Box page links to `laib.<domain>/inbox`, the
   one approval surface; the phone's approve buttons answer 403.
3. `laib add haus`. To pair a phone, enter `https://haus.<domain>` in the
   app: it opens `/pair-device`, the person logs in and confirms, and the
   app is paired (haus#6). `/box/geraete` lists and removes devices.

What the family uses (#266, haus#…): with usage-metrics installed, Haus
gets `HAUS_USAGE_URL` (`host.containers.internal:8094`) and
`HAUS_USAGE_TOKEN` (the `USAGE_METRICS_TOKEN` secret, a podman secret per
#224) and counts server-side, per feature and UTC day, never who: page
views, actions (`/schalten`, ☆, search, Sprechen), the plain "antwortet
gerade nicht" lines it showed, `/napi` refreshes per widget, and taps
tagged `?von=widget.<kind>` by the Haus app. It batches in memory and
flushes every few minutes; usage-metrics down drops the counts and logs
once. Haus also counts voice: Home Assistant's Assist runs, recognized or
not, and the sentence it did not understand (see `templates/home-assistant`).
The Box page's admin card "Was wird genutzt" reads it back: the last 30
days, most and least used, the plain lines, and the sentences the Bote and
voice could not answer. Without usage-metrics the token renders empty and
none of it happens.
