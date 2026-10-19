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
4. **Hands, not users** (ADR 0006, ADR 0013). Four agent identities:
   `resident`, `ops`, `pi`, `claude`. Each has a token and a tier ceiling in
   `policy.yml`. The `ops` hand is the operator: it observes without limit
   and corrects inside an automatic envelope. Only the irreversible is a
   request to a human: removing, rebooting, exposing to the internet. No
   token can approve its own request.
5. **Laib is deterministic, agents are operators** (ADR 0001, ADR 0013). No
   LLM code in the kernel. Solaris hosts the local model that is the
   operator; the Werkstatt hosts the coding agents; Claude connects through
   the Werkstatt. All of them speak the CLI, exposed as MCP generated from
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
19. `laib adopt`: an unmanaged unit on the box is drift; adopt it or ignore it (ADR 0008).

## Features that are dropped

- The Next.js dashboard: wizard, network map, backup page, browser terminal, YAML editor, registry browser, settings pages.
- 28 JSON stores, the digital twin, the store version ledger.
- Nginx Proxy Manager, its admin client, the rekey logic.
- Credential self-heal and 36 diagnose probes with action buttons. About ten probe scripts remain.
- 64 hand-written MCP tools and the in-code policy table.
- Learning proposals, the Hermes and Claude-dev modules, the chat embed.
- The portal with access requests; onboarding is a Solaris card.
- The Go TUI.
- The Fritz!Box poller, the disk-import worker, the unmanaged bundle builder. The disk import is a Werkstatt skill (`importing-an-old-disk`) with `scripts/laib-disk.py` for the copy (#456).
- socket.io, email notifications, health-check CRUD. Health lives in the template; messages go through Solaris.
- Multi-node operation and the SSH pool. Laib is one box (ADR 0003).
- pi-web and claude-dev as separate templates, the laptop-side autoloop, the public MCP origin for Claude.
- Hermes, unless it clears the threshold in ADR 0009.

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

| Task | Local model (Solaris `ops`) | Claude (via Werkstatt) |
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

## The user's path

"Ich hätte gern Paperless für die Familie, nur intern." Solaris asks the one
question, writes the plan into the state repo as a proposal, shows the card
with the diff. Approval, `laib apply`, answer: "Läuft unter
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
not a chore. `podman auto-update` keeps images current inside the window;
`laib diag --journal` checks afterwards.

**Understanding, by the dial.** When converge cannot help (a unit that fails
on every start, a red health, an upstream that changed its config format),
the ops hand reads the logs, forms a hypothesis and makes a proposal with
content. `policy.yml autonomy` says per class whether laib approves it
itself:

| Class | Example | Default | "auto" means |
|---|---|---|---|
| values | a newer image tag, a changed port | auto (`laib init`) | laib merges, applies, and reverts the merge when verify is red |
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
service's secret, a host folder as a volume, a port opened to the LAN or
to other machines, `Network=host`, podman arguments and other privileges,
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
`restart`) runs inside it: the budget (eight per service per hour) is read
from the journal, and a journal that cannot be read refuses the action;
the first action of an hour takes a restic snapshot of the service's
backed-up data paths (`backup: false` paths are not snapshotted; pre-repair
snapshots are kept seven days), and a missing backup target or a failed
snapshot refuses the action — `policy.yml envelope.snapshot: optional` is
the only way around it. `write` and `read` go through an `os.Root` opened
on the service's data path, so no symlink and no `..` leaves it; `exec`
runs only in the service's own containers; `look` runs a read-only command
there without budget or snapshot, because looking is unbounded.

**Watching.** The ops hand watches the journal, `/ready` and `laib logs`
on Solaris's own cadence; red lines also reach it through the webhook. Its
job is triage and proposals, never apply.

**The market.** Suggesting a better tool is judgement work for the
Werkstatt: a periodic ticket per purpose ("is there something better for
photos than Immich now?") answered by Claude with a short report in the
journal and, if it convinces, a template PR. It never installs anything.

## When there is no template

The user's path does not change; the first hop does. Solaris finds no
template in `laib template list` (built-ins plus every registry) and says
so: "Dafür gibt es noch kein Template, ich lege ein Ticket für die
Werkstatt an." The ticket carries the upstream link and the wish in the
user's words. In the Werkstatt:

1. The router picks the hand: pi for an upstream compose file that maps
   one-to-one onto Quadlet (`podlet` turns compose into `.container`
   files), Claude for anything with OIDC, migrations or several containers.
2. The agent writes the folder (units, `laib.yml` with routes, secrets,
   data, health, ports, `provides`), runs `laib template lint`, and opens a
   PR against the household registry.
3. A human merges. `laib template sync` pulls it; Solaris sees it in the
   list and asks: "Das Template ist da, soll ich es vorschlagen?" Then the
   ordinary path: `laib propose --add`, Inbox, apply.

For the household's own small programs (a wish no upstream app answers),
the Werkstatt writes the program too. Program and template go into one app
registry repo (`mdopp/laib-apps`). The path from scaffold to checked route
is `werkstatt/guide/apps.md`, and the mechanics are `laib-app` (#262).

Two registries, deliberately: the **built-ins** in this repo hold only the
platform roles (caddy, authelia, adguard, llama), whoami and the Werkstatt,
so the kernel repo does not grow with every app; the **household registry**
(`mdopp/laib-templates`, one Git repo, `laib template lint` in its CI) holds
everything the family actually uses, including what today sits in
solarisbay, flutstunde and foundry-chronicle as ServiceBay registries. A
template never adds a tool, a probe or a page to Laib; it is files.

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
  new probe. The count stays visible in `tools/list`. Solaris learned the
  cost once: two thirds of a 12k-token prefill were tool definitions.

## Solaris and the GPU

The model server, router, policy proxy and the lease endpoint are the Laib
template `llama` with the role `model` (ADR 0011). Solaris is a client of the
lease and keeps the judgement about when to ask for a mode. `box.yml`
declares the GPU so `laib plan` shows a conflict before apply. The
Werkstatt's working window is a systemd timer; deciding to postpone a ticket
because of movie night is Solaris's call, because only Solaris knows the
calendar. The `ops` profile always runs on the household model and never takes
the lease.

## Readiness

Laib has one state with three values, shown in the same place everywhere: the
first line of the box page, the header of the Solaris chat, the first line of
`laib status`, and the spoken answer to "Laib?".

| State | Means | What you can do |
|---|---|---|
| `bereit` | daemon answers, state repo readable, apply possible | everything |
| `eingeschränkt` | daemon answers, apply is locked (an apply is running, or the Werkstatt holds the GPU) | ask, read, propose |
| `nicht erreichbar` | no answer since time X | Solaris still answers and tells you |

Solaris and Laib are two processes. If Laib falls, the conversation stays and
says so, with the last journal line. After a reboot, systemd starts Laib, Laib
compares the units against the rendered state and reports `bereit` only when
the comparison is clean. Solaris says once: "Laib ist wieder da, 12 Dienste
grün." Anything that did not come up is the first line of Status and a proposal
in the Inbox, never a log line you have to find.

Every card says its tier, who must approve, and whether a snapshot precedes it.
No action's consequences are learned after the click.
