# A small program, from the wish to `<name>.<domain>`

The path every Werkstatt app takes. Each step is a command; the reasons
are here so you know when a step does not fit. Laib's decisions behind
it: ADR 0004 (templates), ADR 0007 (routes, certificates, Strato), ADR
0012 (who may log in where), `docs/DESIGN.md` "When there is no template".

## 0. The wish

A wish arrives as a GitHub issue labelled `wunsch` (`laib wish`, from the
Bote or a person). Before you build:

- **Is there a template already?** `laib template list` (JSON: `available`
  and `installed`). An existing app
  that does the job beats a new one; say so and stop.
- **Is it a program at all?** A calendar, a file share, a photo library
  are upstream apps: write a template for them (`templates.md`), do not
  write the app.
- Cut the issue into a ticket with acceptance criteria. One question to
  the owner if the wish leaves a real choice open (who may use it: the
  family or only the admins; home network only or from outside).

## 1. Where it lives: the app registry repo

All of the household's own programs live in **one repo**, by default
`github.com/mdopp/laib-apps` (checked out at `/workspace/laib-apps`):

```
apps/<name>/         the program, its tests, its Containerfile
templates/<name>/    its Laib template: laib.yml, <name>.container, values.example.yml
.github/workflows/apps.yml   tests every app, publishes ghcr.io/<owner>/<name> from main, lints every template
```

The box knows the repo as the template registry `apps` (box.yml
`registries`); `laib template sync` pulls it. One repo means one CI file,
one registry entry and one place to look. A program big enough for its
own repo still uses this layout there (`apps/` with one app, `templates/`)
and gets its own registry entry.

If the repo does not exist on GitHub yet, that is the owner's step:
„Lege auf GitHub das Repo mdopp/laib-apps an (öffentlich, leer).“ Then
`git clone` it into `/workspace/laib-apps`.

## 2. Scaffold

```
laib-app new <name>                 # --group admins, --exposure public, --port N when needed
```

`<name>` is one lowercase noun (ADR 0021): it is the service, the route
`<name>.<domain>`, the image and the data folder at once. The scaffold is
a Go program (standard library only) with tests, a two-stage Containerfile
(distroless, non-root) and a template that is already right for a small
app:

- route `<name>.<domain>`, `exposure: internal`, `auth: one_factor`,
  `group: family`, `proxySecret` — Authelia does the login, Caddy then
  sends `Remote-User` with a header only it knows, and the app trusts
  `Remote-User` only with that header (ADR 0012 §3);
- the port published on `127.0.0.1` only and declared `lan: false`;
- `health: http: …/healthz`;
- the secret declared in `laib.yml` and referenced as `!secret` — generated
  on the box on first apply, never in the repo.

**Stack.** Go unless there is a reason: one static binary, a 10 MB image,
no runtime to update. Python (FastAPI/uvicorn, `python3 -m venv`) or Node
(Fastify) are fine when a library only exists there; keep the same
layout, a `Containerfile`, tests that `apps.yml` runs (`go test`, `npm
test`, `pytest`), and `/healthz`.

**Data.** An app that keeps state writes to one folder: declare
`data: [{path: ${DATA_DIR}/<name>}]` in `laib.yml` and mount exactly that
(`Volume=${DATA_DIR}/<name>:/data:z`). SQLite in WAL mode is enough. It is
backed up; with a database add `quiesce: true` and drop
`AutoUpdate=registry` from the unit (an image rollback does not roll back
migrated data). Mind the user: a distroless `nonroot` image runs as uid
65532, so add `UserNS=keep-id:uid=65532,gid=65532` or have the app tolerate
a folder it cannot write (lint and `laib logs` will tell).

