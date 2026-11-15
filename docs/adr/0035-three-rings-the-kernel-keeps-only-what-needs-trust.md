# ADR 0035 — Three rings: the kernel keeps only what needs trust

Applies when you add a verb or a package to `cmd/laib` or `internal/`,
when the kernel's line cap (`scripts/loc.sh`) is near, or when a part of
Laib talks to the internet, parses data it did not write, or only reads
and judges.

- **Status:** Accepted, 2026-11-15
- **Deciders:** the owner (mdopp), on „gehört das alles in den Kern? Oder
  sollte davon etwas in ein eigenes System … mit anderen
  Sicherheitsstufen? Etwas das eh nicht so funktioniert wie es soll und
  weg kann oder neu gedacht werden sollte?“, and on the review below:
  „Mach das“.

## Context

The kernel is at 22,462 of 22,500 production lines (#1047). The diet
(#1229) found no further cut *inside* the trust kernel, and it is right
about that. But the kernel still does work that needs no trust:

- **It talks to the internet.** `internal/registry/updates.go` and
  `notes.go` ask registries for digests and fetch changelogs: the most
  privileged process on the box, with the age key in reach, parses
  answers from outside. ADR 0025 already keeps the Operator, which reads
  the web, from acting; the kernel itself does the opposite.
- **It runs household verbs.** ADR 0026 put household verbs behind the
  door in `laib-haushalt`, yet `laib calendar`, `laib person` and
  `laib aufgabe` still live in `cmd/laib`.
- **It observes.** Much of `internal/diag` (devices, images, OS update,
  operator state, self-update) only reads and judges; it needs no secret.
- **It shows a second status UI.** `internal/pages` serves a Status page
  next to Homes' Box pages, which show the same readiness, load and
  findings (owner, 2026-11-04: Laib minimal, the everyday UI in Homes).

## Decision

Laib has three rings.

1. **Ring 0, the trust kernel (host, `laib`).** Only what decides about
   the owner's approval, keys and rights: the state repo, `secrets.age`,
   `policy.yml` with hands, tokens and the envelope, the inbox and its
   approval, `review` (what only a human may allow), apply with snapshot
   and rollback, the journal, the MCP door, and the pages where a human
   approves and enters keys (Inbox, Schlüssel). **The card text a human
   reads before approving stays here:** whoever writes it can mislead.
   Ring 0 never talks to the internet except to verify what it is about
   to run (a digest, a signature).
2. **Ring 1, Laib's helpers (containers, ADR 0027).** Programs that read
   and propose through the door with their own token, no age key, no state
   repo: the Operator, Notify, Haushalt, Lease, and three new ones moved
   out of ring 0:
   - **Updater**: asks registries, fetches changelogs and signatures,
     proposes new digests (ADR 0034). The kernel re-verifies digest and
     signature at apply; it never trusts the updater's word.
   - **Haushalt** gets `calendar`, `person` and `aufgabe` (ADR 0026).
   - **Observer**: the read-only findings of `diag`; the findings that
     need a secret stay in ring 0 and say why.
3. **Ring 2, the services.** Templates, as before.

**Homes is the one status UI.** The Status page leaves `laib serve`;
Laib's own pages keep Inbox and Schlüssel.

### What is rethought, not moved

- **A job with a frame instead of budget and hour windows (ADR 0032).** An
  owner-directed task over private data („gleiche die Google-Personen mit
  Immich ab“) is one card: which hand, which services, what it may read,
  what and how much it may change, until when. Approved once, bounded by
  its frame, journaled; not an Einblick per service plus a raised box-wide
  budget.
- **One card per Laib release for template updates**, in the family's
  words, and no card and no restart for a change that does not change the
  rendered unit (a comment, a README).
- **The Operator's log wishes are collected** and come as one card a week.
- **`laib-app register`** proposes the registry entry as an ordinary
  patch; its own box.yml writer goes.

## Consequences

- Ring 0 shrinks by roughly 2,400 lines to about 20,000; the cap is
  lowered with each move, never raised for a move.
- The kernel stops parsing registry and changelog answers. A compromised
  registry or changelog can at worst make the updater propose something
  the kernel then refuses on digest or signature.
- Each move is its own issue and PR, behaviour unchanged, vm-install green
  on main before the next.
- A household verb or an observation added later goes to ring 1 by
  default; ring 0 needs a reason in the PR.
