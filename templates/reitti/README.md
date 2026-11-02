# reitti — the household's timeline

[Reitti](https://github.com/dedicatedcode/reitti) is a self-hosted Google
Timeline: where each person was, their visits and trips, on a map. Server,
PostGIS, Redis and Reitti's tile cache run in one pod; only the server is
published, on loopback, and reached as `reitti.<domain>` from the home
network (`exposure: internal`).

## Logins

Each person logs in with their family account through Authelia's OIDC
(client `reitti`). Reitti makes the account on the first login
(`OIDC_SIGN_UP_ENABLED`) and keys it by Authelia's subject; the username is
the login name. Local logins are off (`DISABLE_LOCAL_LOGIN`): the login page
shows only the Authelia button, every account Reitti makes has no password,
and so has the `admin` user Reitti's migrations seed (V42 empties it). Each
person sees only their own data; Reitti's admin role is given to nobody,
so nobody can turn a geocoder back on from the settings.

The route is `auth: none` like the other OIDC apps (paperless, immich): the
app logs in itself. `loginProbe: /api/v1/timeline` answers 401 without a
session, which `laib-app check` measures.

## No location leaves the box

| What | Upstream default | Here |
|---|---|---|
| Reverse geocoding (a place's address) | Paikka (`geo.dedicatedcode.com`) and Nominatim (`nominatim.openstreetmap.org`), enabled in Reitti's database | **off**: post-deploy runs `reitti-geocoding.sql` in the database on every apply, which disables every geocoder; visits keep their coordinates and have no address until a geocoder on the box exists |
| Map tiles | the browser fetches them | Reitti fetches them through the pod's tile cache (`reitti-tiles`, nginx, kept a year in `${DATA_DIR}/reitti/tiles`); the browser talks only to Reitti |
| Street-level imagery (Panoramax) | the map's area to `api.panoramax.xyz` | **off** (`PANORAMAX_BASE_URL` empty) |
| Update check | GitHub's API, nightly | off: images change by proposal |

Tiles are still fetched once from the map servers the style names, through
the cache: the default styles use `tiles.dedicatedcode.com` (OpenStreetMap
data, OpenFreeMap), terrain from `tiles.mapterhorn.com`, the satellite style
from ArcGIS. A tile request says which area of the map someone looked at,
never a track or a visit, and a cached tile is not fetched again.

**Why no Photon yet.** A self-hosted [Photon](https://github.com/komoot/photon)
with German data is the way to addresses without the internet. Its
Germany database (`photon-db-germany-1.0`) is a 7.5 GB download and wants
several GB of RAM for its own JVM and search index; on 2026-11-02 the box
had about 11 of 31 GiB available and its swap full. That is a decision of
its own (a `photon` template, the RAM it takes), not a side effect of this
one. Once it exists, the geocoder is its URL (`http://host.containers.internal:<port>`)
in `geocode_services`, and `reitti-geocoding.sql` turns off every other one.

The tile cache (`reitti-tiles`) fetches whatever address Reitti names in its
`X-Reitti-Upstream-Url` header: it listens only inside the pod, never
published.

## Immich

Each person connects their own Immich in Reitti's settings (Integrations →
Immich): server `http://host.containers.internal:2283`, and an API key they
make in Immich under Account settings → API keys. Reitti fetches the photos on the server side; the browser
never talks to Immich for them. The template only makes Immich reachable:
Immich publishes 2283 beyond loopback, and `uses: [immich]` names the link.

## Importing

The phone's Timeline export (Google Maps app) and an old Google Takeout (`Records.json`, the semantic
location history) go in through Reitti's own upload (Settings → Import),
up to 5 GB a file (`request_body` on the route).

## Data

| Path | What | Backup |
|---|---|---|
| `${DATA_DIR}/reitti/pgdata` | PostGIS: points, visits, trips, settings | yes, stopped while it is snapshotted (`quiesce`) |
| `${DATA_DIR}/reitti/data` | uploads, avatars | yes |
| `${DATA_DIR}/reitti/tiles` | the tile cache | no, fetched again |

Redis is a cache and keeps nothing.

## Later

Live recording from the phone (OwnTracks, Overland, GPSLogger) needs
Reitti's ingest API reachable from outside, with the device's token and
past Authelia: a public route with a bypass, the owner's decision. Not
part of this template.
