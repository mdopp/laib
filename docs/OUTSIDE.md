# What Laib deliberately does not do

Three things ServiceBay did that Laib leaves outside, written down so nobody
reads the gap as an oversight (issue #19).

## Router port-forwarding

`exposure: public` makes Caddy serve a host without the LAN matcher. It does
not open the router. ServiceBay talked to the Fritz!Box (gateway poller,
port-forward edges in the network map); Laib does not know the router exists.

Without the forward nothing changes for a LAN-only (`exposure: internal`)
name: Caddy answers any source outside the private ranges with 403 before
Authelia. What the person sees is a German page saying the name works only
in the home WLAN, and a link to Haus, which works on the road (#480). Laib
has no VPN; that is the "anything else" below.

- Once per box: forward TCP 80 and 443 on the router to the box's LAN IP
  (`box.yml lanIp`). Certificates use HTTP-01 (ADR 0007, amendment
  2026-10-13), so port 80 must reach the box; a DNS provider with an API
  (DNS-01) is the fallback.
- Anything else (a game server port, a VPN) is a manual rule or a script
  beside Laib that reads `laib status --json` and talks TR-064. It is not a
  kernel concern and never a probe.

## Residents, groups, passwords

Users live in LLDAP at runtime, not in the state repo. Laib renders
Authelia's access rules (which *group* may reach which route) and nothing
about *who* is in a group.

A resident is invited, never signs up (owner, 2026-10-18; no public form, as
ServiceBay's `dopp.cloud` request page was). `laib person invite <user>
--first F --last L --email E [--group family|admins]`, or Haus's Box card
through the haus hand's `person_invite` (haus#56), creates the LLDAP user
without a password, in the group, and starts Authelia's "Passwort vergessen"
for it (#360). Authelia mails the link, in German, with the way to
`haus.<domain>/willkommen`; the person sets the first password there, so no
password passes through Laib or Haus. Without a mail server (#359) the verb
returns the link to pass on (Signal); it lives 24 hours, and `--resend`
sends a new one and keeps the group. `laib person pending` lists who has not
used the link yet. `--signal +49…` also proposes the person's Signal number
for box.yml `people` (#596), approved by a human like any box.yml change;
`laib person signal <user> +49…` sets or changes it later. The haus hand may invite into `family` only: an admin is
a human's call at the console. Removing someone stays LLDAP's admin UI
(`ldap.`). The tool doing the LLDAP and Authelia work is the auth template's
`person.py`, run in the hooks image like a hook. `dopp.cloud` and `www.`
redirect to `haus.` (the haus route's `redirectFrom`), so the old entry point
still leads somewhere.

## No canary channel

ServiceBay had a `:dev` image channel with a box-verify before `:latest`.
Laib has one box and no fleet: the gate is CI (`scripts/e2e.sh`, later the
real-boot job #7), the safety net is per-service apply with a snapshot and
`laib rollback`. A second channel would be a second thing to keep in sync for
one consumer. If a second box ever exists, that box is the canary.

## The outside watchdog (#57)

Everything that can say "something is wrong" runs on the box. So the one
thing outside is a watchdog: `box.yml watchdog.url` names a
healthchecks-style ping URL; `laib-notify` (its timer, #443) pings it every five minutes
while readiness is not red, and the outside turns fifteen minutes of
silence into a push to the admin's phone. Readiness shows „Wächter:
zuletzt gemeldet 12:05“, diag is yellow without a URL. `laib family-card`
prints the paper card for the family — what the box does, the three
lights, "Stecker ziehen, 10 Sekunden, 5 Minuten warten, dann anrufen",
never the stick — with `watchdog.contact` as the number to call.
