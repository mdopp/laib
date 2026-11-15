# ADR 0004 — Templates are Quadlet files plus a `laib.yml` contract, not forms and generators

Applies when you write or change a template, add a variable type, reach for
Mustache, or write a post-deploy script.

- **Status:** Accepted, 2026-10-08

## Context

ServiceBay templates were Pod YAML, `variables.json` (which generated the
wizard form), Mustache config, `post-deploy.py` and migration scripts. Across
14 templates, 26 variables were secrets and 15 were subdomains, and those
declarations drove proxy, SSO, DNS and LAN-block rendering. With an agent
writing the files, the form is gone; the conventions are not. An agent that
converts an upstream compose file does not know on its own that a port must
never reach the WLAN.

## Decision

1. A template is a folder of Quadlet files with `${VAR}` placeholders plus one
   `laib.yml` declaring routes, secrets, OIDC, data paths, health, ports and
   GPU (`docs/STATE.md`).
2. Substitution is `${VAR}` only. No Mustache, no logic in templates.
3. `post-deploy` stays allowed as a last resort and is journaled. What it used
   to do (OIDC client, admin password, bcrypt) is declarative in `laib.yml`
   and rendered by the kernel.
4. A schema bump is a proposal with an attached migration script, run once
   after a snapshot, journaled. `migrations/` is optional.
5. `laib template lint` checks the contract; CI runs it over every template.
   Registries are Git repos of such folders.

## Consequences

- An agent builds a new service from upstream docs plus the lint output.
- The irreducible part of a template is `laib.yml`; everything else is the
  upstream's own configuration, written as it is.

## Amendment 2026-10-11 (#36): hooks run in an image, in two phases

Fedora CoreOS has no Python, and a hook on the host is a shell on the
host. Hooks therefore run in `ghcr.io/mdopp/laib-hooks` (Python with the
standard library, bcrypt, bash, curl, openssl; `box.yml laib.hooksImage`
overrides), on the host network, with the service folder and
`/mnt/data/laib/render` read-only and the service's declared data paths
read-write — all at their host paths, so a hook reads and writes exactly
what it did before. Two phases: **`pre-start`** runs before the units
start (configuration files, folders, keys — it may not assume the service
is up; Authelia's `configuration.yml` and AdGuard's first `AdGuardHome.yaml`
live here), **`post-deploy`** runs after a green verify and talks to the
running service. A hook may print `laib-secret: NAME=value` and
`laib-exec: <container> <command…>`; the latter Laib runs in one of the
service's own containers — nothing else, and never with a secret on its
argv (#214). `laib template lint` refuses a
hook whose interpreter the image lacks and a `Volume=` source that is not a
declared data path or a rendered file.

## Amendment 2026-11-15 (#1239): one card per release, none for comments

Part of ADR 0035's rethinks (#1232). A pure comment change across the
templates would have filed 13 cards, each with a restart, titled by the
command („template update home-assistant“).

- **What runs as before is no proposal.** A template update whose every
  changed file is a unit equal but for its comment and blank lines (`#`,
  `;`: systemd's own rule), a `laib.yml` equal as YAML, the README, the
  news or a `.template/` copy files nothing (`proposal.ErrInert`) and
  leaves main as it is, so the next apply restarts nothing; one journal
  line says so, at most once in 30 days per service. A hook or any other
  file counts byte by byte: a comment in a script, where a `#` line can be
  part of a heredoc, stays a card. The comments reach the box with the
  next change that runs differently.
- **The drift check agrees** (`scripts/drift.sh` check 7): comment and
  blank lines of a unit (`#`, `;`) or of `laib.yml` (`#`) need no news
  line; everything else in a template still does. The check is the
  kernel's rule read on the diff, so a change that needs no line is one
  the box files no card for; the one gap is a `#` line inside a YAML block
  scalar of `laib.yml`, which the check lets through and the box shows as
  a card without a sentence.
- **One card per release, still one proposal per service.** The box's
  own sync (`template sync --propose`, actor `laib`) marks its template
  updates `karte: "Laib <version>"`, and an older one still open joins
  the newest. The Inbox page, „Alle freigeben“, `laib inbox` (so the chat
  and Homes) and `laib context` fold them into one card (`proposal.Fold`),
  German throughout: „Neue Vorlagen aus Laib 2.31.0 für …“, each
  template's news by service. One approve or reject decides all members.
  They stay separate proposals underneath because the run-isolation of
  #1209 works per proposal: approved together they are one run, and a red
  service goes back alone while the others stay. One proposal for all
  would have taken every service back with the red one.

Weighed against before (#246): the owner taps once per release instead of
once per service and reads what each brings in one place; nothing gets
worse — the per-service rollback, the dial and the human-only reasons are
the same, and a person's or an agent's `laib template update <svc>` is
still its own card. ServiceBay offered an upgrade only when a template
bumped its schema version, as a badge per service with its CHANGELOG's
section headers (`packages/backend/src/lib/templateUpgrades.ts`): a comment
never reached anyone, but every upgrade was its own decision and a forgotten
bump hid a real change. Laib keeps measuring the content instead of trusting
a number, and now measures what runs.
