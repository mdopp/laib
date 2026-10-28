# ADR 0012 — One approval surface; the host shell is the human; pages trust only the proxy

Applies when you add a button to a Solaris card, read `LAIB_ACTOR` for a
permission decision, or expose a Laib page or listener anywhere.

- **Status:** Accepted, 2026-10-09; confirmed by ADR 0019 (2026-10-12): the Box page, the Signal relay and Haus `/box` are one inbox, and no LLM holds a token that can approve (#68). Amended 2026-10-26 (#778): the relay is gone; the Inbox is the only approval surface. Amended 2026-10-28 (#865, ADR 0019 (e)): no token approves what only a human may; the agent hands ops and claude may approve any other proposal but their own.

## Context

Three gaps found on 2026-10-09: (a) a Solaris card with "Freigeben" needs a
human identity Laib can verify, but Solaris holds the `ops` hand, which may
never approve; (b) `LAIB_ACTOR` is an environment variable, so anyone with a
shell as `core` can claim any hand; (c) the pages read `Remote-User`, which
any process on the box could forge against the loopback listener, and pods
cannot reach a loopback listener at all for `/mcp`.

## Decision

1. **Approval surfaces: the box page and the admin's verified Signal number
   (amended by ADR 0016).** A card links to `laib.<domain>/inbox`; the
   decision is made there behind Authelia, or by the admin's own message
   on Signal, which the relay acts on with its `approver` token (a `human`
   hand that can only decide). *(Corrected 2026-10-14, #184: this said the
   Bote used a `human` token; since #48 the relay holds the approver token
   and Hermes none that can approve.)* Laib never accepts an approval
   carried by an agent's own token.
   *(Amended 2026-10-14, #184: the relay also accepts the admin's Matrix
   ID in one room; see below.)*
2. **The host shell is the human.** Hands are enforced only for MCP bearer
   tokens. `LAIB_ACTOR` on the CLI is attribution, not authorization: whoever
   runs the binary as `core` is a human. Therefore no agent gets a shell on
   the host; the Werkstatt is a container and speaks MCP.
3. **Pages trust only the proxy.** Caddy adds `X-Laib-Proxy: <secret>` on
   the `laib.<domain>` route (secret `LAIB_PROXY_SECRET`, generated on first
   apply, rendered into the Caddyfile). The pages and the Inbox buttons
   refuse requests without it. Until a proxy role exists, the pages are open
   only on loopback.
4. **Listener.** `box.yml laib.listen` defaults to `127.0.0.1`. Setting it to
   `0.0.0.0` lets pods reach `/mcp` via `host.containers.internal`; the
   nftables rule then drops the port on physical interfaces, like any
   `lan: false` port. `/mcp` is bearer-only and `/ready` is harmless on any
   interface.

## Consequences

- `docs/DESIGN.md` no longer speaks of "two human surfaces"; Solaris notifies
  and links.
- The Caddy route, the Authelia rule (`group:admins`), the nftables line and
  the proxy secret are rendered from `box.yml laib`, so a renamed host or
  port stays consistent.

## Amendment 2026-10-14 (#184): the relay's second identity

The relay is still one approval surface; ADR 0019 calls a fourth one a
design error. It now accepts two verified identities: the admin's Signal
number, and the admin's Matrix ID in one unencrypted room on the Bote's
own homeserver. What the Matrix identity rests on, and what it does not,
is in ADR 0016's amendment of the same date. Telegram never approves. A
Matrix that fails its check is refused on its own; the relay, its Signal
path and the Box page stay up.

## Amendment 2026-10-26 (#778): the Inbox alone

Owner decision 2026-10-26 („B3“, #751): approvals happen only in the Inbox
(`laib.<domain>/inbox`, behind Authelia's second factor). The relay
(`cmd/relay`, `internal/relay`, the `bote-relay` container and its image),
the `approver` hand, the Signal poll and 👍/👎 votes (#509) and the verdicts
on Matrix and Discord (#184, #463) are removed. §1 now reads: **one approval
surface, the Box page's Inbox**; a card or a chat message links to it, and
nothing else decides. The host shell (§2) stays the human it always was.

- The chat is told, not asked: laib-notify sends a new proposal as one line,
  „Freigabe #N: <headline> · <Inbox link>“, on the owner's channel, with the
  same line as the mail's subject and body when no channel answers.
- Only the hand `human` counts as a human (`policy.IsHuman`). A policy.yml
  from before still loads with its `approver: {…, human: true}` line (the
  loader ignores the unknown key); the mark counts for nothing, so that
  token can no longer approve what only a human may. The hand itself is the
  owner's to remove, with a policy proposal; the default policy no longer
  has it and the defaults sync (#575) never adds it back.
- Rollback: an older release still renders its own bote template with the
  relay and its `laib-relay:latest` image, which stays on ghcr; the box's
  bote template changes only with a template update the owner approves.
