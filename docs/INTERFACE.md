# Laib — the interface

Laib has exactly one interface, the CLI. The MCP surface, the three pages and
the Solaris cards are views on it.

## CLI

```
laib status [service] [--watch]      one line per service; first line is the readiness state
laib logs <service> [--since 1h]     journald of every unit, the containers' output in it (podman log driver journald), secrets redacted
laib diag [service]                  findings with severity and a proposed diff each
laib inspect <service>               units, containers, data files, health, last logs (class 1)
laib read <service> <path>           a file in the service's data paths, secrets redacted (class 1)
laib exec <service> [--container c] -- <cmd>   inside the container, output journaled (envelope)
laib write <service> <path>          content on stdin, in the data paths (envelope)
laib restart <service>               restart + verify (envelope)
laib escalate <service> "<was ich weiß>"       context bundle + red journal line for the next hand; an issue when box.yml intake is set
laib wish "<was das Haus braucht>"             the intake (#56): a GitHub issue with the wunsch label, never pi:ready; kept under <home>/wishes without an intake; from the ops hand a proposal (wishes/<time>.md, class human) whose approval files the issue (#270)
laib note [--kind report|incident|watch] "<text>"   what a hand concluded; report = the morning report
laib operator enable|disable|status  mint the ops token, render and start laib-operator (humans only)
laib updates                         newer patch and minor releases of every pinned image, from the registry (#287)
laib net [--mermaid]                 every name (reach, Authelia, target), the direct ports, the host network, the pods' links: from the rendered config (#439)
laib skill list | enable <name> | pause <name>   the Operator's skills (ADR 0023); the switch is a human's, a commit to policy.yml
laib plan [service]                  render state → diff against the box (files + units)
laib apply [service] [--proposal id] snapshot → write → reload → restart → verify
laib rollback <service|commit>       git revert + apply
laib propose "<reason>"              human with a shell: commit the working tree to a proposal branch
laib propose --set services/x/values.yml:KEY=VALUE "<reason>"      agent: one value (repeatable)
laib propose --add <template> [--name svc] [KEY=VALUE…] "<reason>"  agent: a new service from a template
laib propose --patch "<reason>"      agent: unified diff on stdin (MCP: the patch property); validated in a worktree
laib approve <id> | laib reject <id> merge or drop a proposal (tier check from policy.yml)
laib backup                          restic snapshot of state, journal and declared data paths
laib restore <snapshot|latest> [svc] pre-restore snapshot, stop, restore paths, apply
laib context [--since 24h]           starter bundle for an agent session
laib template list|lint|add <git>    registries and the contract check
laib adopt <unit> | laib ignore <unit>
laib token mint <hand> | list | revoke <id>   humans only
laib secret set <NAME> | list        value on stdin, never an argument; list prints names only
laib init                            first setup as a dialogue
laib serve                           daemon: /ready, /api, /mcp, the three pages
```

Every verb has `--json`. The MCP tool list is generated from the same verb
table (`mcpVerbs` names which of them an agent may reach at all), and
`scripts/drift.sh` in CI checks that every tool the Operator's prompt
names is one of them. The `ops` hand in Solaris is the operator (ADR 0013): it sees every verb
up to `apply`, including `inspect`, `read`, `exec`, `write`, `restart`,
`escalate` and `propose`. What it never sees: `approve` of its own
proposals, `token`, `secret`, `serve`, `init`. `remove` is above its
ceiling and a reboot is not a verb; a public route is a values change,
which the dial may auto-apply — the "always a human's call" in
`policy.yml`'s comment is a convention today, not enforced (#47).

## Typed MCP tools and the Operator's menu (#53, #290)

The tools agents use most publish typed parameters (`cmd/laib/mcpschema.go`),
and the kernel turns a call's JSON into the verb's argv without joining and
splitting a command line, so quotes and spaces survive:

