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
3. **No self-healing in the product** (ADR 0001, ServiceBay ADR 0012 carried
   over). Probes are scripts that emit findings and a proposed diff. The agent
   proposes, the policy or a human approves, `laib apply` does. What may run
   unattended is systemd's own: restart policies, `podman auto-update`, timers.
4. **Hands, not users** (ADR 0006). Four agent identities: `resident`, `ops`,
   `pi`, `claude`. Each has a token and a tier ceiling in `policy.yml`.
   `destroy`, `reboot` and `exec` are always a request to a human. No token
   can approve its own request.
5. **Laib is deterministic, agents are clients** (ADR 0001). No LLM code in the
   kernel. Solaris hosts the local model; the Werkstatt hosts the coding
   agents; Claude connects through the Werkstatt. All of them speak the CLI,
   exposed as MCP generated from the CLI definition.

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
- The Fritz!Box poller, the disk-import worker, the unmanaged bundle builder. They become scripts that call `laib`.
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
| Summarise, triage, rate severity | yes | rarely needed |
| Answer residents about the box | yes | no |
| Nightly ops report, draft a question for the admin | yes | no |
| Root cause across services | groundwork only | yes |
| Write a template, migration, assist | no | yes |
| Review a proposed diff | no | review yes, approve never |
| Tier ceiling | `read` + `propose` | `read` + `propose` + `apply` within policy, session TTL |

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
