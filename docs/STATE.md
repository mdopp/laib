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
    pre-start, post-deploy   hooks (from the template)
    .template/        the template's files as the last add or update copied them in (#511)
    rendered/         written only by laib: the files as they are on the box
skills/<name>/skill.yml  an Operator skill added or replaced on this box (ADR 0023, #839)
wishes/<time>.md      an Operator's wish, added by its proposal; approving it files the intake issue (#270)
lessons/<name>.md     a lesson from a solved incident, proposed by the Operator, let in by a human; laib context carries it (#544)
ignore.yml            units on the box that are deliberately unmanaged
aufgaben.yml          the Box-Aufgaben (#667): the owner's to-dos on the box; laib aufgabe commits it on main, never a proposal
```

The journal is *not* in the repo: `/mnt/data/laib/journal/YYYY-MM-DD.jsonl`.

## box.yml

```yaml
domain: example.home
timezone: Europe/Berlin
zuhause:                          # the household's address (#561), all optional; templates read ZUHAUSE_*
  street: Musterweg 12
  postcode: "12345"
  city: Musterstadt
  country: DE                     # ISO 3166-1, two letters
  latitude: 52.1234               # degrees; latitude and longitude together or not at all
  longitude: 8.0456
  elevation: 63                   # whole metres, optional
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
  owner: llama                    # the service that holds the lease (the model role, ADR 0011)
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
  person: martin                  # the owner in people (#441): laib-notify tells them through the Bote first (incidents,
                                  # updates, each new proposal); mail only when no channel is reachable
updates:
  days: [Sat, Sun]                # laib-images.timer's nights (#528); empty = every night
  time: "04:30"                   # default: 90 minutes after backup.schedule
  laib: auto                      # Laib's own update in the same night, before the images (#547): auto, off, or v0.16.0 to hold
model:                            # a box without a model role (ADR 0018)
  url: http://192.168.1.20:11435/v1
  keySecret: MODEL_API_KEY        # only for a cloud API
  lease: http://192.168.1.20:11435/api/model-lease
intake:                           # wishes and escalations become tickets (#56); laib-haushalt files them with GITHUB_INTAKE_TOKEN from secrets.age (#797);
                                  # the Bote's escalations never: they are Box-Aufgaben for the owner (#863)
  repo: mdopp/laib
  label: wunsch                   # a person triages; pi:ready is set by hand
operator:                         # laib-operator's container (ADR 0013, 0027); laib operator enable sets this
  enabled: true
  model: qwen3.6-35b-a3b
  reportAt: "07:00"
  tick: 10m
  contextWindow: 131072           # the model's window in tokens (#285); unset = llama-server's /props; budgets grow with it above 32k, a look's stays its own (#550)
peers:                            # the other boxes of the household
  kraftwerk: {lanIp: 192.168.1.20, laib: http://192.168.1.20:5900}
persona: joshua                   # the house persona (ADR 0020): wake word, voice, Haus, chat default; joshua when unset
chatPersonas:                     # a Signal number (E.164) that talks to another persona in the chat
  "+4917012345678": hal
people:                           # the household's people and their chat channels, preferred first (ADR 0022 §3)
  mama: ["signal:+4917012345678", "telegram:123456789"]
  lena: ["matrix:!AbCdEf:example.org"]   # a room: the direct chat with the Bote
```

Builtins for units: `${LAIB_PAGE_URL}` is the box page,
`https://<laib.host>.<domain>/` (never a hard-coded `laib.<domain>`, #381).
Builtins for units and hooks: `${MODEL_URL}`, `${MODEL_KEY}`,
`${MODEL_LEASE}` resolve from the model role when present, else from
`model`. Units and hooks both read them inside a container, so the
model role's `127.0.0.1` becomes `host.containers.internal` (ADR 0007,
#198, #200), and both get the same `${MODEL_KEY}`. `${PERSONA}`, `${PERSONA_DISPLAY}`, `${PERSONA_PHRASE}`,
`${PERSONA_WAKEWORD}` (the model name, `hey_joshua`), `${PERSONA_VOICE}`,
`${PERSONA_LANGUAGES}` (`de,en`) and
`${PERSONA_REV}` come from the house persona. A unit that carries
`PERSONA_REV` is restarted on any persona change. `${NOTIFY_PERSON}` is
`notify.person`, whom a service tells when its values name nobody (the
scan service's closing message, #568).

`zuhause` is the household's address, once for every template that needs
it (#561): Home Assistant's home, a solar forecast, a waste calendar. The
loader checks it: every field one line, the postcode loose (letters, digits,
blanks, dashes, 2 to 10), the country two capitals, latitude (-90…90) and
longitude (-180…180) together, the elevation whole metres (-500…9000).
Units get `${ZUHAUSE_STREET}`, `${ZUHAUSE_POSTCODE}`, `${ZUHAUSE_CITY}`,
`${ZUHAUSE_COUNTRY}`, `${ZUHAUSE_LATITUDE}`, `${ZUHAUSE_LONGITUDE}`,
`${ZUHAUSE_ELEVATION}` (empty where unset) and `${ZUHAUSE_REV}`, which
changes with any of them: a unit that carries it is applied again on a
move, and its post-deploy runs. Hooks get the same `ZUHAUSE_*` variables,
but only for a service whose units name one; no other service sees the
address. Coordinates are entered, never looked up: Laib makes no call to a
geocoding service. An address without coordinates while a service reads
them is a plan warning, and that service leaves its location alone.
`laib zuhause set` (Haus's form calls `laib_zuhause`) changes the block as a
proposal of class human ("ändert die Adresse des Haushalts"); the owner
approves it, the caller never (docs/INTERFACE.md).

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

A person's Signal number gets into `people` by a proposal, never by hand
(#596): `laib person invite … --signal +49…` (Haus's „Person einladen“) or
`laib person signal <user> +49…` (the person page; the Bote, when the owner
answers its notice about a dropped sender with who it was) sets that
person's `signal:` channel, in place or after their others, and adds the
person when they are not there yet. The review marks a `people` change class
human: who may write to the Bote is the owner's call. Once applied, the
Bote's pre-start lets the number talk (#586).

## host/image (optional, #368)

The host OS image, in two lines: `# tag: <tag>`, the tag it follows, then the
rebase reference by digest. For example:

```
# tag: stable-nvidia
ostree-unverified-registry:ghcr.io/ublue-os/ucore-minimal@sha256:a287759b…
```

`laib updates` reads it, and the Operator's weekly run proposes a newer digest
as a patch to this file. A change here is a human's call (the review).
`laib-host-image.timer` on the box stages the pinned image, and the reboot
goes through laib's reboot lock (ADR 0024). On a uCore box it is the one OS
update path; `install-box.sh` writes the file from the booted deployment when
it is missing and the box was rebased by tag (#486).

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

`laib init` writes `state.DefaultPolicy`; a box from before a release gets
the built-in hands' new verbs and path grants as one proposal
(docs/INTERFACE.md, #575, #598). An excerpt:

```yaml
tiers: [read, propose, apply, destroy]   # destroy covers reboot and exec
hands:
  resident: { ceiling: read }
  ops:      { ceiling: apply }  # the operator (ADR 0013)
  laib:     { ceiling: propose, verbs: [backup, diag] }  # laib's own timers (#444): the nightly backup, its check, the snapshot before an image update; supplied when missing
  notify:   { ceiling: propose, verbs: [diag, updates, inbox, note] }  # laib-notify (#443, #441): the watchdog ping and the owner's messages; reads, journals a failed send; supplied when missing
  haushalt: { ceiling: propose, verbs: [propose] }  # laib-haushalt's call back from behind the verb door (ADR 0026, 0027): its proposals are never the dial's; supplied when missing
  lease:    { ceiling: apply, verbs: [status, pause, resume, note] }  # laib-lease in its container (ADR 0027): pauses and resumes what a GPU window stops, notes a long one; supplied when missing
  pi:       { ceiling: propose }
  haus:     { ceiling: apply, verbs: [status, inbox, zuhause, person_signal, …], paths: [zuhause, people.*.signal] }  # the family's pages
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

A hand's `paths` (#598) name where in box.yml it may propose a value
(`laib propose --path`, and the verbs built on it: `laib person invite
--signal` at `people.<user>.signal`; `laib zuhause` and `laib person signal`
are served by laib-haushalt behind the door since #677 and propose as the
built-in `haushalt` hand, which holds both paths in code). A pattern is a
dotted path, `*` stands for exactly one segment, and it names one path, not
the subtree under it; `services/*/values.yml:KEY` names a values.yml key.
Only a hand with a verb list is held to its paths; a hand without one and
a ceiling of at least `propose` (claude, pi, the Bote) may propose a value
at any path. A path grant is a policy.yml change, so the owner decides it,
like a verb grant; what the proposal changes is still reviewed and
classed as any other (a box.yml change waits for a human).

A hand never approves its own proposal. What only a human may approve —
`destroy`, a `human` or `remove` class, a tier with `approvals … by: human` —
waits for the Inbox or the host shell; an agent hand whose ceiling holds
`approve` may approve the rest (ADR 0019 (e), #865).

## skills/<name>/skill.yml (optional)

An Operator skill (ADR 0023) is a folder (#839): `skill.yml`, and for a
built-in skill, where it helps, `run.py`. A state repo's folder has no
script: only the Operator's image brings one, and a folder with a `run.py`
is refused, as is a file the old way, `skills/<name>.yml`, whose reason
names the new place. `skill.yml` has `description` (German, one sentence),
`trigger` (one of `every: 10m`, `daily: "07:00"`, `weekly: "Mo 08:00"`,
`event: red`; `${TICK}` and `${REPORT_AT}` are `box.yml operator.tick` and
`reportAt`), `tools` (kernel verbs from `status diag logs inspect look read
journal context inbox plan note escalate propose wish restart exec write
apply updates backup`; `note` required), `more` (tools only a capable model gets, checked
the same way, #286), `context` (`box`: the `laib context` bundle; `usage`:
usage-metrics' answers, read by the runner, handed to the script; `none`:
only what the script prepared), `output` (`note`, `report`, `propose`),
`prompt`, and two fields of #839: `run: run.py` (the skill's script, built-in
only) and `modell`, where the model decides — `frei` (the default: the
agentic loop with every granted tool, as before scripts; the incident and
the looks), `urteil` (one judgement on what the script prepared, offered
only the tools that write it: `note`, `wish`, `propose`, `repair`,
`escalate`) or `nein` (the script alone; it needs `run`). The owner, on
#839: „ich möchte, dass wir die Problemlösungskompetenzen eines LLMs
behalten“ — a script gathers, counts, compares, renders and guards; what is
open-ended stays the model's.

A script (`skills/<name>/run.py`, Python standard library only) defines
`run(s)` and, when its result names `checks`, `check(s, tool, args)`. It
reaches the box only through `s.call("laib_<verb>", **args)`: the runner
makes each call in the skill's name, refuses a tool the skill is not
granted and one over its `limits` (the script's count apart from the
model's), and records every call (`script`) and every refusal (`refused`)
in the session. It returns `laibskill.done(...)`: an `outcome` ends the
session without a model; otherwise `context` is what the model sees,
`human` is added to the note, `state` is kept for the next run unless the
session failed or waited, and `checks` names the tools whose model calls
`check` vets. `skills/laibskill.py` is its one door. The mechanics are fields the Operator's
runner and the skill's script enforce, never prompt prose (#376): `limits` (a cap per tool and
session, `{propose: 3}`) and, for an updates skill, `updates: patch` (its
proposals answer an update, not a finding, #544), and `lessons: vorfall`
(the runner hands the script that skill's sessions, from which it proposes
a lesson for each incident whose proposal laib measured as effective). An
interval skill granted `diag` is a look (#543): the runner reads diag
first, asks no model while the findings are the ones its last good session
saw, and, granted `repair`, proposes each non-red finding's repair once
until it changes. The prompt keeps the judgment. A file with a built-in's name
(`routineblick`, `morgenbericht`, `vorfall`, `nutzung`, `aktualisierung`, `sicherung`, `lehre`, `durchsicht`) replaces it. The
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
  file: ${DATA_DIR}/paperless/laib-status.json   # {"at", "detail", "findings": [{severity, detail, fix}]}; further fields reach laib_status <service> as eigenes (#688)
  check: paperless-check.service        # optional: a oneshot that refreshes it
  stale: 5m                             # older than this, a look starts the check (default 5m)
ports:
  - { port: 8000, lan: false }          # nftables drop on physical interfaces
  - { port: 8025, lan: false, when: [MAIL_HOST] }   # only while MAIL_HOST is set (#190)
gpu: false
provides: [documents]            # purposes; a second service for one purpose is a HINWEIS in plan and Inbox
upstream: [https://github.com/paperless-ngx/paperless-ngx, https://docs.paperless-ngx.com/]   # repo, release notes, docs: where the Operator's web search looks first (#544); absent: the template's
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
line and key. A builtin inside a value renders as it does in a unit, for
the unit and for the hooks alike: `RADICALE_PUBLIC_URL:
https://caldav.${DOMAIN}` reaches the container as the box's own name
(#647). Only builtins: a value that names another value stays as written.

`laib add` writes values.yml from the template's `values.example.yml`. A
template update keeps it and appends each key the new example has and
values.yml lacks, as the example's line writes it (#215); a value already
there is never changed. So a template that brings a new placeholder never
leaves an install with `${KEY} has no value`. A new key must be one line.

The other files a template update brings are merged three ways (#511):
the file on the box, the template as it was copied in last
(`services/<name>/.template/`) and the new template, line by line
(`git merge-file`). A line the owner changed keeps the owner's value. Where
the owner and the template changed the same lines, or a service from before
#511 has no `.template/` yet, the template's file is taken and the
proposal carries a warning that names the files, so nothing auto-approves
it and the diff shows what an approve would undo. A proposal that would
only record `.template/` is not made.

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
`"verb":"outcome"` (actor `laib`, #544) is what came of an approved
proposal that named a finding: `#N von ops wirksam: der Befund ist weg
(unit jellyfin)` or `… ohne Wirkung: der Befund ist noch da (…)`, once per
proposal; a `reject` line carries the owner's reason after the colon.
An `approve` line is the merge (`approve #N by …` on `main`); the apply
that carries it comes after, as one run for whatever was approved
meanwhile, with reason `Freigaben #4, #5, #6 (<from>..<to>)` — the
proposals and the state's commit range, also kept on each inbox entry as
`batch`. A red run's `rollback` lines name it per proposal (#609).

### Routes that trust only the proxy

`proxySecret: <NAME>` on a route makes Caddy send `X-<Service>-Proxy:
<value>` (name overridable with `proxyHeader`) to the service's
*authenticated* handler only; the bypass paths get nothing. The service
trusts `Remote-User` only when the header matches (ADR 0012 §3). The
secret is one of the service's own declared secrets; the loader refuses a
route naming one the service does not declare.

### The app's own login

`loginProbe: <path>` on an `auth: none` route names a path the app
refuses without a session (401 or 403). Laib renders nothing from it;
`laib net` carries it, and `laib-app check` measures the app's own login
there instead of Authelia's (#774).

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

Every route with `auth` other than `none` sits behind Authelia, the sso
service's own routes too: `ldap.` (LLDAP's admin UI, `two_factor`, admins)
asks Authelia's two factors after the LAN check, then LLDAP's own login
(owner, 2026-10-26). The one exception is the route that is Authelia's
portal (the sso service's first route, `auth.`): it never forward-auths, as
a portal behind itself is a loop. LLDAP's LDAP port 3890 is no route and
not behind Caddy (`internal/render/ldap2fa_test.go`).

### Secrets that are a human's login (#441)

`login: {user, host, path?, note?}` on a contract secret marks it as a login
a person uses. `user` is a literal or `${KEY}` of the service's values.yml,
and `host` is a route host (the address becomes `https://<host>.<domain><path>`).
`laib recovery-sheet` lists these logins, and `--vaultwarden <file>` exports
them with their passwords for an import into Vaultwarden (docs/BACKUP.md).

`owner: {label, warum, hilfe, link?}` (#696) on an `external` secret that
no hook mints marks a key the owner brings from outside (Discogs' app key,
the DNS provider's token, a mailbox password). Only these appear on the box
page's „Schlüssel hinterlegen“, in the template's German words: what it is
(`label`), what it brings the household (`warum`), where to get it
(`hilfe`, `link`; `${DOMAIN}` in the link is the box's domain). The loader
refuses `owner:` on a generated or minted secret, or without those words.

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

With `rotate: true` beside `dataBound: true` (#665) the template's hook can
change it inside the app, and `laib secret rotate` does: the new value waits
in `secrets.age` as `<NAME>.next`, the services that declare it apply, and
their hook gets it as `LAIB_ROTATE_<NAME>` next to the current
`LAIB_SECRET_<NAME>`. The hook changes it in the app (an admin API with the
current one, `ALTER ROLE` through `laib-exec` from a 0600 file, never argv)
and prints `laib-secret: <NAME>=<new>` once the app took it; a hand-back
after a failed `laib-exec` of the same hook is not taken. Without the
hand-back the old value stays and `<NAME>.next` goes; with it, a red apply
rotates back once, the same way. A hook that finds the new value working
already only hands it back, so a run after a dropped session finishes it.

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
Laib's own containers (ADR 0027) get theirs the same way, named
`laib.laib-operator.laib-operator.LAIB_TOKEN` and so on; an empty one is
left out of the unit. `laib-haushalt`'s are `LAIB_HAUSHALT_TOKEN` (minted
on apply for the haushalt hand) and `LAIB_HAUSHALT_KEY` (generated: the
verb door's bearer) in `secrets.age`; the lease's laib token is the llama
template's `LLAMA_LAIB_TOKEN`, minted for the lease hand.
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
certificate-transparency logs. A name with no route proxies nowhere and answers
a German 404 page with the way to the start page (#480).
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

## aufgaben.yml

The Box-Aufgaben (#667): what the owner has to do on or for the box that no
code change closes (a second disk, a NAS share, a device to flash). Owner,
2026-10-25: "das ist ja ein Thema der Box, nicht des Codes". They were
GitHub issues and rotted there.

```yaml
aufgaben:
  - id: 2
    titel: Wächter von außen einrichten
    warum: fällt die Box aus, erfährt es niemand
    seit: "2026-10-25"
    von: claude
    erinnern: "2026-12-01"        # from this day diag reminds again
    befund: watchdog              # the diag finding it covers: probe [service|topic]
  - id: 1
    titel: TOTP für mdopp prüfen
    seit: "2026-10-25"
    von: human:core
    erledigt: {am: "2026-10-26", von: haus:mdopp}
```

- **Why the state repo, committed directly.** The list is the box's, so it
  is backed up and versioned with it. A change is bookkeeping, not desired
  state: it renders nothing and changes no service, so it needs no review
  and no Inbox. `laib aufgabe` writes the file and commits it alone on main
  (`proposal.CommitPath`, the way `secrets.age` is committed, #328), and a
  bare `laib rollback` skips such commits: it reverts the box's last change,
  not the ticking-off on top of it.
- **Why the kernel, not laib-haushalt.** ADR 0026 puts household verbs
  behind the verb door, but laib-haushalt's container mounts only the scan
  orders (ADR 0027), never the state repo, and diag, which is the kernel,
  reads the list on every run. A door program that only proposes could not
  tick a task off without an approval.
- **diag.** A finding that an open task's `befund` covers is shown at
  severity info with „zurückgestellt → Box-Aufgabe #N“ and carries
  `aufgabe: N`; from its `erinnern` day it is gelb with „erinnert“. A red
  stays red: a deferral silences a nudge, never damage. A finding that could
  not be measured (`unmeasured`, e.g. laib-disk silent) is never covered.
  When a full diag measures the finding gone, laib checks the task off
  („von Laib erledigt: Befund weg“), but only for the probes whose absence
  is a measurement: watchdog, backup, recovery, restore-test, raid. Every
  other task is ticked off by a person.
- `befund` matches a finding by its probe and its service or topic
  (`backup offsite`); a befund without one covers only findings without one.
- Readiness carries `aufgaben` and `aufgabenFaellig`; the morning report
  names the due ones.
