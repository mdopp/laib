# Laib — the state repo

Path on the box: `/mnt/data/laib/state`, a Git repository owned by `core`.
Every file is YAML except `secrets.age` and the journal.

```
box.yml
policy.yml
secrets.age
services/
  <name>/
    laib.yml          the contract (copied from the template, may be edited)
    values.yml        the variables
    *.container       Quadlet units with ${VAR} placeholders (from the template)
    *.pod
    rendered/         written only by laib: the files as they are on the box
ignore.yml            units on the box that are deliberately unmanaged
```

The journal is *not* in the repo: `/mnt/data/laib/journal/YYYY-MM-DD.jsonl`.

## box.yml

```yaml
domain: example.home
timezone: Europe/Berlin
dataDir: /mnt/data/stacks        # ${DATA_DIR} for templates
roles:                            # which service plays which platform role
  proxy: caddy
  sso: authelia
  identity: lldap
  dns: adguard
  model: llama                    # llama-server + lease, needs gpu.present (ADR 0011)
laib:                             # the box page and /mcp (ADR 0012); all optional
  host: laib                      # laib.<domain>, Authelia group below
  port: 5900
  listen: 127.0.0.1               # 0.0.0.0 so pods reach /mcp; the LAN is dropped by nftables
  group: admins
gpu:
  present: true
  owner: solaris                  # the service that holds the lease
backup:
  repo: sftp:nas:/backup/laib
  schedule: "03:00"
hostNetwork: [caddy, llama]       # the closed carve-out list (#16)
notify:
  url: http://127.0.0.1:8787/api/laib-events   # red results, proposals, readiness (#15)
updates:
  days: [Sat, Sun]                # podman auto-update window (#13); empty = podman's daily
  time: "03:00"
model:                            # a box without a model role (ADR 0018)
  url: http://192.168.1.20:11435/v1
  keySecret: MODEL_API_KEY        # only for a cloud API
  lease: http://192.168.1.20:8787/api/model-lease
operator:                         # laib-operator (ADR 0013); laib operator enable sets this
  enabled: true
  model: gemma-4-e4b
  reportAt: "07:00"
  tick: 10m
peers:                            # the other boxes of the household
  kraftwerk: {lanIp: 192.168.1.20, laib: http://192.168.1.20:5900}
```

Builtins for units and hooks: `${MODEL_URL}`, `${MODEL_KEY}`,
`${MODEL_LEASE}` resolve from the model role when present, else from
`model`.

## policy.yml

```yaml
tiers: [read, propose, apply, destroy]   # destroy covers reboot and exec
hands:
  resident: { ceiling: read }
  ops:      { ceiling: apply }  # the operator (ADR 0013)
  pi:       { ceiling: propose }
  claude:   { ceiling: apply, ttl: 8h }
  human:    { ceiling: destroy }
approvals:
  destroy: { by: human, snapshot: required }
autonomy:                       # the repair dial (docs/DESIGN.md "Repair")
  converge: on                  # ten-minute loop: re-apply drift, restart inactive units, budget 3/h
  values: auto                  # auto | proposal; auto = laib applies and rolls back on red
  template-update: auto
  patch: auto
  new-service: proposal         # adds surface: a human decides
  # remove is never auto; a public route is always a human's call
```

A hand never approves its own proposal. `destroy` requires a human on the box
page or a Solaris card; a token cannot approve.

## services/<name>/laib.yml

```yaml
name: paperless
template: { registry: builtin, name: paperless, version: 3 }
routes:
  - host: paperless                 # paperless.<domain>
    port: 8000
    exposure: internal              # internal | public
    auth: one_factor                # none | one_factor | two_factor
    group: family                   # Authelia subject group, optional
    caddy: |                        # rare extra directives
      request_body { max_size 200MB }
secrets:
  - name: PAPERLESS_SECRET_KEY      # generated once, reused
  - name: PAPERLESS_ADMIN_PASSWORD
  - name: GITHUB_TOKEN
    external: true                  # operator-supplied, never generated
oidc:
  clientId: paperless
  redirectUris: ["https://paperless.${DOMAIN}/accounts/oidc/authelia/login/callback/"]
data:
  - path: ${DATA_DIR}/paperless/data    # backed up
  - path: ${DATA_DIR}/paperless/media
    backup: false                       # bulk, skipped
health:
  http: http://127.0.0.1:8000/api/      # verify after apply
ports:
  - { port: 8000, lan: false }          # nftables drop on physical interfaces
gpu: false
provides: [documents]            # purposes; a second service for one purpose is a HINWEIS in plan and Inbox
after: [authelia]                # restarted and verified before this one
```

This file is what used to be `variables.json` + the subdomain, secret, port
and backup conventions spread over the ServiceBay codebase. It is the
irreducible part of a template: an agent writing Quadlet files from an upstream
compose file does not know on its own that port 8000 must never reach the WLAN.

## services/<name>/post-deploy (optional)

An executable. Runs after `laib apply` verified the service green, in the
service folder, with `LAIB_SERVICE`, `LAIB_DOMAIN`, `LAIB_DATA_DIR`,
`LAIB_TZ`, `LAIB_LAN_IP`, every `values.yml` key (secrets resolved) and
`LAIB_SECRET_<NAME>` for every declared secret in its environment. Ten-minute
timeout. Its output is journaled with secrets redacted; a non-zero exit turns
the apply red. A line `laib-secret: NAME=value` on stdout stores a credential
the hook created (an API token, a generated admin password) into
`secrets.age`, so it is never printed again and survives a reinstall.

This is where ServiceBay's `post-deploy.py` work lives on: first-run
provisioning that only the app can do (Jellyfin's setup wizard, Immich's
admin sign-up, Home Assistant onboarding, AdGuard's admin user, LLDAP
seeding, a Mosquitto integration in Home Assistant). What those scripts also
did, Laib renders instead: proxy hosts, OIDC clients, Authelia rules, LAN
blocks, credential surfacing. A hook must be idempotent: it runs on every
apply that touches the service.

## values.yml

Plain `KEY: value`. Secrets are referenced, never written here:
`PAPERLESS_SECRET_KEY: !secret PAPERLESS_SECRET_KEY`.

## secrets.age

A YAML map `KEY: value`, encrypted to the box's `age` identity. The identity
lives at `/mnt/data/laib/age.key` and once on paper. Backups include
`secrets.age`; the key is the one thing kept outside the box. A secret declared
in `laib.yml` without `external: true` is generated on first apply and never
regenerated.

## Journal line

```json
{"t":"2026-10-08T09:15:02Z","actor":"claude","verb":"apply","service":"media","reason":"Jellyfin 10.11","commit":"c81f2a","result":"green","ms":41200}
```

Actors: `human:<user>`, `resident`, `ops`, `pi`, `claude`, `laib` (timers,
boot). A failed verify is `"result":"red"` with a `detail` field.

### Secrets that may stay unset

A contract secret with `external: true` must be in `secrets.age` before
`laib add`/`approve` renders the service; with `optional: true` as well it
may be missing and the unit gets an empty value. Caddy's `DNS_API_TOKEN`
is the case: needed only when `dns.provider` is set.

### `tls`

`tls: internal` makes Caddy sign every host with its own CA (`local_certs`)
instead of Let's Encrypt. For a trial box whose domain the house's port 80
does not reach (the NUC behind the Kraftwerk) or a domain whose DNS has no
API (Strato). Trust `data/caddy/pki/authorities/local/root.crt` on the
laptop and the phones. Empty = Let's Encrypt: HTTP-01 when `dns.provider`
is empty (port 80 must reach the box, as today), DNS-01 otherwise.
