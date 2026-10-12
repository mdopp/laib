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

## Amendment 2026-10-11 (#45): the trial gets real certificates, `tls: internal` is for laptops only

Android apps trust only system CAs unless they opt in; `mdopp/solaris-android`
has no network security config and no custom trust manager. A trial on
`tls: internal` therefore cannot pass the Phase 3 gate ("the widgets work")
without asking the family to install a root CA, which is the opposite of
fear-free. Decision: **(a) from the issue** — the trial box gets a domain
whose zone lives at a provider with a DNS API. `dopp.cloud` stays at Strato;
Strato can delegate a sub-zone by NS record, so the trial domain is
`nuc.dopp.cloud` delegated to deSEC (free, API, in the Caddy image). Caddy
then does DNS-01 natively (`acme_dns desec`), one certificate per host, no
override domain, no CNAME per host. The stick asks for the provider and its
token (`--dns-provider`, `--dns-token`), first boot sets the secret
`DNS_API_TOKEN` and `box.yml dns.provider`; `laib init --tls internal` is
the explicit opt-in for a laptop-only trial and prints why. (b) — trusting
user CAs in the app — is not done: it would ship a weaker trust model to
every phone for the sake of a trial. (c) is (a) with a foreign domain.

Owner actions before the NUC: delegate `nuc.dopp.cloud` at Strato (NS
records to deSEC's name servers), create the zone and an API token at
deSEC, pass both to `laib-stick`. The one-box end state on the Kraftwerk
keeps HTTP-01 over port 80 as today, or the same delegation.
