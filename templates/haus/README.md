# haus — the surface (ADR 0014)

`ghcr.io/mdopp/haus`, source `mdopp/haus`. The Box page and the app's box
keys (`/napi/servicebay/*`, the app's legacy path, ADR 0021) come from Laib.
While `SOLARIS_URL` is set, the migration bridge passes every other `/napi/*`
path to Solaris and accepts phones paired there (until haus#10). Port 8650 is
published beyond loopback for the Bote's pod (below); the LAN reaches it only through Caddy.

With `laib secret set HAUS_HA_TOKEN` (a Home Assistant long-lived token),
Haus serves the house paths itself from Home Assistant: `ha/call` with the
companion contract's safety rules, `portal/{state,energy,entity-history,active}`
(haus#10). Events and watch stay on the bridge while it is configured, so
notices keep arriving. `ANDROID_FINGERPRINTS` fills
`/.well-known/assetlinks.json` (public, through Caddy's bypass), and
`ENERGY_FLOW`/`ENERGY_TOTALS` name the energy sensors by entity id.

The Haushalt (haus#11): Haus serves `/mcp/haushalt` to the Bote, with the
generated `HAUS_HAUSHALT_TOKEN` as its bearer (ADR 0017). The Bote reaches it
on the host's port 8650: Haus publishes 8650 beyond loopback, `lan: false`
keeps the LAN out, and pages, `/napi` and `/mcp` each check their own secret
(#170). Notes are Markdown files in the file share's `notes` folder.
Calendar and reminders need a Radicale account: set `RADICALE_USER` and
`laib secret set HAUS_RADICALE_PASSWORD`; without it those tools are absent.

1. `laib add haus` mints `HAUS_LAIB_TOKEN` (hand `haus`: status, inbox,
   journal, restart) and generates `HAUS_PROXY_SECRET`, which Caddy sends
   as `X-Haus-Proxy` to the pages — nothing else reaches them (haus#1).
2. Haus decides nothing: the Box page links to `laib.<domain>/inbox`, the
   one approval surface; the phone's approve buttons answer 403.
3. `laib add haus`. To pair a phone, enter `https://haus.<domain>` in the
   app: it opens `/pair-device`, the person logs in and confirms, and the
   app is paired (haus#6). `/box/geraete` lists and removes devices.
