# ADR 0009 — Hermes is not adopted; the threshold under which it would be

Applies when someone suggests Hermes, OpenClaw, or any second agent runtime
beside Solaris.

- **Status:** Accepted for the voice path; superseded for text by ADR 0016 (2026-10-10)

## Context

Solaris replaced Hermes outright in 2026-06: 12.7k-token prefill with two
thirds tool definitions, and a workaround stack that grew with every feature.
The one thing Hermes would add today is its twenty messaging gateways. Solaris
already has cron, memory, skills, an MCP client, a browser chat behind SSO and
an Android app.

## Decision

Hermes is not part of Laib or Solaris. It would be considered only as a pure
transport (no memory, no tools, no model of its own) for a messaging channel
that is a hard requirement and would otherwise cost more than a weekend. It is
removed the moment it keeps a second memory.

## Consequences

The road channel stays the SSO route and the Android app. A Signal bridge, if
ever needed, is evaluated as a small bridge first.
