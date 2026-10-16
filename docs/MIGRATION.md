# Laib — the one migration from ServiceBay

Only one box runs ServiceBay. So: no compatibility layer, no dual control
plane for months, no feature flags. One weekend, one order, one script that
reads ServiceBay's config once.

## Running side by side

Both run at once: ServiceBay in its container on 5888, Laib as a user
service on 5900; separate state, separate nftables tables
(`servicebay_lanblock` / `laib`), separate tokens. ServiceBay's `<name>.kube`
files sit in the Quadlet root, Laib's units in `<name>/` folders. Three
things cannot be shared:

1. **A service.** Quadlet names units after the file, not the folder, and
   ServiceBay's `auth.kube` runs the same Authelia and LLDAP on the same
   data as Laib's `auth/` units, whatever those are called. `laib plan`
   reports `KONFLIKT` for every file in the Quadlet root that would run a
   service of the state beside Laib: the same name or unit name, a host port
   the service publishes, or one of its backed-up data paths (a `.kube` is
   read with its `Yaml=`; the `backup: false` shared tree is shared on
   purpose). While one is reported, every `laib apply` refuses, so
   ServiceBay's unit is stopped and its files moved out **before** the
   service goes into the state (step 4).
2. **Ports 80/443.** Caddy starts on alternative ports beside NPM (step 2).
3. **Whole-file configs.** Authelia, AdGuard rewrites and the SSH port 2222
   (claude-dev vs Werkstatt) move as a whole, never half.

## Principle

