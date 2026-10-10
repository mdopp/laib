# ADR 0013 — The agent is the operator; Laib is its hands, memory and safety net

Applies when you are about to require an approval, deny an agent a verb,
or wonder how far the ops hand may go when a health check fails.

- **Status:** Accepted, 2026-10-10
- **Deciders:** operator (mdopp): "Ich möchte einen Agenten, der wie ein
  Operator agiert, wie ein Familien-Admin."

## Context

ADR 0001 and 0012 (as first written) made agents proposers: they read,
they diffed, a human approved. That imported ServiceBay's "destructive
operations need approval" rule and stretched it over everything an agent
does. A family admin does not ask before restarting Jellyfin or resetting
a password; they ask before deleting photos or opening a port to the
internet. Repair that needs looking inside a container, trying something
and reading the result was impossible by construction.

## Decision

1. **The agent is the operator.** The ops hand (Solaris, always on, local
   model) watches state, logs and health, analyses, corrects, and reports.
   It escalates to the claude hand in the Werkstatt itself when stuck. The
   human sees the morning report and decides class 3.
2. **Three classes of action, not approval per step.**
   - *Observe and analyse — always, unbounded:* `status`, `diag`, `logs`,
     `inspect`, `read` (data dir), `exec` for looking (ps, cat, queries),
     network checks.
   - *Correct — when reversible, inside the envelope:* `restart`, `exec`
     that changes something, `write` in the service's data dir, state
     changes through `apply`, updates, `secret rotate`. The **envelope** is
     automatic: a snapshot of the service's data paths before the first
     mutating action in an hour, every action journaled with its output,
     verify afterwards, rollback on red, a budget per service per hour.
   - *Ask — irreversible or outward-facing:* remove a service or its data,
     reboot, a public route, external secrets, anything that costs money.
     These are proposals in the Inbox for a human, and only these.
3. **Laib is the hands, the memory and the net, not the gate.** Agents act
   through Laib's verbs so every action is journaled and reversible. No
   agent gets a shell on the host (ADR 0012); inside a container it may do
   anything of class 1 and 2.
4. **The dial defaults to the operator.** `policy.yml autonomy`: converge
   on; values, template-update and patch auto; new-service proposal (it
   adds surface); remove never. Public exposure of a route is class 3
   regardless of the dial.

## Consequences

- Hands `ops` and `claude` get the ceiling `apply`; `resident` stays read.
- New verbs: `inspect`, `exec`, `read`, `write`, `restart`, `escalate`.
- The "two halves of repair" in ADR 0001 stay: converge is the mechanical
  half; the operator is the understanding half, and it acts.
- A weaker local model may act because mistakes are cheap: snapshot,
  journal, rollback, budget. The envelope is what makes autonomy safe, not
  a human in the loop.
- Every repair episode ends with the question "how does this become
  mechanical next time?" — the journal holds the commands, the answer is a
  template, hook or values change by the same hand.
