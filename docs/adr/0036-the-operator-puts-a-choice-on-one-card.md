# ADR 0036 — The Operator puts a choice on one card

Applies when the Operator (or any agent) cannot decide alone and the owner must
choose between ways on, when you touch `internal/proposal`'s card, `RunRepairs`
or the Inbox buttons.

- **Status:** Proposed, 2026-11-21 (draft for the owner's decision)
- **Deciders:** the owner (mdopp), on 2026-11-21: "der Operator sollte lernen,
  mir Optionen zu geben und in einer Karte freigegeben zu lassen … Problem,
  Lösungsansätze a, b, … und Konsequenzen. 'Think Harder' oder 'Werkstatt Ticket
  daraus machen' ist dann manchmal eine Option. Ablehnen wahrscheinlich
  seltener." and "B hilft mir mehr".

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
   (replacing `Restore` and `Forward`, which become two entries of it). A way
   has: a label (≤ 40 chars), what follows from it (one or two German
   sentences, the owner's words), and exactly one of
   - `Vorschlag: <id>`: an ordinary proposal already filed (a repair, a patch, a
     values change). Choosing the way approves it. Its own card does not show
     separately.
   - `Aktion: nachdenken`: the Operator runs the incident again with the
     strongest model it may use and more steps, and files a new card.
   - `Aktion: ticket`: the card becomes a Werkstatt task (`laib aufgabe`)
     with the problem and the ways as its text; nothing on the box changes.
   - `Aktion: warten`: leave it open, ask again at a time named in the way.
2. **Choosing one settles the rest.** The other ways' proposals are rejected
   with the reason "anderer Weg gewählt: <label>", so none stays open. A way
   that is an action settles the card as "weitergegeben". There is no way to
   approve two.
3. **The card reads the same every time.** The Operator fills a fixed form:
   *Problem* (what is wrong, what runs now), *Ursache* (known, or "nicht
   gemessen"), *Wege* with *Folge* each, *Empfehlung* (one way, and why).
   Whatever the Operator cannot measure it says so. Rejecting stays the card's
   ordinary "Nein"; it is not offered as a way, because it decides nothing.
4. **The Operator files it with one call**, `laib decide` (MCP `laib_wahl`,
   hand `ops`): `--problem`, `--ursache`, repeated `--weg label|folge|vorschlag-id`
   or `--weg label|folge|aktion`, `--empfehlung`. The vorfall and durchgang
   skills end in it instead of two `repair` calls when more than one way is
   open. A skill's prompt says when: two or more ways that exclude each other.
5. **The approvers are unchanged.** Box page, the admin's Signal, nobody else
   (ADR 0012). A way's proposal keeps its class: a human-only change stays
   human-only. `nachdenken` and `ticket` change nothing on the box, so they
   are class `apply`.

## Consequences

- The owner sees one card per problem and chooses with one tap; a way's
  consequence stands next to its button.
- The Operator has to name its recommendation, which makes a wrong one
  visible. Evaluation (`eval`) gets a case: a failed update with data must
  produce one card with the restore and the roll-forward as ways.
- Size, which is the real cost: the kernel is at 22,677 of 22,690 lines and the
  Operator at 3,299 of 3,300. The model (`Wahl`, settle the rest) and the verb
  are kernel code, about 150–200 production lines net of the two fields they
  replace and the button loop they generalise. This needs a cap raise (the
  owner's decision) or a cut found first. The form's wording and the skill
  prompts are skill text, not Operator Go.
- The Inbox page and Homes' `/box` draw the card; Homes needs the same list of
  ways (`homes` change, separate PR).
- Old cards with `Restore`/`Forward` keep working: read as two ways.

## Not decided here

- The strongest model for `nachdenken` and its budget (ADR 0011, the lease).
- Whether `warten` is wanted at all; it is the cheapest to leave out.
