# haus — the surface (ADR 0014)

`ghcr.io/mdopp/haus`, source `mdopp/haus`. First cut (laib#28): the Box page
and `/napi/servicebay/*` from Laib; every other `/napi/*` path passed to
Solaris, which also validates the phone's device token. Port 8650 is
published on the host's loopback only; Caddy is the only way in.

1. `laib add haus` mints `HAUS_LAIB_TOKEN` (hand `haus`: status, inbox,
   journal, restart) and generates `HAUS_PROXY_SECRET`, which Caddy sends
   as `X-Haus-Proxy` to the pages — nothing else reaches them (haus#1).
2. Haus decides nothing: the Box page links to `laib.<domain>/inbox`, the
   one approval surface; the phone's approve buttons answer 403.
3. `laib add haus`; then `https://haus.<domain>/box/geraete` pairs a phone
   (the setup link opens the app). Phones paired with Solaris keep working
   while `SOLARIS_URL` is set.
