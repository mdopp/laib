# A Laib template

A template is a folder: Quadlet units with `${VAR}` placeholders, one
`laib.yml` contract, a `values.example.yml` (ADR 0004). No Mustache, no
logic, no forms. The contract is the part an agent cannot guess from an
upstream compose file: which port must never reach the WLAN, which value
is a secret, what is backed up, who may log in. The full reference is the
laib repo's `docs/STATE.md` ("services/<name>/laib.yml"); the smallest
complete example is `templates/whoami`, a real app with OIDC is
`templates/vaultwarden`, one with LDAP is `templates/radicale`.

## From an upstream app

1. Read upstream's compose file or install docs. `podlet compose
   docker-compose.yml` gives a first set of `.container` units; one
   container per unit, several in one `.pod` when they share localhost.
2. Write `laib.yml` (below) and `values.example.yml`, replace literals by
   `${VAR}`. Builtins you never declare: `${DOMAIN}`, `${DATA_DIR}`,
   `${TZ}`, `${SERVICE}`, `${LAN_IP}`, `${MODEL_URL}`, `${MODEL_KEY}`,
   `${MODEL_LEASE}`.
3. Write one German sentence for the household at the top of `news`
   (below the `#` comment), what this change brings them, in their words:
   „Joshua lernt die Namen deiner Geräte“, never a key, unit or file;
   `${PERSONA_DISPLAY}` is the house persona's name. Every change of a
   template gets its own new line, CI fails without one
   (`scripts/drift.sh`); a template update's card shows the lines the box
   does not have yet as its headline (#687).
4. `laib template lint <dir>` until it is quiet. Then the same path as an
   app: a PR to the registry repo, `laib template sync`, `laib propose
   --add` (`apps.md` steps 4–7).

## laib.yml, the parts that matter

```yaml
name: <name>
template: {registry: apps, name: <name>, version: 1}
provides: [<purpose>]          # photos, documents …: a second service for one purpose is a HINWEIS
routes:
  - host: <name>               # <name>.<domain>
    port: 8000                 # the host port Caddy proxies to (published on 127.0.0.1)
    exposure: internal         # internal | public — public is always the owner's decision
    auth: one_factor           # none | one_factor | two_factor
    group: family              # family | admins (LLDAP groups)
secrets:
  - name: <NAME>_SECRET_KEY    # generated once on the box, never regenerated
  - name: <NAME>_API_TOKEN
    external: true             # the owner sets it: `laib secret set` (value on stdin)
data:
  - path: ${DATA_DIR}/<name>/data     # backed up
  - path: ${DATA_DIR}/<name>/cache
    backup: false
health:
  http: http://127.0.0.1:8000/health  # or none: "<why>"
ports:
  - {port: 8000, lan: false}   # nftables drops it on the physical interfaces
after: [auth]                  # started and verified after these services
```

- **Secrets** are `!secret NAME` in `values.yml`, never a literal; they
  reach the container as podman secrets, off every argv.
- **Ports:** publish on `127.0.0.1:<port>:<container port>` and declare
  `lan: false`, unless a device on the LAN must reach it directly (MQTT,
  a media client): then `lan: true`. A port two templates use is refused.
- **Volumes:** only declared data paths or rendered files; anything else
  is refused by lint. `:z` (shared label) for data; never relabel a tree
  another service shares (#230).
- **Containers reaching each other:** inside one pod `127.0.0.1:<port>`;
  across services `host.containers.internal:<port>` (the port must be
  published beyond loopback for that); never a hard-coded IP.
- **Databases:** `quiesce: true` on the data path, no `AutoUpdate=registry`
  on the unit; the image moves by proposal.
- **Hooks** (`pre-start`, `post-deploy`) are a last resort, run in the
  hooks image, and print `laib-secret:` / `laib-exec:` lines (ADR 0004
  amendment). Proxy hosts, OIDC clients, Authelia rules and DNS rewrites
  are rendered by Laib: never write a hook for them.

## Who logs in, and how

**A service is usable after `laib add` without filling a form.** Logins
come from LLDAP/Authelia (directly, via OIDC or via Home Assistant);
service accounts are minted by Laib. A first-run wizard, an app's own
admin account, a source or integration that needs a login: the template's
`post-deploy` does it through the app's API, with a generated secret or a
token another service's hook hands back (`laib-secret:`, STATE.md). The
admin it creates is break-glass, never the family's login. Examples:
`media` (Jellyfin's wizard, LDAP login), `home-assistant` (onboarding, the
tokens it mints), `music-assistant` (onboarding, a Jellyfin service
account, login through Home Assistant, #526).

The owner's rule, in this order:

1. **Native OIDC against Authelia** when the app supports it (Immich,
   Vaultwarden, Audiobookshelf …): `oidc: {clientId, redirectUris,
   scopes}` in `laib.yml`, the client secret as `!secret
   OIDC_<CLIENTID>_SECRET` in values, the issuer `https://auth.${DOMAIN}`.
   The route is `auth: none` — the app logs in itself, and forward-auth
   would break its phone clients.
2. **LDAP against LLDAP** when the app has LDAP and no OIDC (Jellyfin,
   Radicale): bind with a read-only account the auth template seeds, never
   as the admin; route `auth: none`.
3. **Authelia forward-auth** (`auth: one_factor`, `group: …`) for
   everything a browser opens and that has no login of its own, including
   your own small apps. If the app reads `Remote-User`, add
   `proxySecret: <a declared secret>` to the route and trust the header
   only when `X-<Service>-Proxy` matches (ADR 0012 §3).

`auth: none` without the app's own login is never right. A route with
`auth: none`, a `bypass:` path, a weaker login or `exposure: public` is
marked in the Inbox card and is the owner's decision.

## The proof

Lint proves the contract, not the service. A template is done when it is
green on the box (`laib status`) and its route passes `laib-app check
<name>.<domain>`. An `auth: none` route declares `loginProbe: <path>`,
a path the app refuses without a session (401 or 403, an API like
`/api/me`); the check measures the app's own login there (#774).
