# ADR 0008 — An unmanaged unit is drift: adopt or ignore, no bundle builder

Applies when you find a hand-rolled unit on the box or want to show "what else
is running".

- **Status:** Accepted, 2026-10-08

## Context

ServiceBay's bundle builder (1.7k lines) discovered hand-rolled Quadlet units,
grouped, validated and offered to adopt them.

## Decision

Anything running on the box that is not in the state repo is drift.
`laib plan` lists it as one line, `nicht verwaltet: <unit>`. `laib adopt
<unit>` copies the unit into `services/<name>/` and writes a minimal
`laib.yml`; `laib ignore <unit>` records it in `ignore.yml`. What belongs
together is the adopting agent's judgement.

## Consequences

About fifty lines and two verbs replace the builder. During the migration
(ADR 0010) ServiceBay's units are listed under `ignore.yml` until each moves.