| Tool | Parameters |
|---|---|
| `laib_status`, `laib_diag` | `service?` |
| `laib_logs` | `service`, `since?` (`60m`, `24h`) |
| `laib_inspect`, `laib_restart` | `service` |
| `laib_read`, `laib_write` | `service`, `path` (`write`: `stdin`) |
| `laib_look`, `laib_exec` | `service`, `container?`, `command` (a list) |
| `laib_escalate` | `service`, `summary` |
| `laib_note` | `kind` (`watch`, `incident`, `report`), `text` |
| `laib_journal`, `laib_context` | `since?` |
| `laib_propose` | `reason`, and `service` + `key` + `value` (one values.yml change) or `patch` |
| `laib_wish` | `text`, `saw?`, `better?` (`wartung`, `familie`, `beides`) |
| `laib_skill` | `action` (`list`, `enable`, `pause`), `name?` |
| `laib_backup` | `action` (`check`: restic checks a sample and restores one path into a scratch dir to compare; `run`) |
| `laib_updates` | none |
| `laib_person_invite` | `user`, `first`, `last?`, `email`, `group?` (`family`, `admins`; empty: family, or on a resend the group the user has), `resend?` (a boolean) → `{user, mailed, link?, expires (RFC 3339), message}` (#360) |
| `laib_person_pending` | none → `[{user, name, email, invited_at}]`: invited, link not used yet |
| `laib_session` | `args`: the session's name → `{token, id, hand, expires}`: only for a hand with `session:` in policy.yml (the Werkstatt, #380) |
| `laib_apply` | `proposal` — for the ops hand only its own open proposal, inside the envelope (#286, ADR 0019 amendment) |

`service` is an enum of the installed services, `key` a values.yml key
(capitals, no path), `value` one line. An unknown parameter, a wrong type,
a value outside an enum or a missing required one is answered `ungültige
Argumente … — nicht ausgeführt` and the verb never runs. Every tool still
takes `args` (one command line) instead, for clients that send it.

The Operator's menu is these tools as the kernel lists them for the
running skill (ADR 0023): no second menu, no translation in the Operator.
The interface follows the model (#286): `OPERATOR_INTERFACE` when set
(`typed` = small, `cli` = capable), else the window — up to 32k tokens a
small model with the short menus and compact budgets, above it a capable
one that also gets each skill's `more:` tools, and whose plain answer is
never mistaken for a call written as text. Above a 32k window the budgets grow with the window —
about a fifth of it for the context bundle, a fifteenth per tool result
(#285); the window comes from llama-server's `/props?model=` (through the
lease proxy) or `box.yml operator.contextWindow`
(`OPERATOR_CONTEXT_WINDOW`). A reasoning model's `<think>` block is
dropped from its reply. The built-in skills' menus:

| Skill | Trigger | Menu |
|---|---|---|
| `routineblick` | every `operator.tick` | `laib_status`, `laib_diag`, `laib_note` |
| `vorfall` | a red | `laib_diag`, `laib_logs`, `laib_inspect`, `laib_restart`, `laib_propose`, `laib_escalate`, `laib_note`; a capable model also `laib_look`, `laib_read`, `laib_journal`, `laib_plan`, `laib_apply` |
| `morgenbericht` | daily at `operator.reportAt` | `laib_status`, `laib_diag`, `laib_journal`, `laib_note` |
| `nutzung` | weekly, Monday 08:00 | `laib_wish`, `laib_note` |
| `aktualisierung` | weekly, Saturday 10:00 | `laib_updates`, `laib_propose`, `laib_note` — a newer patch release per pinned image becomes a proposal; minor releases only in the note (#287) |
| `sicherung` | weekly, Sunday 05:00 | `laib_backup`, `laib_journal`, `laib_diag`, `laib_note` — the canary restore and the week's backups; a red goes into the morning report (#287) |

Reds within a minute (`OPERATOR_EVENT_GATHER`, default 60s) are one
incident session (#287): a cascade — lldap, then Authelia, then Caddy —
reaches `vorfall` once, with every red in its detail, and the prompt asks
for the one cause instead of a restart per symptom.

`nutzung` (#270) starts from usage-metrics instead of `laib context`:
`/summary?days=7` and `/misses?days=7` with `USAGE_METRICS_TOKEN` (the
Operator's unit carries URL and token when the box runs usage-metrics).
A wish from the ops hand is a proposal the admin approves; Operator code
refuses a fourth wish in a session, one whose `saw` has no number, and one
that quotes an unanswered sentence word for word (they are kept 30 days
without names, for admins only; the intake repo may be public). Without
usage-metrics the skill writes one note and asks no model.

A tool is in the menu only if the skill is granted its verb (`ops` at
ceiling `propose` gets no `restart`). `approve`, `reject`, `restore`,
`rollback`, `converge`, `template`, `adopt`, `apply`, `remove` are in no
skill's vocabulary.

### Skills (ADR 0023)

`laib skill list` (MCP `laib_skill`, read tier) lists every skill — the
built-ins in `skills/` of this repo, embedded in `laib`, and the state
repo's `skills/*.yml` — with its status (`aktiv`, `ruht`, `verweigert`),
its granted tools and, per tool it does not get, why. The Operator sends
`X-Laib-Skill: <name>` with every call of a session: `tools/list` then
answers the skill's granted tools only, and a call outside them — or any
call of a paused, refused or unknown skill — is answered `nicht
ausgeführt: …` and never runs. A skill is granted a verb when it names it,
the verb is in the skill vocabulary (`status diag logs inspect look read
journal context inbox note escalate propose wish restart exec write`) and
the hand's ceiling and verb list reach it. `laib skill enable|pause
<name>` (humans only) writes `policy.yml skills` and commits.

## Readiness: `/ready`

`GET http://127.0.0.1:5900/ready` answers one JSON line:

```json
{"state":"bereit","services":12,"green":12,"yellow":0,"red":0,"lastHeard":"2026-10-08T10:42:07Z","lock":null}
```

`state` is one of `bereit`, `eingeschränkt`, `nicht erreichbar` (the last one
is what the *client* renders when the call fails). `lock` names what holds
apply when `eingeschränkt`: `apply:media` or `gpu:werkstatt`.

## The box page: three pages

Status:

```
Laib · bereit · 12 Dienste · zuletzt gehört 10:42:07

  grün   auth, caddy, adguard, media, immich, vaultwarden, ...
  gelb   paperless     Zertifikat läuft in 6 Tagen ab   [diag]
  rot    keine

Letzte Änderung   09:15  claude   media: Jellyfin auf 10.11   #c81f2a
Offene Vorschläge 1                                           [Inbox]
```

Inbox:

```
Vorschlag #17   von ops (Solaris)   Stufe: apply   braucht: Admin

  Paperless für die Familie, nur intern
  + services/paperless/values.yml
  + caddy: paperless.<domain> → :8000, intern
  + authelia: Client paperless, Gruppe family

  Snapshot vor Apply: ja     Rollback: laib rollback paperless

  [Freigeben]   [Ablehnen]   [In der Werkstatt öffnen]
```

Journal: one line per entry, filter by service and actor. Nothing else.

The pages are server-rendered HTML from `laib serve`, behind Caddy and
Authelia (`admins`). Caddy adds `X-Laib-Proxy: <secret>`; without it the pages
answer 403, so a process on the box cannot forge `Remote-User` (ADR 0012).
No JavaScript framework; a form post per button.

## Solaris cards

The admin sees the Inbox as a card that links to `laib.<domain>/inbox`; the
decision happens there, behind Authelia (ADR 0012). Plus two more cards:

- **Ops report** (morning): "Alles grün, ein Zertifikat in 6 Tagen fällig, ich
  habe Vorschlag #18 vorbereitet."
- **Escalation**: "Das verstehe ich nicht. Claude dazuholen? Das Kontextpaket
  liegt bereit." Accepting creates a ticket labelled `needs-claude` with the
  output of `laib context` attached.

Solaris renders the readiness state in its chat header from `/ready` and says
once when the state changes.

## MCP

`laib serve` exposes `/mcp` (streamable HTTP, JSON-RPC 2.0, stdlib). Auth is
a bearer token minted by `laib token mint <hand>`; tokens are hashed in
`/mnt/data/laib/tokens.json`, never in the state repo. `tools/list` returns
only the verbs the hand's ceiling allows, named `laib_<verb>`, with typed
parameters (above) or, for the rest, one `args` string exactly as on the
command line. `serve`, `init`, `token` and
`secret` are never tools. A call above the ceiling answers exit 3 with the
hint to propose. Claude Code: `claude mcp add --transport http laib
http://127.0.0.1:5900/mcp --header "Authorization: Bearer <token>"`.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | done, verify green where applicable |
| 1 | usage or validation error |
| 2 | apply done but verify red (state is applied, journal says so) |
| 3 | refused by policy (tier), proposal created if applicable |
| 4 | locked (`eingeschränkt`) |

### `laib disk`

`laib disk status` shows the data RAID (`/proc/mdstat` parsed, the recorded
member count). `laib disk add /dev/sdX` (humans only) wipes the device,
partitions it and joins it as the mirror of `/dev/md/data`; the sync runs in
the background and `laib diag` shows it. The stick creates the array with one
member on purpose (#29).

### `laib inbox`

Open proposals (`--all` for decided ones), as a table or `--json`; also an
MCP tool (read tier), so the Bote and Haus can show the inbox. A proposal
of class `human` lists why only a human may allow it (the content review,
docs/DESIGN.md). Deciding stays `laib approve` / `laib reject`; an agent's
approve of a human-class proposal is refused with the same reasons.

### Routes with `bypass:`

A route may list path prefixes that skip Authelia on its host, for a
token-only API next to pages: Haus serves `/napi/` to the phone's device
token and `/box` behind Authelia on `haus.<domain>`. Caddy proxies the
bypass paths straight through; Authelia gets a bypass rule per prefix.

### `laib remove <service>`

Humans only. Stops the service's units, takes `services/<name>` out of the
state (a commit: `laib rollback` brings the service back), removes its
Quadlet and rendered files, applies (Caddy, Authelia, DNS lose the routes).
Data paths stay, and Laib prints where; deleting data is a separate, human
act. A role holder (proxy, sso, identity, dns, model) is refused until
`box.yml roles` points elsewhere.

### Tokens a template needs

A contract secret with `mint: <hand>` is a Laib token the service itself
uses (Haus as `ops`, the Bote as `bote`): `laib add` mints it on first
install and stores it in `secrets.age`, so there is no manual `token mint`
plus `secret set`. Human tokens are never minted this way: the admin's own
verdicts stay the admin's (`laib token mint human`).

### Operator heartbeat

The readiness line (`laib status`, `/ready`, the Status page, Haus) carries
the Operator's state: `aus` (not enabled), `startet` (enabled, no note yet),
`läuft`, `stumm` (running but no note for three ticks: model unreachable or a
tick hangs), `antwortet nicht`, `fehlt` (enabled but the unit is down). `laib
diag` turns the last four into findings. An enabled operator that does
nothing is never silent.

### Hands with a verb list, and the approver

`policy.yml` may limit a hand to a few verbs (`verbs: [approve, reject,
inbox, status, note]`) and mark it `human: true`, so its verdicts count as
a human's. `laib init` ships the `approver` hand with exactly that: the
Signal relay's token (#48) — it can decide and nothing else, and it acts
only on the admin's verified number. Over MCP the tool list and every call
honour the verb list.

A hand may name another in `session:` (#380, ADR 0006 amendment). It may
then get a token of that hand per session with `laib_session <name>`: the
TTL is the named hand's (a session hand must have one), the mint is
journaled (verb `session`), `laib token list` shows it as `session
<caller>: <name>`, and expired ones are dropped from `tokens.json` on the
next mint. `laib init` ships `werkstatt: {ceiling: read, verbs: [session],
session: claude}`; the Werkstatt holds that token, never a claude one.

### The decision card (#58)

Every open proposal is shown as the same card — on the Box page, in
`laib inbox`, in the Bote's chat („zeig #17“) and in `laib inbox --json`
(`card`): **what changes** in one sentence („Paperless wird installiert —
unter paperless.<domain> nur im Heimnetz erreichbar“), **what it means for
the family** (the review's reasons: aus dem Internet erreichbar, ohne
Anmeldung, ein Laib-Schlüssel, ein Skript auf der Box …, or „nichts
Sichtbares“), **whether it can be undone** („Zurücknehmen“: files back,
data stays; a new service is stopped and removed, its data stays), **who
proposed it and why** (quoted), and **the safe default**: for a risky
class (human, a new service, a minted token) the first button is „Später“
and the diff sits behind „Details“. The card is built mechanically from
the classified change (`proposal.Card`), never by a model.

### `laib look <service> [--container c] -- <command>`

A read-only command (cat, ls, grep, ps, df, stat, find, …) in one of the
service's own containers: no budget, no snapshot, journaled. `laib exec`
is for changing something and stays budgeted. Neither reaches another
service's container. `policy.yml envelope.snapshot: optional` lets a
class-2 action go ahead without a pre-repair snapshot; the default refuses.

### One lock, pauses, pending activation (#39)

Everything that mutates the box — `apply`, `converge`, `restore`, `remove`,
`approve`, `rollback` — holds the one lock (`<home>/apply.lock`, a flock the
kernel releases when the process dies, with holder, pid and start inside)
for its whole duration. A `converge` tick that finds the lock taken is
skipped and journaled `übersprungen: gesperrt durch restore:human:core`; a
lock whose process is gone is taken over with a journal line.
`laib pause <service> [--until 2h] ["<grund>"]` and `laib resume <service>`
(apply tier): a paused service is left alone by converge and shows as
`pausiert von … bis …` in `/ready` (`paused`), status and the box page;
`restore` pauses the service it rewrites, the GPU lease pauses every service whose units it stopped (`llama`, `stimme`)
while a mode stands. Apply records the services whose activation it owes
(`<home>/apply.pending`, `/ready` `pending`) before it writes files and
clears it after verify; the next apply or converge restarts and verifies
them even when the file diff is clean. Restart-only converge runs are
journaled green with the units and count against the budget of three per
hour.

### Rollback by proposal, stale proposals, the manifest (#41)

`laib rollback #17` takes a freed proposal back: the approve merge is
reverted (`-m 1`) without touching `secrets.age` — services that stay keep
the secrets their data was made with, services that leave the state lose
theirs — and one apply follows. The Box page offers it as „Zurücknehmen“
next to the last five freed proposals. `laib rollback <commit>` validates
the argument (`git rev-parse --verify --end-of-options`) and reverts a
merge commit with `-m 1`. An approve merges in a temporary worktree: a
conflict never reaches `main`, the merged result must load and render, and
a proposal that fails either way is `stale` with a reason („veraltet — neu
vorschlagen“). Apply records every file it put on the box in
`<home>/managed.json`; plan deletes — and for a unit stops and disables —
whatever the manifest has but render no longer produces, so a removed
service, a disabled operator or a removed llama does not keep running.
`laib operator disable` also revokes the ops tokens.

### Roles are verified, the Caddyfile validated, dependants protect a service (#50)

A change to a role's output (the Caddyfile, Authelia's rules, AdGuard's
rewrites) restarts the role's service and verifies it like any changed
service; the rendered Caddyfile runs through `caddy validate` in the
proxy's own image before anything is written, and a Caddyfile that does
not validate refuses the whole apply. `laib remove <service>` refuses while
another service names it in `after:` („… braucht … — erst dort
entfernen“), before anything is stopped. The box's own proposals (the
template-sync timer, actor `laib`) are approved by the box when the dial
says auto; the same change proposed twice reuses the open proposal.

### The shared file-share tree is never relabelled (#230)

`laib template lint` refuses a unit that mounts `${DATA_DIR}/file-share/data`
or a path under it (also through a values entry) with `:z`/`:Z` or without
`SecurityLabelDisable=true`. `:z` makes podman relabel the whole
multi-writer tree on every start; the rootless `lsetxattr` fails on any
entry `core` does not own (a root-owned file from a disk import), and the
container exits 125 in a crash loop (ServiceBay #1731, #1923). The
service's own data folders keep `:z`; with labelling off it is a no-op.

### Templates on CoreOS: ports, health, needs, images (#46)

The state refuses two services publishing the same host port, and
`laib template lint` refuses a template that publishes a port a sibling
template already uses (whoami and the bote pod both had 8080; the bote's
signal-cli is on 8085 now). Every template declares health: an `http:`
probe or `none: "<why>"` (MQTT speaks no HTTP). The probe is green on 2xx/3xx only; an app whose probe
path challenges declares `expect: [401]`, and then only those codes count (#288). A template whose
containers talk to laib's MCP declares `needs: [mcp]`; the state then
insists on `box.yml laib.listen: 0.0.0.0` (ADR 0012 §4) instead of a
container that silently cannot reach laib. Apply pulls the images of new
or re-imaged containers before the units start, so the first start never
includes the pull. Services with a database (immich, paperless,
vaultwarden, Home Assistant, auth, radicale, audiobookshelf, media, beets)
no longer carry `AutoUpdate=registry` — podman's rollback restores an
image, not migrated data; their images change by proposal. Where
auto-update stays, `podman-auto-update.service` takes a `laib backup`
first and the default window is 04:30, never the backup's 03:00.
### Hooks: `pre-start` and `post-deploy`

Both run in the hooks image (ADR 0004 amendment), never on the host:
`pre-start` before the units start, `post-deploy` after a green verify.
Protocol lines on stdout: `laib-secret: NAME=value` (into secrets.age; a
service that declares NAME is re-applied in the same apply when its output
changed, #349) and
`laib-exec: <container> <command…>` (run by Laib in the service's own
container). A `laib-exec` line that carries a secret is refused: ps shows
every argv on the box to every user (#214), so a secret travels on stdin,
in a file only its reader can read, or over the service's own socket.
`box.yml laib.hooksImage` names the image.

### The Operator and the MCP surface (#43)

`diag` is an MCP tool. A verb writes to the writer in its `cliEnv`; MCP
collects a verb's output in a buffer (nothing another goroutine prints
can land in a reply, no pipe can block) and `escalate` builds its bundle
the same way. The Operator's system prompt is generated from the tool
list the hand has; a reply that writes a tool call as text is asked once
for a real call and then ends the session red with a note; a tool call
whose arguments are not a JSON object is refused, never run; the context
bundle is budgeted (head and newest journal lines). `laib operator
enable` points `notify.url` at the operator's event endpoint unless the
owner set one, so reds wake it; an operator enabled for three ticks
without a single note is red („stumm“).

### Hardware health (#59)

`laib diag` asks `smartctl -H -A --json` about every member of the data
array and the OS disk: reallocated or pending sectors, media errors and
wear are yellow („die Platte altert“), a failed self-assessment is red
(„Platte bald tauschen“), 55/65 °C are warm/hot; the kernel's thermal
zones give the box's temperature (80 °C yellow, 90 °C red). Without
smartmontools the probe says so once (`sudo rpm-ostree install
smartmontools`; install-box.sh will layer it with git, restic and diff).
The power-loss proofs (a cut during an apply, a backup, a resync) wait
for Actions minutes and for #81.
