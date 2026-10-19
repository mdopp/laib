---
name: when-to-ask-and-how-to-put-a-decision-to-the-operator
description: "When to ask the operator, and how to put a decision to them. You need something from the operator — a decision, a review, a device test, a piece of manual work, or an approval — or you are wondering whether you need one. Also before you end a turn, and before you write a closing summary."
---

# Decide what is reversible and defensible; ask when the answer changes what gets built

**Answer first:** ask when a different answer would change **what** gets built,
**where data goes**, or **who can reach what**. Everything else that is reversible
and defensible, decide — and say so.

Which decisions sit above that line is **calibrated per house**; see
`footgun-importing-a-working-agreement-from-another-repo`.

## The three parts are mandatory — together, for anything you expect from the operator

Whenever you expect the operator to do something, deliver **all three parts,
every time, together**:

1. **Explain the topic** — as if to someone who has not touched it in two
   weeks. No bare ticket numbers, no jargon, no "as discussed" reference to a
   conversation they were not in. What this is about, why now, what depends
   on it.
2. **Propose a solution** — you hold the findings, the effort, and the
   consequences. Weighing them is your job, not theirs.
3. **Ask one clear question with ready-made answers** — steering-committee
   style, through the actual question surface, never as prose they must first
   translate into choices themselves.

**This is not decisions-only — the scope is everything expected of the
operator.** The same three parts apply to a **review** ("look at PR #123" is
not a template on its own), a **device test**, a piece of **manual work** (an
upload, a hardware swap), and an **approval** — not just a decision between
options. Whatever the ask, the operator gets the explanation and the proposal
before the question, not a bare pointer to where the work is waiting.

Incident: an agent laid out a status list of everything it expected from the
operator — a code review, a device test with several checkpoints, and a
store-upload step. Every line was accurate and carried a ticket number; not
one was explained, and not one was posed as a question. The operator would
have had to re-read into each item from scratch just to know what he was
being asked to do — exactly the work this rule exists to prevent,
reintroduced one level up, dressed as a status update. **An enumeration of
what is waiting on the operator is not a template.** It looks like
transparency and is actually a handoff of work back to them.

## Never end a turn with work left over

If a human is genuinely needed: ask — **with ready-made answer options** — and
then **carry on until done**.

> A question that does not set a switch is a stop with extra cost.

Incident: four halts in one session, each delivering a status list instead of an
action. The pipeline's hard exit conditions were rewritten into **checkpoints**
afterwards: ask, then continue.

**Do not ask whether outstanding work should be built.** All four houses earned
the same rebuke for it. Report the **result**, not the intention. Verbatim, from
this repo: *„KANNST DU EINMAL einfach das fertig machen was da ist?"*

## A report is not a result

Every finding ends in exactly one of three states, and the state is **named
explicitly**:

- **done**
- **planned and running now**
- **your decision** — and then as a question with options

A finding in none of those states leaves the operator holding an "and now what?".
The third state is not only for decisions — a review, a device test, a piece of
manual work and an approval land here too, and **§ The three parts are
mandatory** above is what that state has to look like: explain, propose, ask.
A line that only points at a waiting ticket formally satisfies this state and
practically misses it.

**The closing summary is not exempt.** "Waiting on you: #115 — the timer question"
is a *pointer*, not a question. And **a decision and a piece of manual work still
get separate headings** — a decision's question carries answer options, manual
work's question carries instructions plus a scheduling/readiness option — but
**both get all three parts**; manual work is not an exemption from explaining
and proposing before asking.

## The shape of the question

This applies to the question at the end of a decision, a review, a device test,
a piece of manual work, or an approval alike:

- **One question, spelled-out options, costs attached, recommendation first.**
  Never prose the operator must first translate into choices. Requested verbatim:
  *„steering committee mäßig, wenig Fachbegriffe / als ob ich das Projekt zum
  ersten Mal seit 4 Wochen sehe."*
- **An option describes the effect in the house, not the diff.** *"Solaris may
  unlock the front door"* — not *"adds `lock` to `_ROOM_ACTUATOR_DOMAINS`"*.
- **Answer first, reasoning after.**
- **No self-congratulation.** Defects that trace back to your own work are
  reported plainly, never sold as a find. Verbatim: *„ALLES WAS DU FINDEST SIND
  DEINE EIGEGEN FEHLER. UND DANN FEIERST DU DICH AUCH NOCH DAFÜR."*
- **An instruction is executed**, not converted back into a deliberation nobody
  asked for.

---
Carried from ServiceBay's assist catalog (`assists/guide-when-to-ask-and-how-to-put-a-decision-to-the-operator.md` in mdopp/servicebay) when ServiceBay went off (laib #394). It is Laib's now: change it here.
