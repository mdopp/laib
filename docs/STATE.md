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
skills/<name>.yml     an Operator skill added or replaced on this box (ADR 0023)
wishes/<time>.md      an Operator's wish, added by its proposal; approving it files the intake issue (#270)
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
  repo: /mnt/data/backup/restic
  schedule: "03:00"
  offsite:                        # the copy off the data disk (#391): restic copy after each backup
    repo: rclone:nas:laib-restic  # rclone's backend, served by the rclone container (no host binary)
    env: {RCLONE_CONFIG_NAS_TYPE: ftp, RCLONE_CONFIG_NAS_HOST: 192.168.178.1, RCLONE_CONFIG_NAS_USER: laib,
          RCLONE_CONFIG_NAS_EXPLICIT_TLS: "true", RCLONE_CONFIG_NAS_NO_CHECK_CERTIFICATE: "true", RCLONE_CONFIG_NAS_CONCURRENCY: "2"}
    secrets: [RCLONE_CONFIG_NAS_PASS]   # rclone obscure's output, in secrets.age
dns:                              # DNS-01: one wildcard certificate *.<domain> (#378); absent = HTTP-01 per name
  provider: desec                 # a module in ghcr.io/mdopp/laib-caddy (caddy/Containerfile)
  secretRef: DNS_API_TOKEN        # laib secret set DNS_API_TOKEN (stdin)
  challengeAlias: example-acme.dedyn.io   # only when _acme-challenge.<domain> is a CNAME into the provider's zone
hostNetwork: [caddy, llama]       # the closed carve-out list (#16)
notify:
  url: http://127.0.0.1:8787/api/laib-events   # red results, proposals, readiness (#15)
  mail: owner@example.home        # laib's mails (#393): red for 10 min, once a week the updates; empty: the sso
                                  # service's OPERATOR_EMAIL. Sent with Authelia's SMTP_* values and AUTHELIA_SMTP_PASSWORD
updates:
  days: [Sat, Sun]                # podman auto-update window (#13); empty = podman's daily
  time: "03:00"
model:                            # a box without a model role (ADR 0018)
  url: http://192.168.1.20:11435/v1
  keySecret: MODEL_API_KEY        # only for a cloud API
  lease: http://192.168.1.20:11435/api/model-lease
intake:                           # wishes and escalations become tickets (#56); token GITHUB_INTAKE_TOKEN in secrets.age
  repo: mdopp/laib
  label: wunsch                   # a person triages; pi:ready is set by hand
operator:                         # laib-operator (ADR 0013); laib operator enable sets this
  enabled: true
  model: qwen3.6-35b-a3b
  reportAt: "07:00"
  tick: 10m
  contextWindow: 131072           # the model's window in tokens (#285); unset = llama-server's /props; budgets grow with it above 32k
