# ADR 0036 — The Operator puts a choice on one card

Applies when the Operator (or any agent) cannot decide alone and the owner must
choose between ways on, when you touch `internal/proposal`'s card, `RunRepairs`
or the Inbox buttons.

- **Status:** Accepted, 2026-11-21 (option B, a real choice card; the kernel line cap raised by at most 200)
- **Deciders:** the owner (mdopp), on 2026-11-21: "der Operator sollte lernen,
  mir Optionen zu geben und in einer Karte freigegeben zu lassen … Problem,
  Lösungsansätze a, b, … und Konsequenzen. 'Think Harder' oder 'Werkstatt Ticket
  daraus machen' ist dann manchmal eine Option. Ablehnen wahrscheinlich
  seltener." and "B hilft mir mehr"; decided the same day: build option B, the
  model for „nachdenken“ is the household model with more steps, no larger model.

## Context

On 2026-11-21 a Home Assistant update failed and the Operator left two
separate cards (#615 restore the backup, #616 roll forward). The owner had to
read two cards, work out that they exclude each other, and reject the one he
did not want. The Operator wrote what it did, not what the owner could choose.

The pieces exist for exactly two ways: a red run with data files two repair
proposals and writes their ids into `Proposal.Restore` and `Proposal.Forward`
(`internal/inbox/rollback.go`); the Inbox draws them as two buttons on the
run's card (`internal/pages/inbox.go`). Nothing else can offer a choice, the
labels are fixed, and the ways are always repairs.

## Decision

1. **One card, one problem, N ways.** A proposal can carry `Wahl []Weg`
   (replacing `Restore` and `Forward`: a red run with data offers its two ways
   as two entries of it). A way has a label (at most 40 characters), what
   follows from it (one or two German sentences, the owner's words) and the
   proposal that taps it. Every way is a proposal, so its approval is the
   choice and runs through the same approvers:
   - an ordinary proposal already filed (a repair, a patch, a values change);
   - **nachdenken**: a repair `laib wahl nachdenken <card>`. Approved, it
     opens the incident again for the Operator, with three times the steps and
     the same household model, and the Operator files a new card;
   - **ticket**, always the last: the card's own proposal, `laib wahl ticket
     <card>`, labelled „Als Aufgabe weitergeben“. Approved, it becomes a
     Box-Aufgabe for the admins with the problem and the ways as its text;
     nothing on the box changes. Laib adds it, the Operator does not list it.
   One to three ways come from the Operator, so a card has two to four. The
   ways' own proposals do not show as cards of their own while the card is
   open, and „Alle freigeben“ leaves them out. The card is a repair proposal
   of laib's with the problem as its headline, the cause as `ursache`, the
   ways and the recommendation as its facts, so every surface that shows a
   card shows them; the Inbox puts a button per way where Freigeben stood.
2. **Choosing one settles the rest.** Where an approval is handled
   (`internal/inbox`, so the page, the shell and the Signal relay alike), the
   other ways' proposals are rejected with the reason "anderer Weg gewählt:
   <label>" and the card is closed, so none stays open. A card that says no
   rejects every way. Only a card that is still open excludes: a red run's two
   ways stay both, because the restore is still wanted after a roll-forward
   that is red (#853).
3. **The card reads the same every time.** The Operator fills a fixed form:
   *Problem* (what is wrong, what runs now), *Ursache* (known, or "nicht
   gemessen"), *Wege* with *Folge* each, *Empfehlung* (one way, and why).
   Whatever the Operator cannot measure it says so. Rejecting stays the card's
   ordinary "Nein"; it is not offered as a way, because it decides nothing.
4. **The Operator files it with one call**, `laib wahl anlegen` (MCP
   `laib_wahl`, tier propose): `--problem`, `--ursache`, `--empfehlung` and the
   ways after `--`, each `"label|folge|<proposal id | nachdenken>"` (the form
   the typed MCP arguments already know). The vorfall skill ends in it
   instead of several `repair` calls when more than one way is open; its
   prompt says when: two or more ways that exclude each other.
5. **The approvers are unchanged.** Box page, the admin's Signal, nobody else
   (ADR 0012). A way's proposal keeps its class: a human-only change stays
   human-only. `nachdenken` and `ticket` are repairs (`Repairable`), run as
   the approver, and refuse any other hand.

## Consequences

- The owner sees one card per problem and chooses with one tap; a way's
  consequence stands next to its button.
- The Operator has to name its recommendation, which makes a wrong one
  visible. Evaluation (`eval`) gets a case: a failed update with data must
  produce one card with the restore and the roll-forward as ways.
- Size, which is the real cost: the kernel was at 22,677 of 22,690 lines and the
  Operator at 3,299 of 3,300. The model (`Wahl`, settle the rest) and the verb
  are kernel code; the owner allowed up to 200 lines more for the kernel and
  the few the Operator's step budget needs. The form's wording and the skill
  prompts are skill text, not Operator Go.
- The Inbox page and Homes' `/box` draw the card; Homes reads `wahl`, `ursache`
  and `empfehlung` from the proposal (`homes` change, separate PR).
- A red run's card filed before this ADR names its two ways as `restore` and
  `forward`, which nothing reads any more: its two proposals show as cards of
  their own until they are decided.

## Decided with it

- „nachdenken“ uses the household model (Qwen 35) with more steps; no larger
  model, so the GPU lease (ADR 0011) is unchanged.
- No „warten“ way: leaving a card open is what not tapping does.
