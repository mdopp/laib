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
gpu:
  present: true
  owner: solaris                  # the service that holds the lease
backup:
  repo: sftp:nas:/backup/laib
  schedule: "03:00"
```

## policy.yml

```yaml
tiers: [read, propose, apply, destroy]   # destroy covers reboot and exec
hands:
  resident: { ceiling: read }
  ops:      { ceiling: propose }
  pi:       { ceiling: propose }
  claude:   { ceiling: apply, ttl: 8h }
  human:    { ceiling: destroy }
approvals:
  apply:   { by: human }        # a claude apply within a proposal still shows in the inbox
  destroy: { by: human, snapshot: required }
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
```

This file is what used to be `variables.json` + the subdomain, secret, port
and backup conventions spread over the ServiceBay codebase. It is the
irreducible part of a template: an agent writing Quadlet files from an upstream
compose file does not know on its own that port 8000 must never reach the WLAN.

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
