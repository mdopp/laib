# haus — the surface (ADR 0014)

`ghcr.io/mdopp/haus`, source `mdopp/haus`. The Box page and the app's box
keys (`/napi/servicebay/*`, the app's legacy path, ADR 0021) come from Laib.
While `SOLARIS_URL` is set, the migration bridge passes every other `/napi/*`
path to Solaris and accepts phones paired there (haus#5). Port 8650 is
published on the host's loopback only; Caddy is the only way in.

1. `laib add haus` mints `HAUS_LAIB_TOKEN` (hand `haus`: status, inbox,
   journal, restart) and generates `HAUS_PROXY_SECRET`, which Caddy sends
   as `X-Haus-Proxy` to the pages — nothing else reaches them (haus#1).
2. Haus decides nothing: the Box page links to `laib.<domain>/inbox`, the
   one approval surface; the phone's approve buttons answer 403.
3. `laib add haus`. To pair a phone, enter `https://haus.<domain>` in the
   app: it opens `/pair-device`, the person logs in and confirms, and the
   app is paired (haus#6). `/box/geraete` lists and removes devices.
