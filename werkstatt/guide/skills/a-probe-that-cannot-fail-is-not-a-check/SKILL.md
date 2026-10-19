---
name: a-probe-that-cannot-fail-is-not-a-check
description: "A probe that cannot fail is not a check. You are writing a health check, a test, a probe or any automated check that will report green or red — or you are about to trust one that reported green. Also when a check has been green for a long time."
---

# Green means nothing until the check has failed on purpose

**Answer first:** the most-travelled sentence of a four-repo comparison:

> **A service asked about itself reliably reports that it is fine.**

Written by one house about another's defect, then found verbatim two days later in
its *own* code: `return web.json_response({"ok": True})`, unconditional, green for
over a day while three subsystems logged errors every minute. It was noticed by a
human holding a phone.

## The checks

- **A probe is only evidence once it has gone red against the broken version.**
  Mutation proof: delete the load-bearing line, watch it go red, restore it.
- **Minimum denominator.** Zero hits over zero lines is not an absence. Falling
  below the minimum is **RED**, never green.
- **The threshold belongs to the denominator it was written for**, never to a
  convenient aggregate of it.
- **Three states wherever a display can read something.** "Empty" and "the read
  broke" must not look the same. A field whose truth is unknown is `null` →
  "unknown", **never `false`**.
  - Independently found in another house: a disconnected tile painted the
    *absence of data* as "lamp off", because the accent colour came from
    `card?.isOn == true`, which yields `false` with no card. The tile did not
    claim "off" — it **painted ignorance as a state**. An unmarked display leaves
    you uncertain; a wrongly coloured one saddles the user with an untruth.
- **Read at the receiver, not in your own log.** Incident: a counter looked green
  and had never counted anything — the service required a field that was not being
  sent, every POST returned 400, and the fail-open path swallowed it. Otherwise the
  test checks your own assumption against itself.
- **An answer produced before the target is not the target's answer.** Redirects,
  interstitials and auth gates answer **for** a service, not **as** it. To check the
  service, read something only it can produce.
  - Incident: a new SSO-gated route was declared working because `https://…` returned
    `302` to the login page. That redirect is emitted *before* the upstream is
    contacted — it proved the route and the gate, and said nothing about the service.
    The issue was closed; the operator opened the page and got a `502`. The upstream
    port was not published by the running pod at all.
  - What makes this one worth keeping: **there was nothing to notice.** The proxy
    behaved correctly, the status code was real, nothing anywhere misbehaved. The
    answered question was simply not the asked one. Every other entry here is about
    something broken; this one is about everything working.

- **A test can end up guarding the bug instead of the behaviour.** Incident: a test
  pinned a private volume label verbatim — the very thing that had caused the
  outage. It would have blocked the fix.
- **For anything destructive, test against the sibling, not against itself.** A
  revoke that takes siblings with it passes every single-object test. Incident
  here: window names resolve by exact match *and then by prefix*, so an unanchored
  target would have destroyed a neighbouring session **and reported success**.
- **Deterministic steps belong in a script**, not in prose a model reinterprets
  each run. And whatever must be undone is undone **structurally**: a channel
  flip-back placed in a `finally` kept the installation from getting stuck on a day
  when five verifies failed in five different ways.

## Related

For reporting the *result* of a measurement, see
`checklist-a-measurement-carries-its-own-limits`. For the traps specific to
reading a service's log output, see
`footgun-journal-conmon-is-not-a-second-emitter`.

---
Carried from ServiceBay's assist catalog (`assists/checklist-a-probe-that-cannot-fail-is-not-a-check.md` in mdopp/servicebay) when ServiceBay went off (laib #394). It is Laib's now: change it here.
