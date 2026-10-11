# haus — the surface (ADR 0014)

`ghcr.io/mdopp/haus`, source `mdopp/haus`. First cut (laib#28): the Box page
and `/napi/servicebay/*` from Laib; every other `/napi/*` path passed to
Solaris, which also validates the phone's device token. Port 8650 is
published on the host's loopback only; Caddy is the only way in.

1. `laib add haus` mints `HAUS_LAIB_TOKEN` (ops) itself.
2. optional: `laib token mint human --note haus-admin` → `laib secret set HAUS_HUMAN_TOKEN`
   (Freigeben/Ablehnen on the phone and the Box page; without it the page
   points at Laib's own inbox)
3. `laib add haus`; then `https://haus.<domain>/box/geraete` pairs a phone
   (the setup link opens the app). Phones paired with Solaris keep working
   while `SOLARIS_URL` is set.
