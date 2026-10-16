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
   for the external ones (DNS token, SMTP, Fritz!Box, GitHub, Claude OAuth),
   and the data-bound ones carried over (below). The rest is generated on
   first apply and the post-deploy hook seeds the app. Nothing runs yet.
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
5. **Werkstatt** replaces `claude-dev` + `pi-web`: one template, same
   `/workspace` volume, so checkouts and sessions survive.
6. **Solaris does not move** (it retires, ADR 0015). Its units stay
   ignored (`laib ignore`) while Haus's bridge still needs them (haus#10);
   they go off in Phase 5 (#160).
7. Disable `servicebay.service`. Keep its data dir for 30 days, then delete.
   ServiceBay's assist catalog: there is no import tool yet; what happens to
   it is decided in #160.

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

Every `laib apply` snapshots first. Re-enabling a ServiceBay unit is
`systemctl --user enable --now <unit>`; NPM comes back the same way until step
7. After step 7, rollback is a restore of the ServiceBay data dir, which is
why it stays 30 days.
