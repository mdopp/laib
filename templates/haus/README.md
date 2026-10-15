# haus — the surface (ADR 0014)

`ghcr.io/mdopp/haus`, source `mdopp/haus`. The family's pages and `/napi`,
the API of the Haus app (`mdopp/haus-android`, a widget client, ADR 0022):
the Box keys under `/napi/box/*` come from Laib. Nothing of the Solaris app
is carried. Port 8650 is published beyond loopback for the Bote's pod
(below); the LAN reaches it only through Caddy.

With `laib secret set HAUS_HA_TOKEN` (a Home Assistant long-lived token),
Haus serves the house paths itself from Home Assistant: `ha/call` with the
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
(`qmd/`), CPU only. It listens on the pod's loopback only — its HTTP has
no auth — and Haus reaches it at `HAUS_QMD_URL=http://127.0.0.1:8181`,
always with typed searches (`lex` + `vec`) and `rerank: false`; a plain
query pulls in a 1.7B model and the reranker. `qmd.json` is its
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

1. `laib add haus` mints `HAUS_LAIB_TOKEN` (hand `haus`: status, inbox,
   journal, restart) and generates `HAUS_PROXY_SECRET`, which Caddy sends
   as `X-Haus-Proxy` to the pages — nothing else reaches them (haus#1).
2. Haus decides nothing: the Box page links to `laib.<domain>/inbox`, the
   one approval surface; the phone's approve buttons answer 403.
3. `laib add haus`. To pair a phone, enter `https://haus.<domain>` in the
   app: it opens `/pair-device`, the person logs in and confirms, and the
   app is paired (haus#6). `/box/geraete` lists and removes devices.
