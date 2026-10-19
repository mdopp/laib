---
name: a-measurement-carries-its-own-limits
description: "What a measurement does not carry is a field, not a subordinate clause. You are about to report a measurement, a probe result or a verify verdict — or you are reading one and deciding whether to act on it. Especially when a green result is about to close something and nobody has shown the check could have gone red."
---

# A measurement carries its own limits as fields

**Answer first:** a result that mentions its limits in prose has not stated them.
Prose invariants are advisory — the next reader skips them, and so does the next
agent. Make each limit a **field that has to be filled**. An unfillable field is
then visible at a glance, without anyone auditing the wording.

This is the same principle as CLAUDE.md's "deterministic execution → scripts",
applied to evidence instead of to mechanics.

Agreed across the operator's repos on 2026-08-30 (servicebay, solarisbay,
solaris-android, foundry-chronicle). The shared document uses the German field
names in brackets.

## The five fields

| Field | What goes in it |
|---|---|
| `quantity` (*groesse*) | The **named** quantity, never one derived from it |
| `window` (*fenster*) | Start and end, **computed, not typed** |
| `n` / `n_min` (*n / n_min*) | The denominator, and the minimum it must reach |
| `red_proof` (*rotprobe*) | The same probe run against the known-broken state, with its result |
| `not_established` (*nicht_belegt*) | What this measurement explicitly does **not** show |

A result missing any of them is an anecdote, not a measurement.

## Why each field exists — the incident behind it

Every one of these produced a **green reading on a demonstrably broken machine**
in a single session.

- **`quantity`.** The acceptance named one payload key; the probe averaged across
  *all* keys and came out red — pulled up by the one key that had deliberately
  been left alone. A threshold belongs to the denominator it was written for,
  never to a convenient aggregate of it.
- **`window`.** A timestamp source the parser silently rejected produced an empty
  string, so the filter matched nothing and "0 hits" was reported as proof.
  Compute the bounds and fail loudly when they cannot be computed.
- **`n` / `n_min`.** Zero hits over zero lines is not an absence. After a channel
  flip the log starts empty, so a probe must wait (bounded) for its denominator
  and treat "too few" as **RED, never green**.
- **`red_proof`.** Three probes in a row reported green on a defect that was
  plainly present — one of them because the transport encodes the very bytes it
  was searching for as an array rather than a string. **A probe is evidence only
  once it has failed on purpose.** Run it against the known-broken version first
  and require RED.
- **`not_established`.** A verify proved "the page answers 200", which is *near*
  but not equal to "the section behaves". Written as a field, that gap survives
  into the next decision; written as a closing sentence, it evaporates.

## The reopening rule

> A measurement may reopen a settled decision **only when the target state is in
> `quantity` and not in `not_established`.**

This replaces the softer "only if it measures the target state", which is a
matter of interpretation. This one can be checked by reading two fields.

## The counterpart: judgment outranks the number, but not without it

The architect's judgement may **discard** a measurement whose fields cannot be
filled — an unmeasured system is exactly where experience is worth more than a
number, and many capabilities are never exercised at scale.

It cuts both ways: against a measurement whose fields *are* filled, a dissenting
judgement must name **which field it disputes**. "That feels wrong" is a reason
to measure more precisely, not a reason to bypass the measurement.

## The other half: register the binding beforehand

The five fields make the limits structural **after** the fact. They say nothing
about which quantity should have been chosen. The companion mechanism, from
another house, closes that end:

> Before measuring, write down **which decision depends on which outcome**, what
> applies at each outcome, **and compared to what**. Whoever writes that down only
> after the measurement is interpreting the number instead of following it.

Their fallback triggered cleanly — on a measurement that answered the wrong
question, because only the outcome had been fixed in advance and not the
comparison quantity.

Both mechanisms are needed, at opposite ends: one registers the binding before,
the other makes the limits structural after. Neither is an exhortation.

**Evidence against one's own position, worth keeping in mind:** in the same
comparison, a house's architectural instinct was also wrong, and the numbers that
exposed its two real defects came from a **neighbouring service** evaluating its
journals. About itself it had none.

## Two things this does not replace

- **Recording a binding beforehand.** These fields make the limits structural
  *after* the fact; they say nothing about which quantity should have been chosen.
  Both mechanisms are needed, at opposite ends.
- **Mutation-checking the test itself.** `red_proof` is the probe's version;
  a test's version is deleting the load-bearing line, watching it go red, and
  restoring it. A test that passes before and after the fix is not a test.

## Where this bites in practice here

`assists/footgun-journal-conmon-is-not-a-second-emitter.md` is this checklist as
a concrete case: four measurement traps in one log transport, each of which hands
back a confident zero. Read it before measuring anything about a service's log
output.

---
Carried from ServiceBay's assist catalog (`assists/checklist-a-measurement-carries-its-own-limits.md` in mdopp/servicebay) when ServiceBay went off (laib #394). It is Laib's now: change it here.
