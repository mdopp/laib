# werkstatt

The one dev container (docs/DESIGN.md "Werkstatt"). v0 carries what
ServiceBay's `claude-dev` SSH path carried: a persistent `/workspace` with the
checkouts, Claude Code, pi, gh, git, tmux and sshd on port 2222 for the
Claude Code mobile app. Every session gets Laib as an MCP server as the
`claude` hand.

v1 (issue #8): the autoloop runs as a second container of the same image
(`werkstatt-autoloop`): pi works tickets labelled `pi:ready` in
`AUTOLOOP_REPOS`, opens a PR, and a ticket it cannot finish gets the label
`needs-claude` with the protocol attached, so the claude hand picks it up.
A transient failure (no clone, the preset refused, pi not startable) keeps
`pi:ready` and is tried again on later passes, three times, then it is
handed over with "Versuch 3 von 3" (#44). A run takes the GPU lease for
the coding preset under the holder `werkstatt` (laib ADR 0011, the lease
contract), renews it and gives it back at the end; while somebody else holds
the card the ticket simply waits for the next pass, without counting an
attempt (#162). Protocols and the attempt counter live on the workspace
volume (`/workspace/autoloop/protocol`), so a restart keeps them. `needs-claude` is consumed by a
person or a Claude Code session in the dev container
(`gh issue list -l needs-claude`); nothing automatic reads it. `laib` is a
shell command in the container (laibctl: the Laib MCP with the token from
`/run/laib/token`), `podlet` turns a compose file into Quadlet units, and
the pi extension `laib-llama` keeps pi's model list in step with the
lease. The image's `pi` is a small wrapper that loads it on every run, the
autoloop's and a person's over SSH (#261; npm's own binary is
`pi-upstream`, and pi's subcommands pass through untouched): `extensions/catalog.mjs` turns the lease's `/v1/models` (id,
`allowed_in_mode`, no `pi` object) into pi models and defines the provider
id once; the autoloop pins `laib-llama/<preset>` and a test checks both
agree and lists the lease's real catalogue through the extension.

**Tokens per container (#44).** Each container writes its own tokens to
its own tmpfs `/run/laib` (`Tmpfs=` in both units): the dev container
holds `LAIB_WERKSTATT_TOKEN` and `WERKSTATT_GIT_TOKEN`; the
autoloop holds `LAIB_PI_TOKEN` (hand `pi`) and `WERKSTATT_PI_GIT_TOKEN`, a
fine-grained PAT limited to `AUTOLOOP_REPOS` (Contents rw for the branch
push and the claim ref, Issues r, Pull requests rw). Nothing with a token
touches the shared `/workspace` volume, so restarting the two in either
order cannot swap hands; `laib journal` shows `pi` for the autoloop's notes
and `claude` for the dev container's.

**A short claude token per session (#380, ADR 0006 amendment).** No
claude token is at rest. `laib add werkstatt` mints `LAIB_WERKSTATT_TOKEN`
as the `werkstatt` hand (`policy.yml`: `{ceiling: read, verbs: [session],
session: claude}`). That hand can do one thing: `laib_session <name>`
returns a `claude` token with claude's TTL (8 h), journaled, and `laib
token list` shows it as `session werkstatt: <name>`. Claude Code's laib MCP
server has no fixed header. Its `headersHelper` is `laib --headers`, run
on each connection, which hands out the session's own token from
`/run/laib/sessions/<name>.json` (0600, tmpfs) and asks for a new one ten
minutes before that one runs out. `werkstatt-sessions` names each window's
session (`LAIB_SESSION=<checkout>`); a shell outside one is `shell`.
`laib` in the container speaks with the same token. When a token expires,
only that session loses access, and its next connection gets a new token
without the owner. Revoking the werkstatt token ends new sessions.
Still open: LDAP logins; per-project
tokens.

**Claude's sessions per checkout (#383).** As ServiceBay's `claude-dev`
did, the dev container starts one Claude Code session with Remote Control
per checkout in `/workspace` that has a `CLAUDE.md`: a window in the tmux
session `claude`, named after the checkout, running `claude --continue
--remote-control <name>` (a fresh session where there is nothing to
continue). They show up under the checkout's name in the Claude app and on
claude.ai. `werkstatt-sessions` (werkstatt/sessions.sh) does one pass; the
entrypoint runs it on every start and then every five minutes
(`WERKSTATT_SESSIONS_EVERY`), so a new checkout gets its session without a
restart, and a session that ended is started again. `werkstatt-sessions off
<name>` switches one off with a marker in `~/.werkstatt/no-autostart/` (the
kept home), so it stays off across restarts; `on <name>` brings it back.
Over SSH, `tmux attach -t claude` shows them; a new checkout's first start
may ask Claude Code's folder-trust question once in its window. The
sessions run as `dev` in a clean login shell (the Claude and Laib tokens
from `/run/laib`, no other variable of the container) and in Claude Code's
default permission mode: unlike claude-dev, no
`--allow-dangerously-skip-permissions`, because a session reachable from
the phone holds the apply-capable `claude` hand, not a read-only token.
`scripts/boot-werkstatt.sh` checks the windows after the first start, a
restart and an image update.

**pi in the browser (#384).** As ServiceBay's `pi-web` did, the dev
container serves pi-web-ui at `pi.<domain>`. The route is `exposure:
public`, `auth: two_factor`, `group: admins`; pi-web-ui has no login of its
own, so Authelia is the whole gate. Inside the container it listens on 8791,
the host publishes that on its loopback only (`lan: false`), and pi-web-ui
answers no Host but `pi.<domain>`. `werkstatt-pi-web` (werkstatt/piweb.sh)
starts it as `dev` with its own agent dir `~/.pi/web`: the laib-llama
extension in `settings.json` (the lease's catalogue, model choice in the web
UI's picker), the guide as `APPEND_SYSTEM.md`, and Laib in `mcp.json` as
the **pi** hand (`LAIB_PI_TOKEN`, also for `laib` in pi's shell). Only those
keys are written; what the web UI saved stays. The sessions, settings and
chats live in the kept home (`~/.pi/web`, `~/.pi-web`). The entrypoint
restarts pi-web-ui when it ends. Compared with pi-web: sessions, model
choice, files and terminal are there. Taking the GPU lease from the browser
is not: a model the standing mode does not allow answers "Modell nicht
erlaubt", and the coding preset stays the autoloop's. `pi` and the
Claude sessions run under the same uid `dev`, so the hands are kept apart
by environment, not by the kernel. The job `werkstatt-e2e/model` runs a
chat through the web UI's websocket against a fake model.

**The home is kept (#241).** `/home/dev` is the data path
`${DATA_DIR}/werkstatt/home`: Claude Code's sessions and settings
(`~/.claude`, `~/.claude.json`), dotfiles, shell history, git config —
a restart and an image update keep them, and the backup carries them.
`~/.cache` is its own data path, `${DATA_DIR}/werkstatt/cache`, with
`backup: false`; the image points npm (`NPM_CONFIG_CACHE`), Go
(`GOMODCACHE`, `GOCACHE`) and pip (`PIP_CACHE_DIR`) there. On the first
start into an empty home the entrypoint copies the image's dotfiles
(`/opt/werkstatt/home-seed`); a home with anything in it is never seeded
again, so nothing of the person's is overwritten. The folder is made by
laib as the container's root; the entrypoint (root in the container)
gives `/home/dev` and `~/.cache` to `dev` (uid 1000 in the container, a
subuid of `core` on the host) — no `UserNS=keep-id`, the backup reads the
files under `podman unshare` (docs/BACKUP.md). sshd's host keys live in
`${DATA_DIR}/werkstatt/ssh-host-keys` (`/etc/ssh/keys`, root's, 0600),
made on the first start and then kept, so the mobile app keeps trusting
the box; the image carries none. Secrets stay in `/run/laib`; the
entrypoint writes none into the home. What lands there in clear is what
a person logs into themselves: Claude Code's own login after `claude
/login` (`~/.claude/.credentials.json`; with `CLAUDE_CODE_OAUTH_TOKEN`
set there is none), `gh auth login`'s token. The autoloop has no home of
its own on disk: pi's hand stays apart (#44).

**Small programs (#262).** The Werkstatt builds the household's own small
programs and deploys them under their own `<name>.<domain>`. Both agents
read one guide, `werkstatt/guide/` in this repo and `/opt/werkstatt/guide/`
in the image: Claude Code as its managed CLAUDE.md
(`/etc/claude-code/CLAUDE.md`), pi appended to its system prompt by the `pi`
wrapper. `apps.md` covers the path from a wish to a checked route, and
`templates.md` covers the template contract and the login order.
The mechanics are one command, `laib-app`:

- `laib-app new <name>` scaffolds the app (Go, tests, Containerfile) and its
  template in the app registry repo `laib-apps`. That repo has one CI that
  tests every app, publishes `ghcr.io/<owner>/<name>` only after green tests,
  and lints every template.
- `laib-app register <url>` proposes the repo as the box's template registry,
  once.
- `laib-app check <name>.<domain>` checks public DNS, the certificate and the
  login. Exit 0 is done, 8 says what is missing, including the Strato
  sentence for the owner, and 9 means it could not be measured.

`laib template lint` runs inside the container with the latest release's
kernel. The image carries the toolbox the guide prescribes: go, the Python
venv and pip, an editor, ripgrep, dig and openssl. A tool that fails to
download fails the build.

The owner's steps:
- **once:** create the GitHub repo `mdopp/laib-apps`, then approve the
  registry proposal;
- **per app:** merge the PR, make the new ghcr package public, create the
  Strato CNAME `<name>` → `dopp.cloud`, and approve the service in the Inbox.

`scripts/werkstatt-app.sh` (job `werkstatt-e2e/app`) walks the path in this
image on a box on the runner.

Migration from `claude-dev`: point `${DATA_DIR}/werkstatt/workspace` at the
old `/workspace` volume for the checkouts; claude-dev's home and sessions
and its SSH host keys are copied into `werkstatt/home` and
`werkstatt/ssh-host-keys` once by the Kraftwerk move (docs/MIGRATION.md).
