# ADR 0014 — The Operator, Solaris and Haus are three programs

Applies when you are about to add a page to the agent, an agent to a page,
or box administration to the household assistant.

- **Status:** Accepted, 2026-10-10. §2 (Solaris as the household agent)
  superseded by ADR 0015, which retired Solaris; the household's agent
  you talk to is the Bote (ADR 0016). §1, §3 and §4 stand.
- **Deciders:** operator (mdopp): "Der Laib Operator sollte nicht in
  Solaris leben."

## Context

Solaris carried the household agent, the household pages, the phone's
companion API, box administration and the model lease in one process and
one 7,000-line file. The phone app is mostly native cards, and the chat is
rarely used; what is used is switching and seeing state.

## Decision

1. **Laib Operator** is a separate binary and template in the Laib repo:
   the box's admin agent, ADR 0013's hand `ops`. Its memory is the journal
   and an incidents file; it has no voice and no household tools.
2. **Solaris** is the household agent only: turn loop, memory, tools,
   identity, timers. Two entry points, `/v1/turn` and `/v1/events`. It
   renders nothing.
3. **Haus** is the surface: pages and cards, the companion API, push and
   device tokens, aggregation of Home Assistant, Laib and Solaris. The
   Android app is Haus's TWA plus native widgets.
4. Cards are one component with one renderer, served as JSON to the
   widgets and as HTML to the pages; the chat reply is a card too.

## Consequences

- `docs/DEKONSTRUKTION.md` is the plan; steps 1 and 2 are Laib issues.
- A feature that needs a page, an agent and box access touches three
  repos on purpose; the seams are the two Solaris routes, the Laib MCP and
  the `/napi` contract.
