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

1. **A unit name.** Quadlet names units after the file, not the folder:
   `media.kube` and `media/media.container` both produce `media.service`.
   `laib plan` reports this as `KONFLIKT` and `laib apply` refuses it; stop
   and remove ServiceBay's file first, then apply.
2. **Ports 80/443.** Caddy starts on alternative ports beside NPM (step 2).
3. **Whole-file configs.** Authelia, AdGuard rewrites and the SSH port 2222
   (claude-dev vs Werkstatt) move as a whole, never half.

## Principle

A service belongs to exactly one control plane, recorded in
`services/<name>/laib.yml` (`migratedFrom: servicebay` until the end). While a
service is still ServiceBay's, Laib shows it as `nicht verwaltet` and does not
touch it. While a service is Laib's, ServiceBay's unit for it is disabled.

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
   for the external ones (DNS token, SMTP, Fritz!Box, GitHub, Claude OAuth)
   and for every line under "carry over before the first apply" — the data
   on the box was made with those values, and a fresh one locks the app out
   of its own database (#228). The rest is generated on first apply and the
   post-deploy hook seeds the app. Nothing runs yet.
2. **Caddy beside NPM** on a different port, `laib apply caddy`. Routes are
   verified against `127.0.0.1` before DNS moves. Then AdGuard rewrites
   switch, NPM's unit is disabled. SSO forward-auth now goes through Caddy;
   Authelia's config is still ServiceBay's at this point and stays compatible
   because the rules are Authelia's, not the proxy's (ServiceBay ADR 0005, 0006).
3. **auth** (Authelia + LLDAP): `laib apply auth`. OIDC clients are rendered
   from every `services/*/laib.yml`, which at this point is the full list,
   so no client is dropped (the #1724 failure class cannot occur).
4. **adguard**, then **llama** as the `model` role (ADR 0011): the template
   moves from solarisbay into Laib with the lease sidecar; Its consumers
   (foundry-chronicle now, the Werkstatt's pi from step 5) speak Laib's lease contract (ADR 0011,
   amendment 2026-10-14): a preset instead of a window name, a holder, the
   model key. Then **vaultwarden, radicale, mosquitto, file-share, beets,
   immich, media, home-assistant** in that order: small and stateless first,
   the ones with migrations last. Each one: `laib plan <name>`, read the diff,
   `laib apply <name>`, verify green, disable ServiceBay's unit.
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

## Rollback during the weekend

Every `laib apply` snapshots first. Re-enabling a ServiceBay unit is
`systemctl --user enable --now <unit>`; NPM comes back the same way until step
7. After step 7, rollback is a restore of the ServiceBay data dir, which is
why it stays 30 days.
