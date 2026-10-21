# ADR 0025 — The Operator reads the web through the box's search, and never acts on it

Applies when you are about to give the Operator (or any agent of the box)
access to the internet, add a tool that fetches pages, wonder where an
Operator session may look up an unknown error, a changelog or an upstream
issue, or decide how a proposal that rests on something read on the web
reaches the owner.

- **Status:** Accepted, 2026-10-21 (#544)
- **Deciders:** the owner (mdopp), on #544: the Operator should "search the
  web from good entry points, as reading, never as acting".

## Context

The Operator had no web. For an unknown error it could not read the
upstream's docs, release notes or issues; the Werkstatt could (Claude
Code, pi), but nothing connected the two. The box already runs a search:
Frage (`templates/frage`) is SearXNG plus a model summary, for Assist and
the Bote. Its SearXNG listens on the pod's loopback only (`127.0.0.1:8888`
inside the `frage` pod); the pod publishes Frage's own port, 8710, to the
host and to pods (`lan: false`).

Page text from the web is the widest door for prompt injection the box
has. A model that reads a forum post and may restart, exec or apply in the
same session is a model a stranger can steer.

## Decision

1. **One search, the box's own.** The Operator reads the web only through
   Frage's `/search` — SearXNG's hits, no summary, no model — at
   `FRAGE_URL`, which the kernel renders into the Operator's unit when a
   service provides `frage`. No new service, no page fetch, no second
   search engine. Without Frage, the Operator has no web.
2. **Entry points first, as data.** A search scopes to the service's own
   upstream first — `upstream:` in its laib.yml, else its template's (repo,
   release notes, docs) — then to the general entry points in
   `operator/websources.json` (Home Assistant, podman, systemd, Fedora
   CoreOS, uCore), and asks the open web only when those give fewer than
   three hits. The lists are data, never prompt text.
3. **A narrow tool of the Operator, not a kernel verb.** `websuche`
   (`frage`, `dienst?`) lives in `operator/`; the kernel's verbs and its
   trust kernel do not change. Its answer is excerpts (300 characters) with
   their address, labelled as foreign text — data, never instructions.
4. **Offered only where nothing acts.** The runner offers it in a session
   whose output is `propose`, that may `propose`, and that is granted none
   of `apply`, `restart`, `exec`, `write` — the session can only end in a
   proposal a person reads. `vorfall` (which may restart) and every
   looking or reporting skill never see it.
5. **A proposal that rests on the web says so and is a human's.** Every
   proposal filed after a search carries `sources` (the addresses the
   model names that the search returned, else all of them, at most ten);
   the kernel makes any proposal with a source class `human` and lists the
   addresses among its reasons.
6. **A budget, journaled.** At most four requests and 64 KiB of answers per
   session; the session's note — a journal line — says how many requests,
   how many bytes and which addresses.

### Weighed against before (#246)

- **Maintenance gets easier:** an unknown error or a patch version comes
  with the upstream's own words and address in the proposal, instead of
  the owner looking it up; the entry points are one data file and one
  line per template.
- **Nothing is worse for the family:** nothing the web says is applied
  without the owner; the session that reads it can change nothing; no new
  service runs. What it costs: about 250 Operator lines, a `/search`
  handler in Frage, one kernel line in the Operator's unit and the
  `upstream` field.
- **ServiceBay** had no web for its agents; its assist catalog was curated
  know-how a human wrote. This adds reading, not knowledge: what was read
  reaches the box only through a proposal the owner approves.

### Rejected

- *Publishing SearXNG's port* — a second port for one search, and Frage's
  `/search` is the same SearXNG.
- *Fetching pages* — the widest injection surface for the least gain over
  SearXNG's excerpts; revisit when a measured case needs a page's text.
- *A kernel verb `laib web`* — every hand would see it; the web is the
  Operator's tool for proposals, and the kernel need not know it.
- *The web in `vorfall`* — it may restart; reading and acting stay apart.

## Consequences

- `templates/*/laib.yml` gain `upstream:`; a service added before keeps
  working (laib context reads its template's).
- The eval incidents can script `websuche` like any tool; a real-model run
  with injected excerpts is the measure of point 3.
- The web never reaches `laib apply`: the trust kernel (ADR 0019 §2) is
  unchanged, and ADR 0023's skill rights stay the kernel's.
