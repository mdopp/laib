# ADR 0019 — Calm before autonomy, and the trust kernel

Applies when you want to grant an agent a new mutating verb, add a fourth
approval surface, let an image float, put a new component into the path
between the family and "alles läuft", or decide what the NUC trial has to
prove before the Kraftwerk moves.

- **Status:** Accepted, 2026-10-12 — the answer to the concept review
  (#49), proposal by proposal. Where a proposal is already code, the pull
  request is named; where it is the owner's call, it says so.
  Amended 2026-10-17 (#286): a capable model may run `apply` itself,
  inside the envelope — see the amendment under Consequences.
  Amended 2026-10-23 (#609): an approve is a merge, a run applies what
  was approved, batched, and rolls it back as a whole on red.
  Amended 2026-10-26 (#778): the relay is gone, approvals are the Inbox's.
  Amended 2026-10-28 (#865): (e) says what the code does — agent hands
  approve what is not human-class — and §3's guarantees sit on /box, with
  one calm line on Start (haus#35).
  Amended 2026-10-28 (#853): a red run rolls back the program, not the
  data; the snapshot is offered back, never restored by itself.
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
   (e) no token approves what only a human may: a proposal of class
   `human` or `remove`, the `destroy` tier, or a tier policy.yml gives
   `approvals … by: human` (`policy.NeedsHuman`). Those only a human
   approves, in the Inbox or on the host shell. An agent hand whose ceiling
   holds `approve` (ops, claude) may approve any other proposal that is not
   its own. *(Amended 2026-10-26, #778, and 2026-10-28, #865: see the
   amendments below.)*
3. **Three guarantees the family can see** in plain words, on Haus's
   `/box` *(amended 2026-10-28, #865: Start shows one calm line, haus#35)*: "Alles läuft" (or what does not, and that someone is on it) ·
   "Letzte Sicherung: heute 03:12, an zwei Orten" · "Letzte Änderung: … —
   Rückgängig". Rückgängig is `laib rollback #N` (#75); the fields come
   from `/ready` (#80, haus#4).
4. **Updates are proposals.** Images are pinned by tag and digest, an update
   is a proposal with a snapshot before, verify after, image rollback on
   red and the data's snapshot offered back, never restored by itself;
   backup and update never share the 03:00 slot (#77). *(Amended
   2026-10-28, #853: see the amendment below.)*
5. **An outside watchdog and a paper card.** `laib serve` (since #443 its
   program `laib-notify`, on a five-minute timer) pings an outside
   check while readiness is not red; silence becomes a push to the admin's
   phone; `laib recovery-sheet` prints the age key, the restic password, the repo and
   four steps (#79, #74). The family card (`laib family-card`) left the
   kernel with #798.
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
  *Revisited 2026-10-14 (#157):* the windows stay. They are named for
  what they do to the card (`shared`, `full`, `exclusive`), and consumers
  ask for a preset (ADR 0011, amendment 2026-10-14).
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
  page and Haus's `/box` land in the same inbox, nothing else approves
  (the Signal relay was the third until #778 removed it).
- The NUC trial's exit criteria are the five invariants green in CI, the
  three guarantees visible on Haus (`/box`; one calm line on Start), a restore from the recovery
  sheet, and a month without a red the family noticed first.
- What is still prose: the public-route rule ("always a human's call")
  and the webhook that wakes the Operator (#87 marks both).

## Amendment 2026-10-17 — a capable model applies its own fix (#286)

The owner (mdopp), on #286: **"a capable model may run `apply` itself,
within the budget — the hourly envelope, snapshot first, automatic
rollback on red."** Chosen over keeping `apply` a human's or the dial's
alone, because Qwen 3.x on the Kraftwerk diagnoses well enough that
waiting for a human on a one-value fix costs the family more than the
envelope risks.

What the kernel does, never the prompt (`cmd/laib` `opsApply`):

- the ops hand's `apply` must name **its own open proposal**
  (`laib apply --proposal <id>`); a whole-box or bare service apply by the
  Operator is refused;
- a proposal **only a human may free** (class `human`, `remove`, the
  `destroy` tier, `approvals … by: human`) is refused, whatever the dial;
- every service the proposal touches passes the **envelope** first: the
  hourly budget (`RepairBudget`, which now also counts the Operator's own
  applies), and the pre-repair **snapshot**, failing closed;
- then merge, apply, verify, and **revert and re-apply on red** — the
  dial's auto-approve machinery; the journal books the apply against each
  service's budget.

Which model may: the Operator offers `apply` only to a capable model (a
skill's `more:` tools, ADR 0023 amendment); a small model never sees it.
That choice is the Operator's; the limits above hold whatever it sends.


## Amendment 2026-10-23 — approving is a merge, the run applies (#609)

The owner: „Aber warum muss die Sperre gehalten werden und blockiert das
Interface gegen weitere Freigabe.“ An approve held the one lock (d) from
the snapshot through the merge to the last verify, so a tap behind a nine
minute apply waited nine minutes before it was even recorded.

- **An approve is the merge.** It runs every check it ran before — the
  state loads, the own-proposal rule, the human class, the second factor
  of a web decision — under the state repo's own short lock, never the
  box's, and the proposal is `approved` (outcome `waiting`) at once.
- **The run applies.** One worker (`inbox.Carry`) takes the one lock and
  applies `main` with everything approved meanwhile; each release of the
  lock starts it again, so N approvals during one apply give one more
  apply. Invariant (d) holds for the run unchanged; the approve is no
  longer a holder.
- **The snapshot (#528) moves to the run**, right before it touches the
  services with data, and still fails closed: a failed snapshot leaves the
  box as it was and rolls the run's merges back.
- **A red run is rolled back as a whole**, each proposal marked
  `rolled-back` with the run named, in the Inbox and the journal; one that
  should go alone is proposed again. The dial's auto-approve is the same
  run: "merge, apply, verify, revert and re-apply on red" now means the
  run's merges, not one proposal's.
- The worker lives in the `laib` binary, not in a unit: the CLI, the Signal
  relay (through `laib approve`), MCP and `laib serve` reach it the same
  way, and the shell keeps seeing its apply when the lock is free.

## Amendment 2026-10-26 (#778): invariant (e) without the relay

Owner decision 2026-10-26 („B3“, #751): approvals only in the Inbox. (e)
now reads: **no token approves what only a human may, and only the human
approves at all** — the Inbox (`human:<Remote-User>` behind Authelia's
second factor) and the host shell. The relay, the trust kernel's one
program outside `laib`, is removed with its `approver` hand; `IsHuman` is
the hand `human` and nothing else, and a `human: true` left in an old
policy.yml counts for nothing (`TestApprovalsAreTheInboxsAlone`). The run
worker of the amendment above is reached by the CLI, MCP and `laib serve`.
*„Only the human approves at all“ is superseded by the amendment of
2026-10-28 (#865): agent hands approve what is not human-class.*

## Amendment 2026-10-28 (#865): (e) as the code has it, and where the guarantees sit

The review of 2026-10-28 (#851) found the 2026-10-26 text, „only the human
approves at all“, stricter than the code: `inbox.Decide` lets a hand whose
ceiling allows the proposal's tier approve it unless `policy.NeedsHuman`
says a human must, and `proposal.ApproveChecked` refuses a hand its own
proposal. The owner, on #865: **yes, agent hands may approve proposals
that are not human-class, as the code does today.** So (e) reads as in the
Decision above:

- no token approves a `human` or `remove` class, the `destroy` tier, or a
  tier with `approvals … by: human`; only `human:<Remote-User>` in the Inbox
  or the host shell does (`TestAScriptForTheHostWaitsForAHuman`,
  `TestApprovalsAreTheInboxsAlone`);
- ops and claude may approve any other proposal but their own; the bote
  hand, ceiling `propose`, approves nothing
  (`TestAnAgentHandApprovesWhatIsNotHumanClass`).

§3: the owner decided on haus#35 (2026-10-15) that Start carries one calm
line at the bottom, which moves to the top when something is wrong, and
the three guarantees in their admin wording sit on `/box`. The guarantees
are unchanged; only their place is.

## Amendment 2026-10-28 (#853): on red the program comes back, the data are offered back

§4 promised „image *and data* rollback on red“. The run reverted its merges
and applied again, but never restored the snapshot it took, so after a
database-migrating update the old program started on migrated data while the
card said „Daten bleiben“. Owner decision 2026-10-28: (b), plus a way
forward; (a), an automatic restore, waits for a green restore drill (#438).

- **No automatic restore.** A red run brings the old program back and leaves
  the data as the update left them.
- **It says so.** Each proposal of the run reads „Das alte Programm läuft
  wieder. Die Daten sind auf dem Stand nach dem Update; die Sicherung von
  HH:MM liegt bereit.“, with the snapshot's real time; the journal says the
  same.
- **Two ways on, one tap each**, both proposals laib files itself and the
  human approves in the Inbox (no new approval surface, no new verb):
  „Sicherung zurückspielen“ runs `laib restore <snapshot> <service>`;
  „Nach vorne reparieren“ runs `laib rollback` of the run's revert, so the
  new version is on again and the data are not touched. A forward that is
  still red leaves its red journal line, the Operator's incident (#792:
  bounded, escalated after 30 minutes); the restore stays open beside it.