peers:                            # the other boxes of the household
  kraftwerk: {lanIp: 192.168.1.20, laib: http://192.168.1.20:5900}
persona: joshua                   # the house persona (ADR 0020): wake word, voice, Haus, chat default; joshua when unset
chatPersonas:                     # a Signal number (E.164) that talks to another persona in the chat
  "+4917012345678": hal
people:                           # the household's people and their chat channels, preferred first (ADR 0022 §3)
  mama: ["signal:+4917012345678", "telegram:123456789"]
  lena: ["matrix:!AbCdEf:example.org"]   # a room: the direct chat with the Bote
```

Builtins for units and hooks: `${MODEL_URL}`, `${MODEL_KEY}`,
`${MODEL_LEASE}` resolve from the model role when present, else from
`model`. Units and hooks both read them inside a container, so the
model role's `127.0.0.1` becomes `host.containers.internal` (ADR 0007,
#198, #200), and both get the same `${MODEL_KEY}`. `${PERSONA}`, `${PERSONA_DISPLAY}`, `${PERSONA_PHRASE}`,
`${PERSONA_WAKEWORD}` (the model name, `hey_joshua`), `${PERSONA_VOICE}`,
`${PERSONA_LANGUAGES}` (`de,en`) and
`${PERSONA_REV}` come from the house persona. A unit that carries
`PERSONA_REV` is restarted on any persona change.

`people` names each person once (lowercase, like a service name) with the
channels a message to them goes out on, in order, as `hermes send` takes
them: `signal:`, `sms:` and `whatsapp:` with an E.164 number, `telegram:`
with a numeric chat id, `matrix:` with a room id `!room:server` (the
direct chat with the Bote; Hermes posts into a room, so a user id
`@user:server` is refused, #189),
`discord:` and `slack:` with a channel id, `email:` with an address that has a domain (Hermes' own target shapes; no Matrix thread suffix). A unit
that carries `${PEOPLE_REV}` (the Bote's `bote-notify`) gets
`/mnt/data/laib/render/people/people.json` (name → channels, mode 0600) to mount
read-only and is restarted when a person or a channel changes. A message is
addressed to a person by name, never to a free target. When a service
declares `channels` (below), every target must be on a platform one of them
has set; a `matrix:` target on a box whose Bote has no Matrix fails plan,
naming the value that would switch it on (#184).

## personas/<name>/ (optional)

A persona package (ADR 0020): `persona.yml` (name, display, languages,
wakeword.phrase, voice, prompt, greeting, colors, gimmicks), `style.md` (how
the character talks; style only, the rules are code), `icon.svg`, and
`icon.png`, the same icon as a square PNG of at least 192 px for phones, which
draw no SVG (`scripts/persona-icon.sh` makes it from the SVG). Laib
looks in the state repo first, then `registries/<reg>/personas/<name>`, then
the built-ins (`personas/` in this repo: joshua). When a service uses a
persona value, apply writes every persona in use and `chat.yml` (house and
chat map) to `/mnt/data/laib/render/persona/`, which services mount read-only;
`persona.json` next to each package holds the resolved fields for readers
without YAML (Haus).
A style.md that talks about approvals or rules is a plan warning.

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
skills:                         # the Operator's skills (ADR 0023): absent = active
  routineblick: paused          # active | paused; laib skill enable|pause <name>
```

A hand never approves its own proposal. `destroy` requires a human on the box
page or a Solaris card; a token cannot approve.

## skills/<name>.yml (optional)

An Operator skill (ADR 0023): `description` (German, one sentence),
`trigger` (one of `every: 10m`, `daily: "07:00"`, `weekly: "Mo 08:00"`,
`event: red`; `${TICK}` and `${REPORT_AT}` are `box.yml operator.tick` and
`reportAt`), `tools` (kernel verbs from `status diag logs inspect look read
journal context inbox plan note escalate propose wish restart exec write
apply updates backup`; `note` required), `more` (tools only a capable model gets, checked
the same way, #286), `context` (`box` or `usage`), `output` (`note`,
`report`, `propose`) and `prompt`. The mechanics are fields the Operator's
runner enforces, never prompt prose (#376): `limits` (a cap per tool and
session, `{propose: 3}`) and, for an updates skill, `updates: patch` (the
runner reads `updates` itself, shows and allows only the patch values, and
writes the minor versions and the registry errors into the note). The
prompt keeps the judgment. A file with a built-in's name
(`routineblick`, `morgenbericht`, `vorfall`, `nutzung`, `aktualisierung`, `sicherung`) replaces it. The
kernel grants only the tools the ops ceiling reaches; a file it cannot
accept is listed as `verweigert` with the reason and never runs. Adding or
changing one is class `human` in the review.

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
  tokenEndpointAuthMethod: client_secret_post   # only if the app sends its secret in the form body; default client_secret_basic
  # every client is one_factor, as ServiceBay registered the family's apps (#251)
data:
  - path: ${DATA_DIR}/paperless/data    # backed up
  - path: ${DATA_DIR}/paperless/media
    backup: false                       # bulk, skipped
health:
  http: http://127.0.0.1:8000/api/      # verify after apply
status:                                # what the service says about itself beyond up/down (#309), optional
  file: ${DATA_DIR}/paperless/laib-status.json   # {"at", "detail", "findings": [{severity, detail, fix}]}
  check: paperless-check.service        # optional: a oneshot that refreshes it
  stale: 5m                             # older than this, a look starts the check (default 5m)
ports:
  - { port: 8000, lan: false }          # nftables drop on physical interfaces
  - { port: 8025, lan: false, when: [MAIL_HOST] }   # only while MAIL_HOST is set (#190)
gpu: false
provides: [documents]            # purposes; a second service for one purpose is a HINWEIS in plan and Inbox
after: [authelia]                # restarted and verified before this one
when:                            # a unit exists only while one of its values is set (#184)
  paperless-mail.container: [MAIL_HOST]
requires:                        # a set value needs these set too (#184)
  MAIL_HOST: [MAIL_PASSWORD]
channels: {signal: SIGNAL_ACCOUNT}   # people platforms this service delivers on, each while its value is set
```

`status` is a file the template keeps about itself. laib reads it fresh on
every `laib status`, `/ready` and `laib diag` (a file read, no watcher) and
shows its `detail` on the Box page and its `findings` in diag, each with its
age ("geprüft vor 12 min"). Until the file exists the line says "noch kein
Stand". A service that knows when its state changes
writes the file then (Stimme's trainer). One that cannot (Home Assistant
does not push its config) names a `check`: a `.service` of the template
without `[Install]`. laib starts it with `--no-block` when someone looks at
diag or the Box page and the file is older than `stale`. The page shows the
last state and reloads until the new one lands, and offers "jetzt prüfen".
A slow timer of the template keeps it fresh for the morning report. The
kernel names no template.

`when` names a unit file and the values that switch it on. While none of
them is set, the unit is gone from the load: not rendered, not verified,
removed from the box like any unit the state no longer has, and its own
placeholders need no value. `laib plan` prints each such unit as `AUS:`
with the value it waits for. Errors: a name that is no unit of the
service, a value `values.yml` does not have, and turning off a unit that a
remaining one joins or requires (`Pod=`, `Requires=`, `BindsTo=`).

A port takes `when` too. While none of its values is set, the port is
neither declared (no nftables line) nor published:
every `PublishPort=` line of the service on that host port is dropped, so a
pod need not publish a port for a container that is off.

A value is **set** when it is a non-empty literal. `""`, blanks, `~` and
`null` are unset, and load (and render) as empty. `false` is text and
counts as set; write `""` to switch something off. A `!secret` never counts
as set, because a plain load does not know secrets.

`requires` maps a value to the values it needs once it is set. A literal
one that stays empty fails the load; a `!secret` one fails plan and apply,
which know secrets, before anything is written. Both sides must be values
of the service.

`channels` maps a `people` platform to the value that switches it on (the
Bote: `signal`, `matrix`, `telegram`). A service that declares channels
needs at least one of them set.

This file is what used to be `variables.json` + the subdomain, secret, port
and backup conventions spread over the ServiceBay codebase. It is the
irreducible part of a template: an agent writing Quadlet files from an upstream
compose file does not know on its own that port 8000 must never reach the WLAN.

## services/<name>/post-deploy (optional)

An executable. Runs after `laib apply` verified the service green, in the
service folder, with `LAIB_SERVICE`, `LAIB_DOMAIN`, `LAIB_DATA_DIR`,
`LAIB_TZ`, `LAIB_LAN_IP`, `LAIB_START_URL` (where a login without a target
lands: Haus once installed, else the box page, #381), every `values.yml` key (secrets resolved) and
`LAIB_SECRET_<NAME>` for every declared secret in its environment. Ten-minute
timeout. Its output is journaled with secrets redacted; a non-zero exit turns
the apply red. A line `laib-secret: NAME=value` on stdout stores a credential
the hook created (an API token, a generated admin password) into
`secrets.age`, so it is never printed again and survives a reinstall. The
name may be another service's (Home Assistant mints Haus's and the Bote's
tokens, #349): every service that declares a handed-back secret is planned
again in the same apply and applied when its rendered output changed, so it
starts with the new value; a consumer that reads the secret only in a hook
should also render it into a unit, or it waits for its next change.

This is where ServiceBay's `post-deploy.py` work lives on: first-run
provisioning that only the app can do (Jellyfin's setup wizard, Immich's
admin sign-up, Home Assistant onboarding, AdGuard's admin user, LLDAP
seeding, a Mosquitto integration in Home Assistant). What those scripts also
did, Laib renders instead: proxy hosts, OIDC clients, Authelia rules, LAN
blocks, credential surfacing. A hook must be idempotent: it runs on every
apply that touches the service.

## values.yml

Plain `KEY: value`. Secrets are referenced, never written here:
`PAPERLESS_SECRET_KEY: !secret PAPERLESS_SECRET_KEY`. A value is text or
`!secret NAME`, nothing else: a mapping (`{secret: NAME}`), a list, a
`!secret` without a name or another `!tag` fails the load with the file,
line and key.

`laib add` writes values.yml from the template's `values.example.yml`. A
template update keeps it and appends each key the new example has and
values.yml lacks, as the example's line writes it (#215); a value already
there is never changed. So a template that brings a new placeholder never
leaves an install with `${KEY} has no value`. A new key must be one line.

Without secrets (`laib template lint`, a review) a secret renders as
`${secret:NAME}`. Plan and apply render with the real values, and journal
and diffs show each one redacted back to that placeholder.

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

### Routes that trust only the proxy

`proxySecret: <NAME>` on a route makes Caddy send `X-<Service>-Proxy:
<value>` (name overridable with `proxyHeader`) to the service's
*authenticated* handler only; the bypass paths get nothing. The service
trusts `Remote-User` only when the header matches (ADR 0012 §3). The
secret is one of the service's own declared secrets; the loader refuses a
route naming one the service does not declare.

### Names that only redirect

`redirectFrom: ["@", www]` on a route makes Caddy answer those names with a
redirect to the route's host, `{uri}` kept; `@` is the domain itself. No
service and no Authelia behind them: the old entry points `dopp.cloud` and
`www.` lead to `haus.` (#360).

Caddy strips every client-sent `Remote-*` header on every site, ordered
before forward-auth (`order request_header first`), so the only
`Remote-User` an app ever sees is Authelia's. A route with `auth: none`
gets none at all, and its app must not trust one (#231,
`internal/render/auth231_test.go`).

### Secrets the data was made with (#353)

A contract secret with `dataBound: true` is read by its app only when the app
creates its data: a database's password (Postgres at initdb), LLDAP's admin,
Authelia's storage key, an app's first administrator. `laib secret rotate`
refuses it and changes nothing, because a new value would lock the app out of
its own data. To change one: change it inside the app first (its admin page,
`ALTER ROLE` for a database), then `laib secret set <NAME>` with that same
value and `laib apply`. A secret a hook sets inside the app on every apply
(the LDAP read-only accounts, MQTT, Samba, HA's bote) is not data-bound and
rotates.

### Secrets that may stay unset

A contract secret with `external: true` must be in `secrets.age` before
`laib add`/`approve` renders the service; with `optional: true` as well it
may be missing. A missing one is an empty value everywhere: the unit keeps
its line and gets `Environment=X=` (`${KEY}` inside a longer value becomes
nothing), and a hook gets `LAIB_SECRET_<NAME>=` and the empty `KEY`. It is
never the placeholder text. A consumer reads empty as absent. Caddy's
`DNS_API_TOKEN` is the case: needed only when `dns.provider` is set.

### Secrets reach containers as podman secrets (#224)

Quadlet turns every `Environment=` line into `podman run --env`, and `ps`
shows that argv to every user on the box. So an `Environment=` line of a
`.container` that names a secret-backed value (a `!secret`, or the builtin
`MODEL_KEY`) renders as `Secret=laib.<service>.<container>.<KEY>,type=env,target=<KEY>`;
the container's environment is the same. Plan compares each such podman
secret with the box (`podman secret inspect --showsecret`): a missing one
or another value is a change of its service, apply hands the value to
`podman secret create` (`--replace` for a new value) on stdin before the units start and
restarts the service; a `laib.*` secret nothing names any more is removed
after its unit stopped. An apply cut off in between is finished by the next
plan (the value still differs) or the pending marker. An empty value stays
`Environment=KEY=`: podman refuses an empty secret. Host units (`.service`)
keep `Environment=`: systemd sets it without an argv, in a 0600 file.
A key built from a value (`ACCOUNT_${SHARE_USER}`) can hold what podman
refuses in a name, or a dot that would make two lines one name: such a line
gets `laib.<service>.<hash of container and key>` instead. A secret line
holds one assignment, read as systemd reads it (blanks around `=`, quotes
dropped); two assignments, or a key with a comma, quote or blank (podman
cannot target it), fail the render. With no secret values at all (lint, a
missing `secrets.age`) plan compares no podman secrets, so it never removes
their last copy.

### Backup target by default

`laib init` writes `backup.repo` as a restic repo on the data disk
(`/mnt/data/backup/restic`, 03:00). It exists from day one, so the operator
snapshots before repairs and `laib restore` has something to restore, and it
protects against mistakes and bad updates — not against the disk. `laib
diag` says so until a NAS or second repo (or a mirror, `laib disk add`) is
there. The copy off the disk is `backup.offsite` (docs/BACKUP.md, "The
off-box copy").

### Secrets a template mints

`mint: <hand>` on an external secret makes `laib add` mint a Laib token for
that hand (not human) and store it, once. See INTERFACE.md.

### `tls`

`tls: internal` makes Caddy sign every host with its own CA (`local_certs`)
instead of Let's Encrypt. For a trial box whose domain the house's port 80
does not reach (the NUC behind the Kraftwerk) or a domain whose DNS has no
API (Strato). Trust `data/caddy/pki/authorities/local/root.crt` on the
laptop and the phones. Empty = Let's Encrypt: HTTP-01 when `dns.provider`
is empty (port 80 must reach the box, as today), DNS-01 otherwise.

### `dns`: one wildcard certificate (#378)

With `dns.provider` (and no `tls: internal`) Caddy gets **one** certificate
for `*.<domain>` by DNS-01 and none per name: a new route has a valid
certificate at once, needs no public DNS record (the LAN finds it through
AdGuard's `*.<domain>` rewrite), and its name does not appear in the public
certificate-transparency logs. A name with no route is closed (`abort`).
The bare domain gets its own certificate only when a route uses it
(`redirectFrom: ["@"]`); Caddy keeps one certificate per name, so apex and
wildcard are two. A two-level name (`a.b.<domain>`) is not under the
wildcard and gets its own certificate.

`challengeAlias` is for a domain whose DNS has no API (Strato): there,
`_acme-challenge.<domain>` is a CNAME to a name in a zone at the provider,
and Caddy writes the TXT record at that name
(`dns_challenge_override_domain`). Caddy asks public resolvers
(`9.9.9.9`, `1.1.1.1`), not the box's AdGuard. `laib diag` checks the CNAME
through the same resolvers: red when it points elsewhere, yellow when it
does not resolve. The owner's steps: docs/INSTALL.md, "One wildcard
certificate".
