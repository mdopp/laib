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
| browser only, behind Authelia | `auth: one_factor` / `two_factor` | admin surfaces people open in a browser; forward-auth does the login | laib, sync (Syncthing), ldap, dns, adguard, whoami |
| own login or OIDC | `auth: none` | the app has native clients that cannot pass forward-auth, or logs in through Authelia's OIDC itself | media (Jellyfin, LDAP-Auth plugin against LLDAP), paperless, files (File Browser), auth, immich, vaultwarden, hoerbuecher (Audiobookshelf), caldav (Radicale, LLDAP accounts), home-assistant |

OIDC clients (immich, vaultwarden, audiobookshelf) fetch Authelia's
discovery document over HTTPS: they need a certificate they trust — real
certificates (#45, the trial domain at deSEC), never `tls: internal`.
Radicale binds LDAP with LLDAP's read-only account `radicale`
(`lldap_strict_readonly`, secret `RADICALE_LDAP_PASSWORD` seeded by the
auth template), never as the admin. Home Assistant's integrations (three
Wyoming services, CalDAV, Music Assistant, MQTT, the MCP Server, Frage as
conversation agent) are set up by hand once; with the optional secret
`HA_TOKEN` (a long-lived HA token) `laib diag` lists what is still missing.

| Template | Role | Notes |
|---|---|---|
| adguard | dns | AdGuard Home on the host network; pre-start writes the first config with the admin user, post-deploy syncs laib's rewrites via the API |
| caddy | proxy | host network (the one carve-out), reads `render/caddy/Caddyfile` |
| auth | sso, identity | Authelia + LLDAP in one pod; pre-start writes configuration.yml and the OIDC key, post-deploy seeds LLDAP |
| whoami | — | the smallest complete example: route, SSO, OIDC, secret, LAN-blocked port |
| werkstatt | — | v0 of the one dev container: Claude Code, pi, repos, sshd for the mobile app; Laib as MCP for every session |
| bote | — | Hermes as stock with signal-cli: the agent you talk to; config rendered by the hook (ADR 0016) |
| frage | — | "was ist …": SearXNG + model summary; /ask and an OpenAI-compatible endpoint for Assist and the Bote (ADR 0015) |
| media | — | Jellyfin for music and video with GPU; the hook finishes the wizard, creates the admin and a phone user limited to Musik, registers libraries |
| audiobookshelf | — | Hörbücher und Podcasts with auto-download and own apps; replaces the hand-rolled podcast script and takes audiobooks out of Jellyfin |
| music-assistant | — | the house's player brain for Assist: radio, playlists, podcasts on the speakers; host network for discovery |
| radicale | — | Kalender und Kontakte over CalDAV/CardDAV, LLDAP logins, the household's store (ADR 0017) |
| vaultwarden | — | Passwörter with SSO through Authelia's OIDC client |
| mosquitto | — | MQTT broker; password file by the hook; 1883 open to the LAN for devices |
| esphome | — | ESPHome dashboard and OTA, host network for discovery |
| beets | — | music tagging over the file share's music folder, admins only |
| home-assistant | — | Home Assistant, Z-Wave JS UI and the Matter server on the host network; the house and the voice brain (ADR 0015) |
| immich | — | photos: server, GPU machine learning, Postgres with vectors, Redis in one pod; OIDC via Authelia |
| file-share | — | Syncthing, Samba and File Browser over the shared data folder on the host network |
| stimme | — | Whisper, Piper, openWakeWord and the wake-word trainer as Wyoming units for Assist |
| paperless | — | Paperless-ngx with Postgres and Redis; logins through Authelia's forward auth; consume folder on the file share |