A service belongs to exactly one control plane. It is ServiceBay's while its
file is in the Quadlet root and Laib's state does not have it; it is Laib's
from the moment ServiceBay's unit is stopped, its file moved out and
`laib add <name>` puts it in the state. There is no marker and no in-between:
the `KONFLIKT` check above keeps the two apart (`migratedFrom` was dropped,
#227; ADR 0010: the kernel knows nothing about ServiceBay). A Quadlet unit
cannot be disabled while its file is there, so "disable" always means the
file moves out.

## Order

1. **Laib binary + state repo**, nothing applied. `laib init --domain …`,
   then `scripts/migrate-servicebay.py --config /mnt/data/servicebay/config.json
   --templates <servicebay>/templates,<each external registry>/templates
   --out /mnt/data/laib/migration` writes a draft `<name>/` per installed
   template — laib.yml with the routes the box's proxy hosts have and the auth
   ServiceBay applied, data paths from the Pod YAML's host paths, secrets and
   ports from `variables.json`; values.yml from `installedVariables` — and
   prints what it could not map: proxy hosts and variables without an
   installed service, named volumes, data the Laib built-in would not mount.
   The drafts never go into the state repo (the script refuses): a
   `services/<name>/` without units makes laib refuse the whole state, and
   `laib add <name>` refuses one that exists (#226). Where Laib has a
   built-in, the draft is what to compare `laib add <name>` against; the
   others get their units from their own repo's `laib.yml` or by hand
   (ADR 0004: `.container` is the primary form). Secrets: `laib secret set`
   for the external ones (DNS token, SMTP, Fritz!Box, GitHub, Claude OAuth),
   and the data-bound ones carried over (below). The rest is generated on
   first apply and the post-deploy hook seeds the app. Nothing runs yet.
2. **Caddy beside NPM** on a different port, `laib apply caddy`. Routes are
   verified against `127.0.0.1` before DNS moves. Then AdGuard rewrites
   switch, NPM's unit is disabled. SSO forward-auth now goes through Caddy;
   Authelia's config is still ServiceBay's at this point and stays compatible
   because the rules are Authelia's, not the proxy's (ServiceBay ADR 0005, 0006).
3. **auth** (Authelia + LLDAP), by the a–c of step 4: ServiceBay's
   `auth.kube` out first, then `laib add auth`. OIDC clients are rendered
   from every `services/*/laib.yml`, which at this point is the full list,
   so no client is dropped (the #1724 failure class cannot occur).
4. **adguard**, then **llama** as the `model` role (ADR 0011): the template
   moves from solarisbay into Laib with the lease sidecar; Its consumers
   (foundry-chronicle now, the Werkstatt's pi from step 5) speak Laib's lease contract (ADR 0011,
   amendment 2026-10-14): a preset instead of a window name, a holder, the
   model key. Then **vaultwarden, radicale, mosquitto, file-share, beets,
   immich, media, home-assistant** in that order: small and stateless first,
   the ones with migrations last. Each one, in this order (the service is
   down from a to c; that is the price of never having two writers on one
   database):
   a. Stop ServiceBay's unit and move its files out of the Quadlet root:
      ```sh
      systemctl --user stop <name>.service
      mkdir -p ~/servicebay-units
      mv ~/.config/containers/systemd/<name>.kube ~/.config/containers/systemd/<name>.yml ~/servicebay-units/   # or <name>.container
      systemctl --user daemon-reload
      ```
   b. Carry its secrets over (#228) and, where "Data that moves into
      Laib's layout" lists it, `scripts/move-servicebay-data.sh <name>`
      (#229).
   c. The `laib add <name> …` line `migrate-servicebay.py` printed for it
      (the box's values, a database's image pinned; it applies). `laib plan`
      shows no `KONFLIKT`, the apply ends green, the app shows the old data.
5. **Werkstatt** replaces `claude-dev` + `pi-web`: one template; their git
   checkouts are copied into the Werkstatt's workspace (below). Their
   sessions and SSH host keys are an open decision (below).
6. **Solaris does not move** (it retires, ADR 0015). Its units stay
   ignored (`laib ignore`) while Haus's bridge still needs them (haus#10);
   they go off in Phase 5 (#160).
7. Disable `servicebay.service`. Keep its data dir for 30 days, then delete.
   ServiceBay's assist catalog: there is no import tool yet; what happens to
   it is decided in #160.

## Data that moves into Laib's layout (#229)

The built-ins keep their own layout. They do not learn ServiceBay's paths;
the data moves into theirs once, per service, after ServiceBay's unit is out
(step 4) and before `laib add`:

```sh
scripts/move-servicebay-data.sh --dry-run <service>   # what it would do
scripts/move-servicebay-data.sh <service>             # on the box, as core
```

It **copies** and never moves. ServiceBay's paths stay as they were, so
handing a service back is re-enabling its unit, and they go with ServiceBay's
data dir after 30 days. Two files are edited in place, and their ServiceBay
version is kept beside them as `<file>.servicebay`; a rollback puts those
back. It refuses a service whose ServiceBay file is still in the Quadlet
root, or whose target already holds data, before writing anything of it.

| Service | ServiceBay on the box | Laib | Step |
|---|---|---|---|
| vaultwarden | `vaultwarden/` (db.sqlite3, rsa_key.pem in it) | `vaultwarden/data` | copied |
| home-assistant | `home-assistant/matter-server` | `home-assistant/matter` | copied: the Matter fabric survives |
| home-assistant | Z-Wave JS server on 3001 through `sb-external-settings.json` (3000 was NPM's) | zwave-js-ui's own `settings.json`, its default 3000 | edited; Home Assistant's Z-Wave JS entry (`.storage/core.config_entries`) follows to `:3000` |
| file-share | podman named volume `file-share-syncthing-config` | `file-share/syncthing-config` | copied: device ID and keys survive, nothing is re-paired |
| file-share | `file-share/filebrowser-db/filebrowser.db` | `file-share/filebrowser/filebrowser.db` | copied: users, scopes and share links survive; `filebrowser-config` stays behind (the unit configures File Browser) |
| werkstatt | `claude-dev/workspace`, `pi-web/workspace` | `werkstatt/workspace` | the git checkouts copied, owned by the Werkstatt's `dev` (1000); pi-web's as `pi-web-<name>` where claude-dev has the same one |

Everything else the built-ins mount sits at ServiceBay's paths already (auth,
adguard, the Home Assistant config and the Z-Wave store, immich upload and
pgdata, paperless, radicale, media, mosquitto, beets, esphome, llama) and is
adopted in place.

**Values and images.** `scripts/migrate-servicebay.py` prints one
`laib add <name> …` line per built-in. It carries the box's values under
Laib's names: `BASE_DN` from `LLDAP_BASE_DN` (ServiceBay derived a blank one
from the domain), `MQTT_USER` from `MQTT_USERNAME` (the box's is `mqtt`), and
`SHARE_USER`. **The migration never downgrades a database.** If the box runs
another tag than the built-in's default for a container Laib does not update
itself (the ones that keep a database), the line pins the digest the box
runs: `LLDAP_IMAGE=$(podman image inspect --format '{{index .RepoDigests 0}}'
docker.io/lldap/lldap:latest)`. This covers LLDAP `:latest` against
`:stable`, Paperless `:beta` against `:latest` and Authelia `:latest` against
`:4`. Moving to the built-in's tag later is a deliberate update, a values
change with its snapshot.

**Open, the operator's decision (#229).** Until it is made, the default
applies:

- **Immich's external libraries.** The box mounts all of `file-share/data`
  at `/mnt/photos`, and the libraries are `/mnt/photos/<user>/photos` and
  `/mnt/photos/photos`. The built-in mounts `PHOTOS_RO`, by default
  `file-share/data/photos`, there. Default: those library paths no longer
  exist and Immich marks their assets offline.
- **Samba's per-user accounts.** ServiceBay synced LLDAP users into Samba;
  the built-in has the one `SHARE_USER`. Default: devices mount with that
  account; the personal ones are gone after the first restart.
- **Home Assistant's Authelia login** (`auth_oidc`, client `homeassistant`).
  The built-in declares no `oidc:`, so Authelia no longer knows the client.
  Default: the login button fails, and users log in with their Home
  Assistant password. A user who only ever used Authelia needs one set
  before the move.
- **The Werkstatt's sessions and SSH host keys.** ServiceBay kept them in
  `/workspace`; the Werkstatt keeps its home in the image. Default: sessions
  start fresh, and the host key changes once (the app asks to trust it again).

## What does not move

- The 28 JSON stores, the twin, the approval and audit stores. The journal
  starts empty on day one; the old audit log stays in the ServiceBay data dir
  for 30 days.
- ServiceBay API tokens. All agents get new hand tokens.
- The TUI, the portal, the companion app's `/napi` (Haus owns it now; until haus#10 the rest goes through Haus's Solaris bridge).

## Secrets that carry over (#228)

The data on the box was made with these values. Laib must start with the same
ones, or the app is locked out of its own database or config:

| Laib secret | ServiceBay name | What a new value breaks |
|---|---|---|
| `LLDAP_ADMIN_PASSWORD` | same | LLDAP reads it only when it creates the DB: Authelia's bind, the auth post-deploy, Radicale's seeding |
| `AUTHELIA_STORAGE_KEY` | `AUTHELIA_STORAGE_ENCRYPTION_KEY` | Authelia refuses its `db.sqlite3`; every TOTP/WebAuthn registration is lost |
| `IMMICH_DB_PASSWORD` | `DB_PASSWORD` | immich-server cannot reach its Postgres |
| `PAPERLESS_DB_PASSWORD` | same | the same for Paperless |
| `MQTT_PASSWORD` | same | every device and Home Assistant lose the broker |
| `JELLYFIN_ADMIN_PASSWORD` | same | the media post-deploy cannot log in to Jellyfin |
| `ADGUARD_ADMIN_PASSWORD` | same | post-deploy cannot sync the rewrites into the kept `AdGuardHome.yaml` |
| `SAMBA_PASSWORD` | `SHARE_PASSWORD` | devices that mount the share with the old account |

They are set **before the service's first apply**, which is `laib add`:
`secrets.age` is one flat map, and every apply generates whatever secret a
service in the state is missing, so a value not carried by then is a new one.
Carrying all of them at step 1 is safe; the services are not in the state yet.

```sh
# on the box, as core, while ServiceBay's container still runs
scripts/carry-servicebay-secrets.sh    # all eight, or name services (… auth immich); LAIB=/var/opt/laib/laib if laib is not on PATH
```

The values live sealed in ServiceBay's `config.json` (`installedSecrets`,
AES-256-GCM with `secret.key` beside it). The host has no Python and openssl
has no GCM, so the script unseals each value inside ServiceBay's container
with its node and pipes it straight into `laib secret set <Laib name>`. No
value is passed on argv, kept in a variable or file, or shown. A value the
store does not have is reported and the script exits non-zero. Read it
before the apply; it does not set a substitute. The store holds what
ServiceBay deployed. A password changed later inside the app (in LLDAP's UI,
say) is not in it, and the first apply's verify shows that.

One value goes with them: the box's broker user is `mqtt` and the built-in's
default is `homeassistant`, so `laib add mosquitto MQTT_USER=mqtt`.

## Rollback during the weekend

Every `laib apply` snapshots first. Handing a service back to ServiceBay is
`laib remove <name>`, then its files back from `~/servicebay-units/` into the
Quadlet root, `systemctl --user daemon-reload`, `systemctl --user start
<name>.service`; NPM comes back the same way until step 7. After step 7,
rollback is a restore of the ServiceBay data dir, which is why it stays 30
days.
