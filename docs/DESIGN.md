# Laib — design

Laib is what ServiceBay becomes when you design it for a local LLM and a remote
Claude session as the operators, with a human who reads, approves and rarely
touches anything by hand. Stability, simplicity and transparency are the
goals; everything below is derived from them.

## Why not evolve ServiceBay

ServiceBay (2026-10) is 91k backend lines, 57k frontend lines, 185 API routes,
23 pages, 64 hand-written MCP tools, 36 diagnose probes, 28 JSON stores, 13
background timers and 709 test files. A third of its commits are fixes, and its
most common failure is "success reported, nothing done". None of that is a bug;
it is the consequence of a GUI-first cut that later grew an agent surface.
Removing it from inside a 150k-line tree with 19 invariants costs more than
building a kernel under 15k lines beside it. One user, one migration
(ADR 0010).

## The five decisions

1. **Files are the truth, Git is the history** (ADR 0001). A Git repo on the box
   holds desired state: one folder per service, `box.yml`, `policy.yml`,
   `secrets.age`. Diff, rollback, audit and transparency come for free.
2. **One verb** (ADR 0001). `laib plan` shows, `laib apply` does. Install,
   update and repair are the same path. Apply ends with verify, and a red
   verify is not a success.
3. **Repair in two halves** (ADR 0001 as amended). Mechanical repair runs
   on its own: the converge loop brings the box back to the state. Repair
   that needs understanding is a proposal by a hand, and the autonomy dial
   in `policy.yml` says which classes laib may approve itself, with rollback
   on a red verify. See "Repair" below.
4. **Hands, not users** (ADR 0006, ADR 0013). Every agent and program
   acts as a hand: `resident`, `ops`, `pi`, `claude`, `bote`, `haus` and the
   program hands in `policy.yml`. Each has a token and a tier ceiling there.
   The `ops` hand is the operator: it observes without limit and corrects
   inside an automatic envelope. Only the irreversible is a request to a
   human: removing, rebooting, exposing to the internet. No hand approves
   its own proposal, and none approves what only a human may (ADR 0019 (e)).
5. **Laib is deterministic, agents are operators** (ADR 0001, ADR 0013). No
   LLM code in the kernel. The Operator (`operator/`, its own container,
   ADR 0013, 0027) is the box's local model acting as the hand `ops`; the
   Werkstatt hosts the coding agents; Claude connects through the
   Werkstatt. All of them speak the CLI, exposed as MCP generated from
   the CLI definition, and every action they take is journaled and
   reversible. Laib is their hands, memory and safety net, not their gate.

## Features that exist

