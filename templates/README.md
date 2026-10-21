# Built-in templates

A template is a folder: Quadlet files with `${VAR}` placeholders, a `laib.yml`
contract and a `values.example.yml` (ADR 0004). Install one by copying the
folder to `services/<name>/` in the state repo, renaming
`values.example.yml` to `values.yml`, filling it, and running `laib plan`.

`laib template lint templates/<name>` validates a folder exactly like the
state loader would.

## Route policy

Two kinds of route, written into every `laib.yml` (#55):

| Kind | `auth:` | Why | Templates |
|---|---|---|---|
| browser only, behind Authelia | `auth: one_factor` / `two_factor` | surfaces people open in a browser; forward-auth does the login | laib, sync (Syncthing), files (File Browser, proxy auth on Remote-User), ldap, dns, adguard, whoami |
| own login or OIDC | `auth: none` | the app has native clients that cannot pass forward-auth, or logs in through Authelia's OIDC itself | media (Jellyfin, LDAP-Auth plugin against LLDAP), paperless (OIDC, account on the first login), auth, immich, vaultwarden, hoerbuecher (Audiobookshelf), caldav (Radicale, LLDAP accounts), home-assistant |

An `auth: none` route fronts an app that really logs in on its own: none
of its units may trust `Remote-User` or turn the app's login off
(`internal/render/auth231_test.go`, #231). Caddy strips every client-sent
`Remote-*` header on every site before forward-auth adds Authelia's, so
only an app behind Authelia ever sees one. File Browser makes a family
member's account on the first visit, without admin rights; the LLDAP
account in `FILEBROWSER_ADMIN_USER` is its admin from the first start, as
in ServiceBay. An unauthenticated control port on the host network (Z-Wave JS,
Matter) is declared `lan: false` or listens on loopback.

OIDC clients (immich, vaultwarden, audiobookshelf, paperless) fetch Authelia's
discovery document over HTTPS: they need a certificate they trust — real
certificates (#45, the trial domain at deSEC), never `tls: internal`.
Radicale binds LDAP with LLDAP's read-only account `radicale`, file-share reads the family with `file-share` (`FILE_SHARE_LDAP_PASSWORD`),
(`lldap_strict_readonly`, secret `RADICALE_LDAP_PASSWORD` seeded by the
auth template), never as the admin. Home Assistant's integrations (three
Wyoming services, CalDAV, MQTT, the MCP Server) are set up
by hand once; Frage as conversation agent is added by home-assistant's
post-deploy once Frage answers (HA's Ollama integration on Frage's Ollama
API, #491), Music Assistant once music-assistant's post-deploy handed it a
token (#526); with `HA_TOKEN`, which
home-assistant's post-deploy mints like Haus's and the Bote's (#349),
`laib diag` lists what is still missing.

| Template | Role | Notes |
|---|---|---|
| adguard | dns | AdGuard Home on the host network; pre-start writes the first config with the admin user, post-deploy syncs laib's rewrites via the API |
| caddy | proxy | host network (the one carve-out), reads `render/caddy/Caddyfile` |
| auth | sso, identity | Authelia + LLDAP in one pod; pre-start writes configuration.yml and the OIDC key, post-deploy seeds LLDAP |
| whoami | — | the smallest complete example: route, SSO, OIDC, secret, LAN-blocked port |
| werkstatt | — | v0 of the one dev container: Claude Code, pi, repos, sshd for the mobile app; Laib as MCP for every session |
| bote | — | Hermes as stock with signal-cli: the agent you talk to; config rendered by the hook (ADR 0016) |
| frage | — | "was ist …": SearXNG + model summary; /ask, /mcp for the Bote, an OpenAI-compatible endpoint, and Ollama's /api/chat for Assist (HA's Ollama integration, #491); pre-start writes SearXNG's settings.yml (ADR 0015) |
| media | — | Jellyfin for music and video with GPU; the hook finishes the wizard, creates the local admin, registers libraries, logs the family in through LLDAP (read-only bind) with Quick Connect |
| audiobookshelf | — | Hörbücher und Podcasts with auto-download and own apps; replaces the hand-rolled podcast script and takes audiobooks out of Jellyfin |
| music-assistant | — | the house's player brain for Assist: radio, playlists, podcasts on the speakers; host network for discovery; the hook onboards it, adds Jellyfin (its own account `musicassistant`) and Radio Browser, and the family signs in through Home Assistant (#526) |
| radicale | — | Kalender und Kontakte over CalDAV/CardDAV, LLDAP logins, the household's store (ADR 0017) |
| kalender | — | calendars from outside (a Google calendar's secret iCal address, a public ICS) mirrored one way every 15 min into the person's read-only Radicale collection `google-<name>` / `abo-<name>`, as the service account `kalender`, plus copies for Home Assistant's own account (#495); entered in Haus, kept by `laib calendar`; a timer, no daemon; findings in diag |
| vaultwarden | — | Passwörter with SSO through Authelia's OIDC client |
| mosquitto | — | MQTT broker; password file by the hook; 1883 open to the LAN for devices |
| esphome | — | ESPHome dashboard and OTA, host network for discovery |
| beets | — | music tagging over the file share's music folder, admins only |
| home-assistant | — | Home Assistant, Z-Wave JS UI and the Matter server on the host network; the house and the voice brain (ADR 0015) |
| immich | — | photos: server, GPU machine learning, Postgres with vectors, Redis in one pod; OIDC via Authelia |
| file-share | — | Syncthing, Samba and File Browser over the shared data folder on the host network; Samba has one account per LLDAP user in family or admins (read with the read-only `file-share` account), each password set by the person in Haus, the shared `SHARE_USER` as fallback (#296) |
| stimme | — | Whisper, Piper, openWakeWord and the wake-word trainer as Wyoming units for Assist |
| paperless | — | Paperless-ngx with Postgres and Redis; logins through Authelia's OIDC, the apps with tokens; consume folder on the file share; a mail inbox from `PAPERLESS_MAIL_*` (README.md) |
| usage-metrics | — | aggregate, anonymous usage counters (mdopp/usage-metrics); no route, in-box callers only on 8094 with the generated `USAGE_METRICS_TOKEN` |
