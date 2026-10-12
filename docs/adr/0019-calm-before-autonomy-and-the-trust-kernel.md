# ADR 0019 — Calm before autonomy, and the trust kernel

Applies when you want to grant an agent a new mutating verb, add a fourth
approval surface, let an image float, put a new component into the path
between the family and "alles läuft", or decide what the NUC trial has to
prove before the Kraftwerk moves.

- **Status:** Accepted, 2026-10-12 — the answer to the concept review
  (#49), proposal by proposal. Where a proposal is already code, the pull
  request is named; where it is the owner's call, it says so.
- **Deciders:** operator (mdopp), on the review's measure: good UX,
  resilience, agentic operation — for people who mostly do not think
  technically and are afraid of breaking something.

## Context

The review (#31, #49) found the concept right in its backbone — declared
state in Git, plan/apply/verify, a journal, systemd and Quadlet, data in
standard backends, humans deciding the irreversible — and thin where the
autonomy it grants rests on prose: the envelope failed open, a proposal's
class came from its shape, the Signal approval was a sentence in a prompt,
recovery needed a password no command showed, the box being down was not
a case, decisions were diffs, updates floated, and nothing proved what
happens after a power cut. It also found overshoot: twelve components and
four LLM agents for one household, an `exec`-capable operator on a small
local model, a software factory in the critical path, more rules described
than enforced, and a lease with modes × presets that was already silently
wrong.

## Decision

1. **Calm before autonomy.** The NUC trial's first phase is Laib + Caddy +
   Authelia + backup (on the box and off it) + the Haus start page, soaked
   with the fault injection the VM job already has (reboot, reinstall,
   restore, update) and one month of real use. The Operator starts as a
   **diagnostician**: `diag`, `inspect`, `logs`, `note`, `escalate`,
   `propose`. `write`, `exec` and `restart` stay inside the envelope and
   are granted per action type after the eval harness
   (`werkstatt/evals`, #82) shows no wrong mutating action on the incidents
   it holds. The Werkstatt, the autoloop and Signal approvals are the
   second phase, outside the trial's gate.
2. **The trust kernel** is five invariants, each with a test that tries to
   break it, and nothing else is promised:
   (a) no agent output reaches host execution — hooks and units come only
   from templates a human merged, hooks run in the hooks image, not on the
   host (#70);
   (b) a proposal's class comes from its content, not its shape (#66);
   (c) snapshots, budgets and the envelope fail closed (#67);
   (d) one lock with an owner, held from the first stop to the last verify
   (#73);
   (e) no LLM holds a token that can approve — the relay that sees the
   sender's number decides (#68).
3. **Three guarantees the family can see** on Haus's start page, in plain
   words: "Alles läuft" (or what does not, and that someone is on it) ·
   "Letzte Sicherung: heute 03:12, an zwei Orten" · "Letzte Änderung: … —
   Rückgängig". Rückgängig is `laib rollback #N` (#75); the fields come
   from `/ready` (#80, haus#4).
4. **Updates are proposals.** Images are pinned by tag and digest, an update
   is a proposal with a snapshot before, verify after, image and data
   rollback on red; backup and update never share the 03:00 slot (#77).
5. **An outside watchdog and a paper card.** `laib serve` pings an outside
   check while readiness is not red; silence becomes a push to the admin's
   phone; `laib family-card` prints "Wenn die Box nicht geht" and
   `laib recovery-sheet` the age key, the restic password, the repo and
   four steps (#79, #74).
6. **Decisions in plain language.** Every approval shows the consequence in
   one sentence, whether it is reversible, who asked and why, and "Später"
   first for risky classes; the diff sits behind "Details" (#78).
7. **Fewer agents in the critical path.** The Bote talks and relays, the
   Operator diagnoses; Frage, Automat, the Werkstatt, the lease's modes
   and the registry are optional and can be off without the family
   noticing. A component is in the critical path only if "alles läuft"
   depends on it; today that is Laib, Caddy, Authelia, the backup timer
   and Haus's start page.
8. **Hardware gates before the Kraftwerk.** The data disk is chosen by
   identity, never by size (#63); a reinstall from the stick keeps the
   data (#64) and the stick cannot write to an unchecked device (#65); the
   firewall and the registry login survive a reboot (#71); SMART and
   temperature are in `diag` (#83); the power-cut proof is #81 (open: the
   root filesystem after a hard reset during a reinstall's first boot).

Rejected from the review, with the reason:

- *Two lease modes only (household / exclusive).* The four presets exist
  because the Kraftwerk's consumers (Solaris, foundry-chronicle, pi) ask
  for different models today; what the review really found — stale unit
  names, no auth, the widest window by accident, silent starvation — is
  fixed (#84) without removing modes. Revisit when Solaris is gone.
- *Fewer ADRs.* The count is not the problem; unenforced ones are. The
  drift check (#87) and the trust-kernel tests are the enforcement; ADRs
  that describe more than the code does are amended, not deleted.

## Consequences

- For the NUC trial the owner sets `ops: {ceiling: propose}` in
  `policy.yml` (one line; the Operator then diagnoses and proposes) and
  raises it to `apply` when the eval harness clears the mutating verbs.
  `laib init` keeps writing `apply`, because the Kraftwerk's end state is
  ADR 0013; the grant stands, its *timing* changes. ADR 0013 is amended.
- A fourth approval surface is a design error; ADR 0012 stays: the Box
  page, the Signal relay (verified number) and Haus's `/box` all land in
  the same inbox, nothing else approves.
- The NUC trial's exit criteria are the five invariants green in CI, the
  three guarantees visible on the start page, a restore from the recovery
  sheet, and a month without a red the family noticed first.
- What is still prose: the public-route rule ("always a human's call")
  and the webhook that wakes the Operator (#87 marks both).