1. State repo as the single truth.
2. `laib plan` / `laib apply`: render, diff, snapshot, write, reload, restart, verify.
3. Templates: Quadlet files plus a `laib.yml` contract; registries are Git repos (ADR 0004).
4. Runtime: Podman Quadlet on Fedora CoreOS, rootless, systemd restart, `podman auto-update` (ADR 0002).
5. Proxy + TLS: Caddy, Caddyfile rendered from state, DNS-01 for internal-only certs (ADR 0007).
6. SSO: Authelia + LLDAP, OIDC clients and access rules rendered from state.
7. DNS: AdGuard rewrites rendered from state.
8. Secrets: one `age` file, decrypted only during apply; generated once, reused forever (ADR 0006).
9. Snapshot before every apply; `laib rollback` is revert plus apply.
10. Backup: restic over the state repo plus declared data paths, systemd timer, `laib restore`.
11. Observe: `laib status`, `laib logs`, `laib diag`; probes as scripts with structured output.
12. Journal: one JSONL file per day; every action with actor, reason, diff ref.
13. Proposals and approvals: a proposal is a branch; tiers from `policy.yml`.
14. Agent surface: MCP generated from the CLI, one token per hand with TTL.
15. Box page: three pages, Status, Inbox, Journal, served by `laib serve`.
16. Werkstatt: one template with repos, pi, Claude Code, the autoloop, `dev.<domain>`, SSH.
17. `laib context`: the starter bundle for an agent session.
18. `laib init`: first setup as a dialogue; USB install stays a script.
19. An unmanaged unit on the box is drift: ignore it or bring it back as a template (ADR 0008; `laib adopt` went with #798).
20. Only what touches the host runs on it (ADR 0027): `laib`, `laib-disk`,
    `self-update.sh` and the backup. `laib-operator`, `laib-notify` and
    `laib-haushalt` are Quadlet containers laib renders
    (`quadlet/<name>/<name>.container`), `laib-lease` is the llama
    template's container: the image the release pins by digest into laib,
    the host's network, a read-only root without capabilities, their own
    folders as the only mounts, their tokens as podman secrets. The age key,
    `secrets.age`, the state repo and the journal are not in reach. The verb
    door hands a call to `laib-haushalt` over HTTP; the lease pauses
    services through laib's `pause` verb over MCP.

## Features that are dropped

The owner on #441 (2026-10-19): "Das Interface ist jetzt der Chat, das heißt
aber nicht, dass die Daten deshalb falsch sein dürfen." Start/stop, live
logs and the backups come back as verbs the chat hands call (`laib start`,
`laib stop`, `laib logs`, `laib backups`), each answering from the running
state or the journal, not as web pages.

- The Next.js dashboard: wizard, network map, backup page, browser terminal, YAML editor, registry browser, settings pages.
- 28 JSON stores, the digital twin, the store version ledger.
- Nginx Proxy Manager, its admin client, the rekey logic.
- The credentials page and its Bitwarden CSV come back as the recovery sheet's list of logins and `laib recovery-sheet --vaultwarden <file>` (owner, mdopp, 2026-10-19, #441).
- Credential self-heal and 36 diagnose probes with action buttons. About ten probe scripts remain. One-click repairs come back through the chat, not a button (owner, mdopp, 2026-10-19, #441): a finding's `repair` verb becomes a proposal with `laib repair`, and it runs once a human approves.
- 64 hand-written MCP tools and the in-code policy table.
- Learning proposals, the Hermes and Claude-dev modules, the chat embed.
- The portal with access requests; onboarding residents against LLDAP is a card on Haus's Box page.
- The Go TUI.
- The Fritz!Box poller, the disk-import worker, the unmanaged bundle builder. The disk import is a Werkstatt skill (`importing-an-old-disk`) with `scripts/laib-import.py` for the copy (#456).
- socket.io, email notifications, health-check CRUD. Health lives in the template; messages to the owner go through laib-notify: the Bote first, mail as the fallback (#441).
- Multi-node operation and the SSH pool. Laib is one box (ADR 0003).
- pi-web and claude-dev as separate templates, the laptop-side autoloop, the public MCP origin for Claude.
- Hermes, unless it clears the threshold in ADR 0009.

## ServiceBay's features, decided (#441)

The list above was written at the founding commit without the owner. Each
ServiceBay feature it dropped, or that vanished unnamed, now has a decision.
Where a line here and the list above disagree, this one holds.

**The interface is the chat.** The owner, 2026-10-19 (#441): "Das Interface
ist jetzt der Chat, das heißt aber nicht, dass die Daten deshalb falsch sein
dürfen." What ServiceBay showed on web pages comes back as verbs and MCP
tools the chat's hands call (pi, Claude, later the Bote), each with its tier,
not as pages. **The rule:** a chat answer about the box comes from a Laib
tool that reads the rendered or running state, never from templates or the
model's guess; where no such tool exists, the hand says so instead of
reconstructing. Which hand may call what is the owner's call (#392).

**Back, as verbs the chat calls:**

- **Network map.** Entscheidung: mdopp, 2026-10-19 — the agents guessed the network wrong without it. Done: `laib net` and `--mermaid` (#440), read tier.
- **Start and stop of a service.** Entscheidung: mdopp, 2026-10-19 — `restart` exists, but keeping a service off (and on again) had no verb.
- **Live logs, status, inspect, look.** Entscheidung: mdopp, 2026-10-19 — they exist at the read tier (`laib logs`, `status`, `inspect`, `look`) and are the chat's way to read a service; a gap here is a missing verb, not a missing page.
- **A shell.** Entscheidung: mdopp, 2026-10-19 — `laib look` (read) and `laib exec` (envelope) in a service's own container; a host shell stays out (ADR 0012).
- **Backup status**: the last nightly backup, the off-box copy's age, the last restore test, in one answer. Entscheidung: mdopp, 2026-10-19 — one line "Letzte Sicherung" does not say whether the backup can be trusted.
- **One-click repairs**: a diag finding's fix becomes a proposal. Entscheidung: mdopp, 2026-10-19 — the fix verb is known; the chat files it, a human approves it.
- **Credentials handover**: the admin logins Laib holds go into Vaultwarden and onto the recovery sheet. Entscheidung: mdopp, 2026-10-19 — the household must reach its apps when the admin is not there.
- **Disk import**, as a Werkstatt skill with a deterministic copy tool. Entscheidung: mdopp, 2026-10-19 — importing an old disk is a judgement (what goes where) plus mechanics (copy, verify, dedupe); the agent judges, the tool copies.
- **Notifications through the Bote's channels, mail as the fallback.** This replaces ServiceBay's mail on a destructive action. Entscheidung: mdopp, 2026-10-19 — the owner reads Signal before mail; the mail of #393 stays for when no channel answers.
- **Who approves what from the chat.** Entscheidung: mdopp, 2026-10-19 — a destructive action is approved only in the Inbox, with two factors; the chat tells about it and sends the link. A non-destructive one may be approved in the chat.
- **OS updates**, with the GPU surviving them. Entscheidung: mdopp, 2026-10-19 — an unpatched host is no option. Done (#368).

**Dropped on purpose:**

- **Web pages for the above** (a network page, start/stop buttons, a log viewer, a backup page). Entscheidung: mdopp, 2026-10-19 — the interface is the chat.
- **Web terminal.** Entscheidung: mdopp, 2026-10-19 — pi in the browser and Claude's Remote Control cover it; a host shell stays out (ADR 0012).
- **Health history graphs.** Entscheidung: mdopp, 2026-10-19 — the journal has the history.
- **Settings, registry browser and setup pages.** Entscheidung: mdopp, 2026-10-19 — the state repo and the agents are the interface.
- **Learning proposals** (`propose_learning`). Entscheidung: mdopp, 2026-10-19 — replaced by pull requests to the Werkstatt guide's skills (#394).
- **Fritz!Box integration** (TR-064: port forwards, DNS). Entscheidung: mdopp, 2026-10-19 — a one-time setup, not worth the attack surface.
- **The `:dev` canary channel.** Entscheidung: mdopp, 2026-10-19 — vm-install tests every release.
- **Multi-node over SSH.** Entscheidung: mdopp, 2026-10-19 — Laib is one box (ADR 0003).
- **The factory-reset assistant.** Entscheidung: mdopp, 2026-10-19 — the stick reinstalls the box.

## What agents do that scripts cannot

The line is not easy versus hard; it is known versus unknown. A script can do
anything someone already wrote down as input and output. An agent does what
has no such description yet:

1. Explain a failure no probe knows: forming the hypothesis, not testing it.
2. Turn intent into configuration: "Paperless for the family, internal only" into template, values, SSO group and route, asking the one question that matters.
3. Weigh and ask: which option, when to ask the human, and how to phrase it so the household understands.
4. Build the unplanned: a template that does not exist, a migration for an upstream break.
5. Condense knowledge: an incident becomes an assist, abstracted, not transcribed.
6. Bridge worlds: Home Assistant state, box state and the family calendar. "Not tonight, movie night."
7. Verify with judgement: exit 0 is not "it works". Check the denominator.
8. Explain: the journal is facts; the agent says what happened and what it means.

And the other half: an agent's output is always an artifact, never a side
effect. Apply, wait, poll, flip back, snapshot and notify are done by `laib`.

Within the agent column:

| Task | Local model (the Operator, `ops`) | Claude (via Werkstatt) |
|---|---|---|
| Watch state, logs, health; triage | yes, always on | no |
| Look inside: inspect, read, exec for looking | yes | yes |
| Correct: restart, fix a config, reset a password, apply a value | yes, inside the envelope | yes |
| Answer residents about the box; the morning report | yes | no |
| Root cause across services, an upstream that changed its format | first attempt, then `laib escalate` | yes |
| Write a template, migration, hook | no | yes |
| Remove, reboot, expose publicly | never: a proposal for the human | never |
| Tier ceiling | `apply` | `apply`, session TTL |

The envelope (ADR 0013): a pre-repair snapshot of the service's data once
per hour, every action journaled with its output, verify after a restart,
rollback on red, eight mutating actions per service per hour before the
hand is stopped and `escalate` is the way on.

## Every touch answers at once

UX is part of the feature, not a polish pass after it (owner, 2026-10-23,
#588). Every touch — a button in Haus, a card in the Inbox, an answer of
the Bote, a verb's reply — answers at once, shows its progress, and ends
visibly where it began, in one of three states:

- **done**, with what is now true;
- **an error** with its reason in the family's words and the next step;
- **waiting** with a choice: what is needed, from whom, and the buttons.

No silent buttons, no endless spinners (a wait has a limit and then says
so), no raw error text in front of the family. Phone first: the end state
fits a phone screen without scrolling to it. Nobody reviews a PR before it
merges (#866); checks hold this: `cmd/laib/taps854_test.go` runs every POST
route of the pages and fails one that ends in none of the three states
(#854), and the `plain855_test.go` lints keep raw text out of what people
read (#855). What they do not see (a page's own script, Haus) is a
guideline, not a gate.

Example: Haus „Scanner einrichten“ showed nothing after the tap. Laib had
answered in 4 s — no scanner found, enter the address by hand — and Haus
never showed that answer. The search worked; the feature did not.

## The user's path

"Ich hätte gern Paperless für die Familie, nur intern." The Bote asks the one
question, writes the plan into the state repo as a proposal, and laib-notify
sends the admin the card's line with the Inbox link. Approval in the Inbox
(#778), the run applies it, answer: "Läuft unter
paperless.<domain>, SSO ist drauf." If no template exists, the request becomes
a ticket for the Werkstatt, a PR, a merge, and then the same path. The user sees
the same card both times and never chooses the agent; a router does:

| Ticket | First hand | Escalation |
|---|---|---|
| Small, clear, one repo | pi with the local model, one attempt | failure or label `needs-claude` |
| New template, migration, cross-service root cause | Claude directly | human |
| Anything with destroy, reboot, secrets | nobody without a card | human |

## Repair: the desired state wins, and a dial says how far laib goes alone

The operator's picture: define the desired state, an agent keeps the box
there. Laib does that in two halves, because the two need different
guarantees.

**Mechanical, always on.** `laib converge` runs every ten minutes and after
boot (`autonomy.converge: on`), under the one lock every mutating verb
holds (a tick beside a restore is skipped, #39), and it leaves paused
services alone (`laib pause`): it re-applies files that drifted and
restarts units that are not active, toward the state repo and never from
the box into it. Guardrails carried over from ServiceBay ADR 0012: never
regenerate a secret, diff-first and journaled, a budget of three runs per
hour after which it stops and tells a human through the journal and the
webhook. A service that needs more is flapping, and flapping is a finding,
not a chore. Images change in two ways (ADR 0002 amendment, #528): those
of services without data nightly by `laib-images.timer` after the backup,
verified, the previous image back on red; those with data by a proposal a
person approves, snapshotted first. podman auto-update stays masked.

**Understanding, by the dial.** When converge cannot help (a unit that fails
on every start, a red health, an upstream that changed its config format),
the ops hand reads the logs, forms a hypothesis and makes a proposal with
content. `policy.yml autonomy` says per class whether laib approves it
itself:

| Class | Example | Default | "auto" means |
|---|---|---|---|
| values | a newer image tag, a changed port | auto (`laib init`) | laib merges; the next run applies it and reverts its merges when verify is red (#609) |
| template-update | the registry shipped a new version | auto | the weekly sync proposes and laib applies, rollback on red |
| new-service | "install Paperless" | proposal | laib adds it when no purpose warning fires |
| patch | anything else an agent can express as a diff | auto | same |
| human | what the content review marks (below) | — | never auto, whatever the dial says |
| remove | delete a service | never auto | a human, always (ADR 0006) |

**The content decides, not the shape (#34).** Before the dial is asked,
`internal/review` reads the change against main — files, contracts, the
rendered units — and names, in plain German, what only a human may allow:
a script that runs on the box (post-deploy, a host unit, an `Exec*` line in
a container unit), `policy.yml`, `secrets.age`, `ignore.yml`, `box.yml`
roles and friends, a public route or a weaker login, a path without login,
hand-written Caddy, a token (`mint`) or an external secret, another
service's secret, a host folder as a volume or another service's folder
(#666), a port opened to the LAN or to other machines, `Network=host`,
podman arguments and other privileges,
a program from a different image repository, a restriction taken away,
data taken out of the backup, a removed service, a file Laib does not know,
a symlink, a state that does not load. Such a proposal is class `human`:
the inbox and `laib inbox` show the reasons, and an agent's `approve` is
refused with them. A warning (a second service for one purpose) goes to a
human too. A non-human hand may not add a token for a hand above its own
ceiling at all: the proposal is refused, nothing is kept. Every auto
decision is journaled as `laib approve` with the proposing hand named, so
"who changed this?" has an answer.

**The envelope fails closed (#35).** Every class-2 action (`write`, `exec`,
`restart`) runs inside it: the budget (`policy.yml envelope.budget`, eight
per service per hour by default, shared by all hands; a refused action does
not count) is read from the journal, and a journal that cannot be read
refuses the action;
the first action of an hour takes a restic snapshot of the service's
backed-up data paths (`backup: false` paths are not snapshotted; pre-repair
snapshots are kept seven days), and a missing backup target or a failed
snapshot refuses the action — `policy.yml envelope.snapshot: optional` is
the only way around it. `write` and `read` go through an `os.Root` opened
on the service's data path, so no symlink and no `..` leaves it; `exec`
runs only in the service's own containers; `look` runs a read-only command
there without budget or snapshot, because looking is unbounded.

**Watching.** The ops hand watches the journal, `/ready` and `laib logs`
on its tick (`operator.tick`, ten minutes); red lines also reach it through
the webhook (`notify.url`, which `laib operator enable` points at the
Operator's `/event`). Its
job is triage and proposals, never apply.

**The market.** Suggesting a better tool is judgement work for the
Werkstatt: a periodic ticket per purpose ("is there something better for
photos than Immich now?") answered by Claude with a short report in the
journal and, if it convinces, a template PR. It never installs anything.

## When there is no template

The user's path does not change; the first hop does. The Bote finds no
template in `laib template list` (built-ins plus every registry) and says
so: "Dafür gibt es noch kein Template, ich lege ein Ticket für die
Werkstatt an." That is `laib wish` (#56): a ticket in `box.yml intake.repo`
with the `wunsch` label. The ticket carries the upstream link and the wish in the
user's words. In the Werkstatt:

1. The router picks the hand: pi for an upstream compose file that maps
   one-to-one onto Quadlet (`podlet` turns compose into `.container`
   files), Claude for anything with OIDC, migrations or several containers.
2. The agent writes the folder (units, `laib.yml` with routes, secrets,
   data, health, ports, `provides`), runs `laib template lint`, and opens a
   PR against the household registry.
3. A human merges. `laib template sync` pulls it; the Bote sees it in the
   list and asks: "Das Template ist da, soll ich es vorschlagen?" Then the
   ordinary path: `laib propose --add`, Inbox, apply.

For the household's own small programs (a wish no upstream app answers),
the Werkstatt writes the program too. Program and template go into one app
registry repo (`mdopp/laib-apps`). The path from scaffold to checked route
is `werkstatt/guide/apps.md`, and the mechanics are `laib-app` (#262).

Two registries, deliberately: the **built-ins** are embedded in the binary
(`templates/embed.go`), so a fresh box installs offline (#14); a **registry**
is a Git repo in `box.yml registries` that `laib template sync` clones into
`/mnt/data/laib/registries/<name>`. A registry template wins over the
built-in of the same name, and `laib template update <svc>` (which syncs
first) proposes its diff only when the content differs.

What is true today (#906): every template still sits in this repo and is
embedded; the household's are not moved out yet. The box reaches a template
fix without a kernel release by naming this repo as its registry
(`{name: laib, url: https://github.com/mdopp/laib, ref: main, path:
templates}`): a template change merged to main is a proposal after the next
sync, with no vm-install and no release. The embedded copy stays the floor
when GitHub is out of reach. The limit: the box reads main's template with
the kernel it runs, and a `laib.yml` field that kernel does not know is
ignored, not refused. A template change that needs a kernel change reaches
the box before that kernel does; its proposal then shows a diff the running
Laib renders the old way, so it is approved only after `laib updates` has
the release.
The aim stays a household registry of its own (`mdopp/laib-templates`,
#24) holding everything that is not a platform role (caddy, auth, adguard,
llama, whoami, werkstatt), so the kernel repo does not grow with every app.
A template never adds a tool, a probe or a page to Laib; it is files.

## Not a hundred tools for one topic

Two sprawls, one rule each.

- **Services.** `laib.yml provides: [photos]` names what a service is for.
  A second service for a purpose is a `HINWEIS` in `laib plan` and in the
  Inbox entry of the proposal that would add it, so the human decides with
  the duplication in view. Adding is always a proposal; nobody installs by
  accident. The status page groups by purpose, so "three things for photos"
  is visible, not buried.
- **Agent tools.** The verb table is the only source of MCP tools, filtered
  by the hand's ceiling; `serve`, `init`, `token` and `secret` are never
  tools; templates cannot add tools. A new verb needs a recurrence, like a
  new probe. The count stays visible in `tools/list`. Solaris, the retired agent,
  learned the cost once: two thirds of a 12k-token prefill were tool
  definitions.

## The agents and the GPU

The model server, router, policy proxy and the lease endpoint are the Laib
template `llama` with the role `model` (ADR 0011). Consumers ask the lease
for a preset: the Bote may ask for the large one for a hard question and
falls back to the household preset when the card is busy. `box.yml`
declares the GPU so `laib plan` shows a conflict before apply. The
Werkstatt's working window is a systemd timer. The Operator always runs on
the household preset and never takes the lease.

## Readiness

Laib has one state with three values, shown in the same place everywhere: the
first line of the box page, Haus (which reads `/ready`) and the first line of
`laib status`.

| State | Means | What you can do |
|---|---|---|
| `bereit` | daemon answers, state repo readable, apply possible | everything |
| `eingeschränkt` | daemon answers, apply is locked (an apply is running, or the Werkstatt holds the GPU) | ask, read, propose, approve (it applies with the next run, #609) |
| `nicht erreichbar` | no answer since time X | the Bote and Haus still answer; what needs Laib waits |

Readiness is about what you can do, not about what is red. A red service, a
full disk, a failed backup or a missing firewall leave it `bereit`; they are
the red count and the red lines beside it (#856).

One function computes the verdict (`pages.judge`, #856), and every surface
reads it: `/ready`, the Status page, `laib status` (and `--json`, which Haus
reads over `laib_status`). It holds the state, what is red, the last backup
with its places, its age and why it does not hold, and the last change (the
last approved proposal). Red is what `laib diag` finds red, the probes the mail
reads: units, health, the service's own status file, disk pressure, the
backup, the firewall and the Operator are probed on every look; the slow ones
(RAID, SMART, the certificate, the box's config) come from the last whole
`laib diag` run, with its time. The backup holds while it is green and no
backup finding is red (failed, or older than 36 h); a stale copy off the box
lowers its places to one.

Laib and the agents are separate processes. If Laib falls, the Bote still
talks, but its Laib tools do not answer. After a reboot, systemd starts
Laib, Laib compares the units against the rendered state and journals the
result. Anything that did not come up is red on the first lines of Status and
a proposal in the Inbox, never a log line you have to find.

Every card says what changes, what it means for the family, how it is undone
and its risk; a snapshot before the change is among its warnings, and the
tier sits under Details (#856). No action's consequences are learned after
the click.
