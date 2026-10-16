# Laib

**Laib** is the control plane for one home box, built for agents first and
humans second. The name carries the idea: the *Laib* is the daily loaf everyone
in the house takes a slice of; the *Leib* is the body of the house that agents
move as hands.

It replaces [ServiceBay](https://github.com/mdopp/servicebay) for this one box.
Nobody else runs it, so there is exactly one migration and no compatibility
layer (ADR 0010).

## What it is

- **A Git repo on the box is the only truth.** One folder per service, a
  `policy.yml`, a `box.yml`, an `age`-encrypted secrets file. Every change is a
  commit with an author, a reason and a diff. `git log` is the audit log.
- **One verb.** `laib plan` renders the state and shows the diff against the
  box; `laib apply` snapshots, writes, reloads, restarts and **verifies**. An
  apply whose verify is red is not a success.
- **systemd runs the containers**, not Laib. Laib renders Podman Quadlet files
  and reloads systemd. Restart policy, timers and auto-update are systemd's.
- **The agent is the operator; Laib is its hands, memory and safety net.**
  Laib has no LLM code. The household agent (Solaris) watches, looks inside
  containers, corrects and reports; the coding agents in the Werkstatt (pi,
  Claude Code) build what is missing. All speak the same CLI, exposed as
  MCP. Every action is journaled and reversible: snapshot before, verify
  after, rollback on red, a budget against loops. Only the irreversible
  waits for a human (ADR 0013).
- **Three human surfaces, one conversation.** Solaris is where you talk. The
  Werkstatt (`dev.<domain>`) is where things get built. The box page
  (`laib.<domain>`) has three pages: Status, Inbox, Journal.

## Layout

```
cmd/laib/            the one binary: CLI + daemon (`laib serve`), diag probes
cmd/laib-stick/      the laptop tool: builds the Fedora CoreOS install stick
cmd/laib-operator/   the admin agent (ADR 0013), a host unit
cmd/laib-lease/      the model role's policy proxy and GPU lease (ADR 0011)
cmd/frage/           "was ist …": SearXNG + summary, OpenAI-compatible and MCP
cmd/automat/         Home Assistant automations as an MCP tool in the bote pod
cmd/persona/         the Bote's persona engine: directory, levels, gimmicks (ADR 0020)
internal/state       load + validate the state repo
internal/render      pure renderers: quadlet, caddy, authelia, adguard, nftables
internal/plan        state → files → diff against the box
internal/apply       snapshot, write, reload, restart, verify, the one lock
internal/journal     append-only JSONL, one file per day
internal/policy      hands, tiers, who needs approval
internal/proposal    proposals, cards, approval by content
internal/secrets     age-encrypted secrets.age
internal/token       hand tokens
internal/operator    the Operator's loop, prompt and eval harness
internal/lease       the lease and the policy proxy
internal/frage       Frage's search and summary
internal/automat     the Automat's tools
internal/mcpserver   the smallest MCP server (Hermes, Claude Code, laib's client)
internal/stick       the stick wizard's steps (Butane, ISO, Ignition, write)
internal/boxfs       the box's paths
internal/sysd        the systemctl seam
templates/           built-in templates (Quadlet files + laib.yml + hooks)
usb/                 the box's Butane and the live-install scripts laib-stick embeds
werkstatt/           the dev image: repos, pi, Claude Code, autoloop
qmd/                 the memory library's image: qmd as released, CPU only (#195), served by the Vermittler (#198)
scripts/             install-box.sh, e2e.sh, loc.sh, drift.sh, persona-icon.sh
docs/                KONZEPT, ZIELBILD, DESIGN, INTERFACE, STATE, TESTING, INSTALL, BACKUP, adr/
testdata/golden      renderer golden files
```

## Stand (2026-10-11)

Nothing has run on a real box yet. What exists and how far it is proven:

| Piece | Where | Proof |
|---|---|---|
| Kernel: state repo, plan/apply, converge, operator verbs, MCP, pages | `cmd/laib`, `internal/` | unit + golden tests, e2e with shims, real Quadlet on a runner |
| The stick: `laib-stick`, first boot, data disk (RAID1, one member) | `cmd/laib-stick`, `usb/` | CI `vm-install`: KVM guest installs CoreOS, reboots, `laib` ready, mirror joins, reinstall keeps data |
| First services: Caddy (own CA on a trial box), backup + restore | `templates/caddy`, `cmd/laib backup.go` | the same VM run: HTTPS 200, restic round trip |
| Operator (admin agent), lease, Frage | `cmd/laib-operator`, `cmd/laib-lease`, `cmd/frage` | e2e with a scripted model; never against a real model |
| Bote (Hermes on Signal, Matrix and/or Telegram) with the `ha` and `automat` tools; the approval relay | `templates/bote`, `cmd/automat`, `cmd/relay` | config rendered and checked against Hermes' source; automat and relay unit-tested against a fake HA and a fake homeserver |
| Haus (the surface): Box page, pairing, companion API for the widgets | `mdopp/haus`, `templates/haus` | unit tests against fake Laib and Solaris |
| 23 templates for every unit the box runs today | `templates/` | lint + render; caddy, auth, adguard and radicale installed in the VM |

The NUC trial (#23, #27) is the first real run; `docs/INSTALL.md` has the
pre-flight. The Kraftwerk follows `docs/ZIELBILD.md`.

## Read first

0. [`docs/ZIELBILD.md`](docs/ZIELBILD.md) — the end state (one box, the
   Kraftwerk) and the five phases from the NUC trial to retiring ServiceBay.
1. [`docs/KONZEPT.md`](docs/KONZEPT.md) — the whole concept as decided:
   constructs, division of labour, interfaces by user group, models.
   [`docs/DESIGN.md`](docs/DESIGN.md) is the kernel's design underneath.
2. [`docs/INTERFACE.md`](docs/INTERFACE.md) — the CLI, the MCP surface, the
   three pages, the Solaris cards, the readiness signal.
3. [`docs/STATE.md`](docs/STATE.md) — the state repo layout and every file's
   schema.
4. [`docs/adr/`](docs/adr/) — the decisions. Read the index in
   [`docs/adr/README.md`](docs/adr/README.md) before changing anything about
   runtime, identity, proxy, templates or tests.
5. [`docs/MIGRATION.md`](docs/MIGRATION.md) — the one-shot move from ServiceBay.
6. [`docs/DEKONSTRUKTION.md`](docs/DEKONSTRUKTION.md) — how Solaris and the
   Android app split into Solaris, Haus, Stimme and the Laib Operator.
7. [`docs/C4.md`](docs/C4.md) — the system map in three C4 levels plus the
   "a service for the family" sequence.
8. [`docs/SERVICES.md`](docs/SERVICES.md) — what runs on the box today
   and which templates Laib still needs for it.
9. [`docs/BACKUP.md`](docs/BACKUP.md), [`docs/INSTALL.md`](docs/INSTALL.md),
   [`docs/OUTSIDE.md`](docs/OUTSIDE.md) — backup, install, and what Laib
   leaves to the router, to LLDAP and to nobody.

## Build

```
go build ./cmd/laib
go test ./...
```

Go 1.27, no CGO, one static binary. The binary is installed on Fedora CoreOS
via Ignition at `/var/opt/laib/laib` and runs as a systemd user service of the
`core` user (ADR 0002).

## Status

Founded 2026-10-08; reviewed 2026-10-11 (#31, worked through #60). Nothing
has run on a real box yet: the VM job is the proof, the NUC trial (#23) is
the first real run. See `docs/MIGRATION.md` for the order in which services
move and `docs/REVIEW.md` for what is proven and what is not.
