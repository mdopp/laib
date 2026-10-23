# ADR 0026 — Household verbs live outside the kernel; the kernel offers scoped proposals and a verb door

Applies when you are about to add a verb for a household feature (a
service's order, a person's detail, a box.yml setting the family edits, a
look at the LAN), when the kernel's line cap is in the way of one, or when
you wonder whether new code belongs in `cmd/`/`internal/` or in a program
of its own.

- **Status:** Accepted, 2026-10-23
- **Deciders:** the owner (mdopp), on "warum sind solche Themen Teil des
  Kernes und nicht separat?" and the answer below: "mach das".

## Context

The kernel (`cmd/`, `internal/`) is what the household has to trust: state,
render, apply, proposals and their review, policy, the journal. Its cap
(`scripts/loc.sh`) keeps it small enough to read.

Since v0.18 the kernel's growth has mostly been household verbs:
`calendar` (#495), `zuhause` (#561), `scan` (#568, #571),
`person_invite`/`person_pending` (#360), `person_signal` (#596), and
`net geraete` (#577). Each one is a thin shell: it checks an input and
turns it into a box.yml proposal, an order file or a read. They are in the
kernel only because the verb table, the MCP schema and the policy check
are there, and adding a verb was the cheapest path. On 2026-10-23 that put
the kernel at 16,860 of 16,900 lines, and each next feature would mean
asking for another raise.

There are already programs outside the kernel with their own caps that
meet it over MCP, files or HTTP and never by import: the Operator
(ADR 0023), `laib-disk` and `laib-notify` (#443), persona, mcpserver.

## Decision

1. **The kernel keeps only what decides trust.** That is state, render,
   apply, proposals and review, policy, the journal, the lock, backups,
   and the self-update. A household feature is not trust.
2. **Scoped structured proposals.** The kernel offers one primitive: a
   proposal that sets or removes values at named paths of box.yml or a
   service's values.yml. The edit is a line edit that keeps comments, the
   way `state.WithBlock` does since #596. Policy can grant a hand *paths*,
   not only verbs. Example: `haus` may propose `people.*.signal` and
   `zuhause`. The content review, its class and the Inbox apply unchanged:
   a box.yml change waits for a human.
3. **A verb door.** A verb can be served by a program beside `laib`
   (`/var/opt/laib/laib-<name>`), declared in the verb table with its tier
   and MCP schema as data. The kernel checks the hand, the tier and the
   verb, then runs the program with the caller's actor and a token that
   may only read and propose. Such a program never changes the box itself.
   Each program has its own cap in `scripts/loc.sh`, as ADR 0023 set for
   the Operator.
4. **The household verbs move out.** `scan`, `zuhause`, `person_*`,
   `calendar` and `net geraete` move behind the door or onto the scoped
   proposal, and keep their names and shapes. Haus, the Bote and the
   Werkstatt call the same verbs as before.

## Consequences

- A new household feature costs lines in its own program, not in the
  kernel. The kernel cap stops being the gate on household work.
- The kernel shrinks by the moved verbs, minus the door and the scoped
  proposal.
- Policy gains path grants. They are new review surface: a path grant is
  a policy.yml change and so the owner's decision, like a verb grant
  (#575).
- One more process hop per household call. The verbs are interactive
  but not hot, so this does not matter.
- Until the move is done, a household verb that must land goes into the
  kernel under the current cap, with this ADR named in its PR.
