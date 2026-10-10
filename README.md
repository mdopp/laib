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
- **Agents are clients, never modules.** Laib has no LLM code. The household
  agent (Solaris), the coding agents in the Werkstatt (pi, Claude Code) and a
  remote Claude session all speak the same CLI, exposed as MCP. An agent's
  output is an artifact: a diff, a proposal, a question, a report. Scripts do
  the side effects.
- **Three human surfaces, one conversation.** Solaris is where you talk. The
  Werkstatt (`dev.<domain>`) is where things get built. The box page
  (`laib.<domain>`) has three pages: Status, Inbox, Journal.

## Layout

```
cmd/laib/            the one binary: CLI + daemon (`laib serve`)
internal/state       load + validate the state repo
internal/render      pure renderers: quadlet, caddy, authelia, adguard, nftables
internal/plan        state → files → diff against the box
internal/apply       snapshot, write, reload, restart, verify
internal/journal     append-only JSONL, one file per day
internal/policy      hands, tiers, who needs approval
internal/probe       diag probes: scripts with structured output
templates/           built-in templates (Quadlet files + laib.yml)
werkstatt/           the dev image: repos, pi, Claude Code, autoloop
docs/                DESIGN.md, INTERFACE.md, STATE.md, TESTING.md, MIGRATION.md, adr/
testdata/golden      renderer golden files
```

## Read first

1. [`docs/DESIGN.md`](docs/DESIGN.md) — the whole concept: what exists, what
   was dropped, and why.
2. [`docs/INTERFACE.md`](docs/INTERFACE.md) — the CLI, the MCP surface, the
   three pages, the Solaris cards, the readiness signal.
3. [`docs/STATE.md`](docs/STATE.md) — the state repo layout and every file's
   schema.
4. [`docs/adr/`](docs/adr/) — the decisions. Read the index in
   [`docs/adr/README.md`](docs/adr/README.md) before changing anything about
   runtime, identity, proxy, templates or tests.
5. [`docs/MIGRATION.md`](docs/MIGRATION.md) — the one-shot move from ServiceBay.
6. [`docs/C4.md`](docs/C4.md) — the system map in three C4 levels plus the
   "a service for the family" sequence.
7. [`docs/BACKUP.md`](docs/BACKUP.md), [`docs/INSTALL.md`](docs/INSTALL.md),
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

Founding commit, 2026-10-08. Nothing runs on the box yet. See
`docs/MIGRATION.md` for the order in which services move.
