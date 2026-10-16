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