**Login.** Your own small app has no login: keep forward-auth. If it must
serve a phone app or a client that cannot follow a browser redirect, use
Authelia's OIDC (`oidc:` in `laib.yml`, the client secret as `!secret
OIDC_<CLIENT>_SECRET`) — see `templates.md`. Never `auth: none` for an app
without its own login.

## 3. Build and test, here

```
cd /workspace/laib-apps/apps/<name> && go vet ./... && go test ./...
cd /workspace/laib-apps && laib template lint templates/<name>
```

`laib template lint` runs in the Werkstatt with the release's kernel: the
same contract check the box runs before it proposes the service (ports
used by another template, a volume that is not a data path, a secret that
is not declared, a health check missing).

You cannot build or run the image here (no podman). That is on purpose:
the image the box runs is the one CI built from the merged commit.

## 4. Ship: a PR, the owner merges, CI publishes

```
git switch -c feat/<name> && git add -A && git commit -m "feat(<name>): <what it does>"
git push -u origin feat/<name> && gh pr create --fill
```

The owner merges (from the phone is fine). On main, `apps.yml` runs the
tests, then publishes `ghcr.io/<owner>/<name>:latest` and `:<sha>`.

**ghcr footgun:** a new package is **private**. The box has no ghcr login,
so the first pull fails with `unauthorized`. Owner, once per app: „Stelle
auf GitHub unter Packages das Paket <name> auf öffentlich.“ Check with
`gh api /users/<owner>/packages/container/<name> --jq .visibility`.

## 5. The registry, once per repo

```
laib-app register https://github.com/mdopp/laib-apps
```

This proposes the `registries` entry in box.yml. It is the owner's
decision (a box.yml change); the Inbox card shows it. If box.yml has
other registries already, the command says so and prints the lines for
the owner to add.

## 6. Propose the service

```
laib template sync                     # the box pulls the registry
laib template list | jq -e '.available["<name>"]'   # ["apps"]: the box sees it (over MCP the answer is JSON)
laib propose --add <name> "<why, in one German sentence for the owner>"
```

A new service is always the owner's decision: the Inbox card says what
changes („<name> wird installiert — unter <name>.<domain> nur im
Heimnetz erreichbar“), who proposed it and why. Wait for it. After the
approve, Laib applies: pulls the image, starts the unit, verifies the
health check, renders the Caddy route and Authelia's rule.

## 7. The subdomain: the owner's Strato record, then the check

Laib's certificates come from Let's Encrypt by HTTP-01 (ADR 0007), so
every routed name must exist in public DNS — also an internal one (it
is only reachable from the home network; AdGuard answers it at home). The
domain is at Strato, which has no API and no wildcard records. So the
owner creates one record per app. Tell them exactly, in one sentence:

> „Lege bei Strato unter dopp.cloud die Subdomain „<name>“ an und trage in
> ihren DNS-Einstellungen als CNAME-Ziel „dopp.cloud“ ein.“

(On a trial box under `nuc.dopp.cloud` the subdomain is `<name>.nuc`, and
the Kraftwerk's NPM needs an HTTP-only proxy host for it pointing at the
trial box's port 80; `laib-app check` prints that line too.)

Then check, and repeat until it exits 0:

```
laib-app check <name>.<domain>
```

- **dns:** public DNS (DNS over HTTPS) resolves the name to the house's
  address. Exit 8 with the Strato sentence until the record exists; Strato
  can take up to an hour.
- **cert:** Caddy on the box serves a valid certificate for the name.
  Caddy asks Let's Encrypt on its own once DNS answers and port 80 reaches
  the box; give it a few minutes, `laib logs caddy --since 15m` otherwise.
- **login:** without a login (and with a forged `Remote-User`) the route
  sends to `auth.<domain>`. An `auth: none` route is checked with `--open`.

Only now tell the owner it is done: „<Name> läuft unter https://<name>.<domain>,
anmelden mit dem Familien-Login.“

## 8. Changes later

- **Code:** a PR as in step 4. With `:latest` and `AutoUpdate=registry`
  the box picks the new image up in its update window; an app with a
  database pins `IMAGE: ghcr.io/<owner>/<name>:<sha>` and moves by
  `laib propose --set services/<name>/values.yml:IMAGE=… "<grund>"`.
- **Template** (route, secret, data): a PR, then `laib template sync`
  and `laib template update <name>` — a proposal like any other.
- **Public from outside:** `exposure: public` is a template change the
  owner decides; it also needs the house's port 443 to reach the box.

## When it goes wrong

`laib status`, `laib diag <name>`, `laib logs <name> --since 30m`,
`laib inspect <name>`. A red apply rolls back by itself; the journal
(`laib journal --since 2h`) says what happened.
