# immich-public-proxy

[Immich Public Proxy](https://github.com/alangrainger/immich-public-proxy)
(IPP, AGPL-3.0, image `ghcr.io/alangrainger/immich-public-proxy`, pinned
to a release; needs Immich 3.0 or newer) is the one public way to the
household's photos (owner, 2026-11-07). Immich itself is `exposure:
internal`: the family reaches it at home or through the Fritz!Box's
WireGuard. A guest opens a shared link on `fotos.<domain>`; IPP passes
through only what Immich shares, and holds no API key, no login and no
state.

- **The route** is public with `auth: none`: the share key is the
  capability. Caddy answers every path but IPP's (`/share`, `/s`, the
  upload probe) with 403, so nothing of Immich's API is on this name even
  if IPP misroutes; `loginProbe: /api/server/ping` (which Immich answers
  to anyone) measures exactly that with `laib-app check fotos.<domain>`.
- **No rate limit yet.** Share keys are long random strings, but a share
  with a password or a short custom slug (`/s/<slug>`) can be guessed at.
  The Caddy rate-limit module comes with Laib's kontakt train (#1101);
  once it is in the Caddy image, a `rate_limit` zone belongs on this
  route as on `kontakt`.
- **Metadata** stays IPP's default: no EXIF, no place, no GPS for
  visitors (every field is opt-in upstream); downloads follow each share's
  own setting in Immich. Visitor uploads (IPP's second image) are not run.
- **The links.** Immich writes its share links with its *External domain*
  (Administration → Einstellungen → Server). immich's `SHARE_URL` value
  (`https://fotos.<domain>`) makes immich's post-deploy set it, and only
  once `<SHARE_URL>/share/healthcheck` answers `ok`, so a link never points
  at a proxy that is not there. Set it after this service is green.
