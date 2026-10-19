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

## Amendment 2026-10-13 (#45): HTTP-01 through the Kraftwerk instead of the deSEC delegation

The owner chose no new DNS account for the trial. Strato accepts no wildcard
records, but it does accept two-level subdomains (`laib.nuc.dopp.cloud`
resolves publicly as a CNAME to `dopp.cloud`, checked 2026-10-13). So the
NUC gets its certificates by **HTTP-01**: `box.yml` has no `dns.provider` and
no `tls: internal`, and Caddy uses Let's Encrypt's default challenge.

- **Public names:** one Strato subdomain per routed host, each a CNAME to
  `dopp.cloud`. Phase 1: `laib.nuc`, `auth.nuc`, `ldap.nuc`, `haus.nuc`,
  `dns.nuc`, `whoami.nuc`. A new service on the NUC needs one more.
- **Port 80:** the house's port 80 reaches the Kraftwerk's NPM. For each
  name there, NPM gets one proxy host that is **HTTP only (no SSL)** and
  forwards to the NUC's port 80. A proxy host without SSL does not include
  NPM's own `letsencrypt-acme-challenge.conf`, so the challenge reaches the
  NUC's Caddy (the same shape `flut.dopp.cloud` has today). From outside the
  NUC only shows its port 80 (the challenge or a redirect); HTTPS from
  outside still ends at the Kraftwerk, which has no route for these names.
- **Cost:** renewals (every ~60 days) need the Kraftwerk up and those proxy
  hosts in place, so the trial depends on the Kraftwerk at this one point.
  The proxy hosts and CNAMEs go when the trial ends.

The deSEC delegation (2026-10-11) stays the fallback, as does a DNS provider
with an API (INWX), which would also allow a wildcard certificate. When the
Kraftwerk itself runs Laib, it does HTTP-01 on its own port 80 and this
forwarding disappears.

## Amendment 2026-10-16 (#249): Authelia's rules are one rendered list

Point 4 kept ServiceBay's two own rules (apex deny, wildcard `one_factor` for
`family` and `admins`) in the auth hook's `configuration.yml`, next to laib's
rendered `laib-access.yml`. Authelia's own `check-policy` shows the two lists
are not joined: the later file's `rules` replace the earlier one's. The
wildcard was already gone on the box, and with the files in the other order
it alone would have decided, opening every admin host to the family at one
factor (ServiceBay's #878 shape).

So `laib-access.yml` is the whole of `access_control`: `default_policy: deny`
(the apex deny), and one rule per route whose subject is the route's group
plus `admins`, or `family` and `admins` where the route names none (the
wildcard's audience, now per host). The hook's config has no access rules.
A CI job judges the rendered rules with Authelia's binary from the pinned
image, in both file orders.

## Amendment 2026-10-19 (#378): one wildcard certificate, the challenge delegated to deSEC

Migration day turned phase 4 red for two new names (#377): Strato has no
API and no wildcard records, so every route name needed a hand-made CNAME
for HTTP-01, and every name landed in the CT logs. ServiceBay had the same
shape (NPM, one HTTP-01 certificate and one Strato record per host, and the
acme-challenge collision footgun on forward-auth hosts).

Decision (owner, variant 1): **ACME challenge delegation.** `dopp.cloud`
and its records stay at Strato; `_acme-challenge.dopp.cloud` is a CNAME to a
name in a deSEC zone, and Caddy writes the TXT there with a token limited to
that zone (`box.yml dns.challengeAlias`, `dns_challenge_override_domain`).
With any `dns.provider` the Caddyfile has one `cert_issuer acme` and a
`*.<domain>` site, so Caddy (2.10+) manages one wildcard certificate and
none per name. `acme_dns` is not used: it replaces the issuer's challenge
config, the alias with it, and the desec module takes no positional token.
The image pins Caddy (2.10.2) and the desec module. Alternatives not taken:
moving the zone to an API provider (INWX, deSEC) — more to move for the
same result; an NS delegation of a subzone (the 2026-10-11 amendment) —
it changes the names.
