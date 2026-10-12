# ADR 0016 — The Bote: the agent you talk to, on Signal, built from Hermes as stock

Applies when you add a conversation channel, a chat page, a tool the
family talks to, or consider forking Hermes.

- **Status:** Accepted, 2026-10-10
- **Deciders:** operator (mdopp): "Ich möchte ja auch mit Signal mit ihm
  reden können."

## Context

ADR 0015 removed the LLM from the voice path and retired Solaris the
agent. That left the claim "the only agent conversation is Claude in the
Werkstatt", which was wrong: the admin wants to talk with the house's
agent on the road, exchange, ask, approve. ADR 0009 declined Hermes
because of the voice path (prefill, latency, no Wyoming); those reasons
are gone, and its strengths — messaging gateways, HA integration, MCP
client, memory, cron — are exactly what a text agent needs.

## Decision

1. **The Bote** is the agent people talk to: Signal first, Matrix and
   Telegram when wanted, and the Sprechen field in Haus. It knows the
   house (HA), the box (Laib MCP as hand `bote`, read + propose), questions
   (Frage), the household memory (a Haus tool), and reminders (cron).
2. **Hermes, as stock.** No fork, no sidecar, no trace proxy. A template
   configures gateways, tools and prompts. The June lesson stands as a
   threshold: when the Bote needs more than a few hundred lines of glue,
   it is rebuilt lean; the first sign of a workaround stack is the signal.
3. **Approval by verified number.** A message from the admin's Signal
   number is the admin. The Bote holds a `human` token bound to that
   number and uses it only to approve or reject proposals on the admin's
   word; it never approves on its own hand. Amends ADR 0012: two approval
   surfaces, the Box page and the verified Signal number.
4. **Models.** The Bote is not latency-critical and may request the
   `thinking` preset through the lease for hard questions; the household
   preset is its default.
5. Family members may each get the Bote on their own number with their
   own memory namespace; the house yes, the box no. Default: the admin.

## Consequences

- Haus drops the Verlauf page; the morning report and escalation answers
  reach the admin through the Bote.
- ADR 0009 is superseded for the text case and stays for voice.
- ADR 0015 §5 is corrected: Hermes has one job, the Bote.

## Amendment 2026-10-11 (#48): approval by verified number is code, not prose

Hermes holds no token that can approve. The `bote` hand reads, proposes,
notes and escalates (its MCP entry is `trust: untrusted`, so proposing
asks the person in the chat). "gib #N frei" / "lehne #N ab" are handled
by the **relay** (`cmd/relay`, container `bote-relay` in the pod): it
subscribes to signal-cli's event stream, acts only when the sender is
`ADMIN_NUMBER`, looks the proposal up, decides through Laib with the
`approver` hand — `human: true`, `verbs: [approve, reject, inbox, status,
note]` — and answers with the consequence. Another number trying it gets a
journal line. The Automat executes writes and deletes only with the code
its first answer named, and keeps deleted automations for restore.
