# What Laib deliberately does not do

Three things ServiceBay did that Laib leaves outside, written down so nobody
reads the gap as an oversight (issue #19).

## Router port-forwarding

`exposure: public` makes Caddy serve a host without the LAN matcher. It does
not open the router. ServiceBay talked to the Fritz!Box (gateway poller,
port-forward edges in the network map); Laib does not know the router exists.

- Once per box: forward TCP 80 and 443 on the router to the box's LAN IP
  (`box.yml lanIp`). Certificates use HTTP-01 (ADR 0007, amendment
  2026-10-13), so port 80 must reach the box; a DNS provider with an API
  (DNS-01) is the fallback.
- Anything else (a game server port, a VPN) is a manual rule or a script
  beside Laib that reads `laib status --json` and talks TR-064. It is not a
  kernel concern and never a probe.

## Residents, groups, passwords

Users live in LLDAP at runtime, not in the state repo. Creating, approving
and removing a resident is Solaris's onboarding card talking to LLDAP
directly (solarisbay #10 tracking). Laib renders Authelia's access rules
(which *group* may reach which route) and nothing about *who* is in a group.

## No canary channel

ServiceBay had a `:dev` image channel with a box-verify before `:latest`.
Laib has one box and no fleet: the gate is CI (`scripts/e2e.sh`, later the
real-boot job #7), the safety net is per-service apply with a snapshot and
`laib rollback`. A second channel would be a second thing to keep in sync for
one consumer. If a second box ever exists, that box is the canary.

## The outside watchdog (#57)

Everything that can say "something is wrong" runs on the box. So the one
thing outside is a watchdog: `box.yml watchdog.url` names a
healthchecks-style ping URL; `laib serve` pings it every five minutes
while readiness is not red, and the outside turns fifteen minutes of
silence into a push to the admin's phone. Readiness shows „Wächter:
zuletzt gemeldet 12:05“, diag is yellow without a URL. `laib family-card`
prints the paper card for the family — what the box does, the three
lights, "Stecker ziehen, 10 Sekunden, 5 Minuten warten, dann anrufen",
never the stick — with `watchdog.contact` as the number to call.
