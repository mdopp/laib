# ADR 0012 — One approval surface; the host shell is the human; pages trust only the proxy

Applies when you add a button to a Solaris card, read `LAIB_ACTOR` for a
permission decision, or expose a Laib page or listener anywhere.

- **Status:** Accepted, 2026-10-09

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
   on Signal, where the Bote uses a `human` token bound to that number.
   Laib never accepts an approval carried by an agent's own token.
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
