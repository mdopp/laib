# werkstatt

The one dev container (docs/DESIGN.md "Werkstatt"). v0 carries what
ServiceBay's `claude-dev` SSH path carried: a persistent `/workspace` with the
checkouts, Claude Code, pi, gh, git, tmux and sshd on port 2222 for the
Claude Code mobile app. Every session gets Laib as an MCP server as the
`claude` hand. With file-share installed, `/workspace` is also the LAN share
`\\<box>\werkstatt`, for the LLDAP admins only (#666): what they write there
is dev's, as Samba hands each new file to its folder's owner.

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

**The guide and its skills (#394).** What ServiceBay's agent kit carried
(an hourly git checkout in its data dir, mounted into claude-dev and
pi-web) is the Werkstatt's own now: `werkstatt/guide/` with `AGENTS.md`
and `skills/<name>/SKILL.md`, in the image, which is rebuilt on every push
to main and taken by `AutoUpdate=registry` in the next update window. No
release, no timer, one source. At every start `werkstatt-skills` links the
skills for pi (`~/.pi/agent/skills/laib`, `~/.pi/web/skills/laib`) and
Claude Code (`~/.claude/skills/<name>`). ServiceBay's own catalog stays
readable in the checkout `/workspace/servicebay/assists`; its ADRs and
CLI describe a system that is off and are not carried.

**Laib's MCP in the sessions (#392).** Every Claude Code session of `dev`
(over SSH, and the Remote Control sessions the Claude app opens) has Laib
as the MCP server `laib` at `host.containers.internal:5900/mcp`, user
scope, written by the entrypoint with the token as a header variable, never
on argv. (Until 2026-10-24 `werkstatt-mcp-tidy` dropped ServiceBay's
entries carried over in the kept home at every start; they are gone, and so
is the tidy.) There is
no claude.ai connector to the box: `admin.<domain>/mcp` answers only the
LAN (`respond 403` otherwise), and the owner's Claude from outside is the
Claude app attached to a Werkstatt session, which has `laib` already.

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
kept home), so it stays off across restarts and no fresh start follows a
`--continue` that ends; `on <name>` brings it back. It runs as dev
(`podman exec -u dev systemd-werkstatt bash -lc 'werkstatt-sessions off
<name>'`): as anyone else it refuses, since root's home and root's tmux are
not the sessions' (#498).
Over SSH, `tmux attach -t claude` shows them. Nobody is at a window, so
before a checkout's first start the pass answers Claude Code's first-run
questions in `~/.claude.json` (#498): `hasCompletedOnboarding` and
`projects["/workspace/<name>"].hasTrustDialogAccepted`, written only when
missing, everything else in the file kept. The
sessions run as `dev` in a clean login shell (the Claude and Laib tokens
from `/run/laib`, no other variable of the container but the permission
mode) in the mode the owner set as `PERMISSION_MODE` (#898):

- `default`: Claude Code asks in the app before a tool call that needs a
  permission (`--permission-mode default`).
- `bypass`: every tool call runs without asking
  (`--dangerously-skip-permissions`), as under ServiceBay's claude-dev; the
  pass also answers Claude Code's one-time warning
  (`skipDangerousModePermissionPrompt` in `~/.claude/settings.json`).
  **The risk:** a session reachable from the phone holds the apply-capable
  `claude` hand and the Werkstatt's GitHub token, so a prompt injected
  through a web page, an issue or a file runs its commands unasked. The
  owner chose this knowingly (2026-10-29); Laib's own gate still holds:
  what is above the `claude` hand's ceiling stays a proposal in the Inbox.

The mode is passed on every start, so a `--continue` does not fall back to
the mode its conversation last ran in. `laib status werkstatt` shows it
(„Claude-Sitzungen ohne Rückfragen …“), and in bypass `laib diag` names it.
A change of the value restarts the container and with it every session.
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
/login` (`~/.claude/.credentials.json`), `gh auth login`'s token
(`~/.config/gh/hosts.yml`); the entrypoint makes both dev's and 0600 at
every start. The autoloop has no home of its own on disk: pi's hand stays
apart (#44).

**Which Claude login (#498).** Remote Control needs Claude Code's own
claude.ai login. `CLAUDE_CODE_OAUTH_TOKEN` (`claude setup-token`) outranks
that login and can only make model requests: no Remote Control, no
claude.ai connectors (Claude Code's docs, "Authentication precedence" and
"Generate a long-lived token"). So the login shells, and with them the
sessions, get the token only while `~/.claude/.credentials.json` is
missing: on a new box the sessions work at once, without Remote Control;
after one `claude /login` over SSH, in any window, every session started
from then on (the next pass, a restart) uses the login and has Remote
Control. The login is kept in the home; Claude Code renews it while it is
used. When it has lapsed for good, `/login` again; deleting the file falls
back to the token. Leaving `CLAUDE_CODE_OAUTH_TOKEN` empty is fine too.

**git and gh (#498).** With `WERKSTATT_GIT_TOKEN` set, git pushes with that
PAT from `/run/laib`. Without it, and with gh logged in
(`~/.config/gh/hosts.yml`, carried from claude-dev or made with `gh auth
login`), the entrypoint runs `gh auth setup-git`, as claude-dev did, and
git pushes with gh's login. `user.name` and `user.email` are in the kept
`~/.gitconfig`.

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

Migration from `claude-dev` (migration day, 2026-10-19; the mover is in
git history, ADR 0010) copied the checkouts into `werkstatt/workspace` and,
from claude-dev's home (its `/workspace`), into `werkstatt/home` (#498):
`.claude.json` (onboarding, trust, the account),
`.claude/.credentials.json` (the claude.ai login),
`.claude/history.jsonl`, `.claude/projects/-workspace-<checkout>` for each
checkout that kept its name (the conversations `--continue` picks up),
`.config/gh/hosts.yml` and `.gitconfig`. claude-dev's `settings.json` (its
hooks) stayed behind.
