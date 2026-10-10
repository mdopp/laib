# ADR 0001 — Agent-first: a Git state repo is the truth, `plan`/`apply` is the one verb, no LLM code in the kernel

Applies when you are about to add a store, a second write path, a background
reconciler, a form, or any LLM call inside Laib.

- **Status:** Accepted, 2026-10-08
- **Deciders:** operator (mdopp)

## Context

ServiceBay kept desired state in 28 JSON stores plus a digital twin, exposed
it through 185 routes and 64 MCP tools, and repaired drift with reconcilers
and probe actions. Each store was a place for two writers to disagree, each
route a seam that could change shape unseen, each reconciler a thing that
could fight a human. Its ADR 0012 already forbade autonomous reconcile loops.
With agents as the operators, the human's job is to read and approve, so the
system must be diffable and attributable above all.

## Decision

1. Desired state is a Git repository on the box (`docs/STATE.md`). Every
   change is a commit with author, reason and diff. There is no other store.
2. `laib plan` renders and diffs; `laib apply` snapshots, writes, reloads,
   restarts and verifies. Install, upgrade and repair are the same path.
3. **Repair in two halves (amended 2026-10-10, operator decision).**
   *Mechanical* repair runs unattended: the converge loop re-applies drifted
   files and restarts inactive units every ten minutes, always toward the
   state repo and never from the box into it, never generating a secret
   anew, within a budget of three runs per hour before it stops and tells
   a human. *Understanding* repair (a config change, a new template, a new
   service) is a proposal by a hand; what laib may approve itself is the
   autonomy dial in `policy.yml`, per proposal class, with rollback on a red
   verify. `remove` is never automatic. systemd's own machinery (restart
   policies, auto-update) runs regardless.
4. The kernel contains no LLM call and no agent loop. Agents are clients over
   the CLI/MCP. An agent's output is an artifact, never a side effect.

## Consequences

- Audit log, rollback, transparency and "who did this" come from Git.
- A failing apply is loud (exit 2, red journal line), never silently retried.
- Agents cannot be blamed for a step that must always happen; those steps are
  in `laib`.
- Secrets cannot live in the repo in clear text (ADR 0006).
