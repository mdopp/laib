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

**Tokens per container (#44).** Each container writes its own tokens to
its own tmpfs `/run/laib` (`Tmpfs=` in both units): the dev container
holds `LAIB_CLAUDE_TOKEN` (hand `claude`, the TTL `policy.yml` declares —
`laib token mint claude --note werkstatt` again and `laib secret set
LAIB_CLAUDE_TOKEN` when it runs out) and `WERKSTATT_GIT_TOKEN`; the
autoloop holds `LAIB_PI_TOKEN` (hand `pi`) and `WERKSTATT_PI_GIT_TOKEN`, a
fine-grained PAT limited to `AUTOLOOP_REPOS` (Contents rw for the branch
push and the claim ref, Issues r, Pull requests rw). Nothing with a token
touches the shared `/workspace` volume, so restarting the two in either
order cannot swap hands; `laib journal` shows `pi` for the autoloop's notes
and `claude` for the dev container's. Still open: the `dev.<domain>` web
surface (pi-web's UI), which arrives with Haus; LDAP logins; per-project
tokens.

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
