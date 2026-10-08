# ADR 0007 — Caddy replaces Nginx Proxy Manager; the Caddyfile is rendered from state

Applies when you touch routes, certificates, forward-auth, LAN-only access or
WebSocket timeouts.

- **Status:** Accepted, 2026-10-08

## Context

NPM kept its own SQLite state behind an admin API; ServiceBay grew ten copies
of the client for it, a rekey path, and several diagnose probes for drift
between its config and NPM's. The operator's rule is "public certificates, no
public reachability", which needs DNS-01.

## Decision

1. Caddy, as a Quadlet service with role `proxy` in `box.yml`. One Caddyfile
   rendered from every `services/*/laib.yml` route.
2. TLS via Let's Encrypt with DNS-01 through a pinned Caddy image that
   carries the DNS provider plugin.
3. Authelia via Caddy's `forward_auth`. LAN-only is a `remote_ip` matcher.
   NPM's "block exploits" is dropped. Per-route extras go into the `caddy:`
   block of the route.
4. Authelia's own rules (apex deny, wildcard one_factor; ServiceBay ADR 0005
   and 0006) are unchanged; only the wiring moves.

## Consequences

- No admin API, no SQLite, no proxy drift probes.
- The DNS provider becomes a declared dependency of the box (`box.yml`).
