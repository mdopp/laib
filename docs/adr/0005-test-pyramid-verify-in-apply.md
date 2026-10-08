# ADR 0005 — Golden renderers, one booting integration job, verify inside apply; no gates until recurrence

Applies when you add a test, a CI job, a lint rule, a ratchet, or wonder what
"done" means.

- **Status:** Accepted, 2026-10-08

## Context

ServiceBay had more test lines than production lines, 19 invariant checks,
three ratchets, Playwright specs that never ran, and a real-box verify that
two agents died in the middle of. Its most common failure stayed "success
reported, nothing done". Tests proved the loader; proof was on the box.

## Decision

See `docs/TESTING.md`. In short: golden files for every renderer; contract
tests for every template; one CI job that boots and applies; verify is part of
`laib apply` and a red verify is exit 2. Test lines at most equal production
lines. No ratchets or invariant scanners at the start; a gate is added the
second time a shape breaks.

## Consequences

- The kernel's size is the architecture gate. Under 15k lines the structure is
  visible without a scanner.
- The real box is a canary, not a gate; an agent can never strand it mid-verify
  because the verify is inside the script.
