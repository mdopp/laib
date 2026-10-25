#!/usr/bin/env bash
# Keys from the mounted secret dir, sshd in the foreground. The ServiceBay
# habit of a config UI inside the dev container is gone: configuration is
# the service's values.yml, applied by laib.
set -euo pipefail
# The home is a data path (${DATA_DIR}/werkstatt/home, #241): Claude's
# sessions, the dotfiles and the history survive a restart and an image
# update. laib makes the folder as the container's root; dev gets it here.
# The image's dotfiles go into an empty home once (~/.cache is the cache
# data path's mount point), never over the person's files.
mkdir -p /home/dev
chown dev:dev /home/dev
if [ -d /home/dev/.cache ]; then chown dev:dev /home/dev/.cache; fi
# Claude Code's own tree is dev's: a ~/.claude/projects a root restore left
# behind (2026-10-20) kept every checkout without a folder yet from saving its
# conversation, so `claude --continue` after a restart started it empty
if [ -d /home/dev/.claude ]; then find /home/dev/.claude -xdev ! -user dev -exec chown dev:dev {} + || echo "werkstatt: WARNING: ~/.claude is not all dev's; a checkout may not save its conversation" >&2; fi
if [ -d /opt/werkstatt/home-seed ] && [ -z "$(ls -A /home/dev | grep -vx .cache || true)" ]; then
  su dev -c "cp -a /opt/werkstatt/home-seed/. /home/dev/"
fi
# laib creates the workspace data path as core, which is root in here; dev
# works in it (#262). The folder itself only, never -R over the checkouts.
chown dev:dev /workspace
mkdir -p /home/dev/.ssh
if [ -n "${WERKSTATT_AUTHORIZED_KEY:-}" ]; then
  echo "$WERKSTATT_AUTHORIZED_KEY" > /home/dev/.ssh/authorized_keys
  chmod 600 /home/dev/.ssh/authorized_keys
fi
chown -R dev:dev /home/dev/.ssh
if [ -n "${WERKSTATT_SSH_PASSWORD:-}" ]; then
  echo "dev:${WERKSTATT_SSH_PASSWORD}" | chpasswd
fi
# Tokens live in this container's own tmpfs (/run/laib, Tmpfs= in the unit),
# never on the shared /workspace volume: the dev container and the autoloop
# each keep their own hand and their own GitHub token (#44).
mkdir -p /run/laib && chmod 700 /run/laib && chown dev:dev /run/laib
if [ -n "${CLAUDE_CODE_OAUTH_TOKEN:-}" ]; then
  # Claude Code reads it from the login shell's environment; profile.d is
  # readable by every user here, so it names the 0600 file, never the token (#224)
  printf '%s' "$CLAUDE_CODE_OAUTH_TOKEN" > /run/laib/claude-token && chmod 600 /run/laib/claude-token && chown dev:dev /run/laib/claude-token
  # Only while dev has no claude.ai login of its own (#498): the token outranks
  # the login, and it can only make model requests, no Remote Control
  # (Claude Code's docs, "Generate a long-lived token"). With a login in the
  # kept home the sessions use it; the token is the fallback before /login.
  echo 'if [ -r /run/laib/claude-token ] && [ ! -s "$HOME/.claude/.credentials.json" ]; then export CLAUDE_CODE_OAUTH_TOKEN="$(cat /run/laib/claude-token)"; fi' > /etc/profile.d/claude.sh
fi
# The logins dev made itself live in the kept home (#498): Claude Code's
# (~/.claude/.credentials.json) and gh's (~/.config/gh/hosts.yml), carried
# from claude-dev by the move or made with `claude /login` and `gh auth
# login`. dev's alone, whoever wrote them (a root shell, the move), as
# claude-dev's entrypoint held them.
for f in /home/dev/.claude/.credentials.json /home/dev/.config/gh/hosts.yml; do
  if [ -f "$f" ]; then chown dev:dev "$f" "$(dirname "$f")" && chmod 600 "$f" && chmod 700 "$(dirname "$f")"; fi
done
if [ -n "${LAIB_TOKEN:-}" ]; then
  # the autoloop's pi hand as a 0600 file: `laib` (laibctl) and pi read it from there, never from argv
  printf '%s' "$LAIB_TOKEN" > /run/laib/token && chmod 600 /run/laib/token && chown dev:dev /run/laib/token
fi
if [ -n "${LAIB_WERKSTATT_TOKEN:-}" ]; then
  # The dev container holds no claude token (#380): only the werkstatt hand's,
  # which can get one claude token per session. Every Claude session of dev
  # gets Laib as an MCP server whose header comes from `laib --headers`
  # (Claude Code's headersHelper, run on each connection): that session's
  # token from /run/laib/sessions, or a new one when it runs out. Nothing
  # with a token lands on argv (#217) or in ~/.claude.json.
  printf '%s' "$LAIB_WERKSTATT_TOKEN" > /run/laib/werkstatt-token && chmod 600 /run/laib/werkstatt-token && chown dev:dev /run/laib/werkstatt-token
  rm -f /etc/profile.d/laib.sh
  # the home is kept: drop the entry a previous start wrote, so a new URL or form takes
  su dev -c "claude mcp remove -s user laib >/dev/null 2>&1 || true"
  su dev -c "claude mcp add-json -s user laib '{\"type\":\"http\",\"url\":\"${LAIB_MCP_URL:-http://host.containers.internal:5900/mcp}\",\"headersHelper\":\"laib --headers\"}' >/dev/null 2>&1 || true"
fi
if [ -n "${WERKSTATT_GIT_TOKEN:-}" ]; then
  printf 'https://x-access-token:%s@github.com\n' "$WERKSTATT_GIT_TOKEN" > /run/laib/git-credentials
  chmod 600 /run/laib/git-credentials; chown dev:dev /run/laib/git-credentials
  su dev -c "git config --global credential.helper 'store --file /run/laib/git-credentials'"
elif [ -s /home/dev/.config/gh/hosts.yml ]; then
  # no PAT for this container: git pushes with gh's own login, as claude-dev did (#498)
  su dev -c "gh auth setup-git" || echo "werkstatt: WARNING: gh auth setup-git failed, git push asks for a user" >&2
fi
# the guide's skills where pi and Claude Code look, from this image (#394)
su dev -c "werkstatt-skills" || echo "werkstatt-skills failed: the agents start without the guide's skills"
if [ "${WERKSTATT_ROLE:-ssh}" = "autoloop" ]; then
  # the second container of the pod: pi works tickets, same /workspace
  exec su dev -c "python3 /opt/werkstatt/autoloop.py"
fi
# pi in the browser (#384, werkstatt-pi-web), on pi.<domain> behind Authelia.
# Its values go to a 0600 file in this tmpfs, never on argv or into the
# home: the domain, the model, and the pi hand (also for `laib` in pi's
# shell). Kept running: a pi-web-ui that died must not leave pi.<domain> dark.
if [ -n "${WERKSTATT_DOMAIN:-}" ] && [ -n "${LAIB_PI_TOKEN:-}" ]; then
  printf '%s' "$LAIB_PI_TOKEN" > /run/laib/pi-token
  printf 'WERKSTATT_DOMAIN=%q\nMODEL_URL=%q\nMODEL_KEY=%q\nLAIB_TOKEN=%q\n' "$WERKSTATT_DOMAIN" "${MODEL_URL:-}" "${MODEL_KEY:-}" "$LAIB_PI_TOKEN" > /run/laib/pi-web.env
  chmod 600 /run/laib/pi-token /run/laib/pi-web.env && chown dev:dev /run/laib/pi-token /run/laib/pi-web.env
  (while true; do
    su dev -c "env -i HOME=/home/dev USER=dev LOGNAME=dev SHELL=/bin/bash PATH=/usr/local/bin:/usr/bin:/bin bash -lc werkstatt-pi-web" \
      || echo "werkstatt: WARNING: pi-web-ui ended, starting it again in 5s" >&2
    sleep 5
  done) &
fi
# Claude Code's Remote Control sessions, one per checkout with a CLAUDE.md
# (#383, werkstatt-sessions): a pass now, then every WERKSTATT_SESSIONS_EVERY
# seconds (0: only now), so a new checkout gets its session without a
# restart. As dev in a clean login shell: profile.d gives it the Claude and
# Laib tokens from /run/laib, and none of the container's other variables
# (the SSH password, the git token) reach a session.
sessions() {
  su dev -c "env -i HOME=/home/dev USER=dev LOGNAME=dev SHELL=/bin/bash TERM=xterm-256color PATH=/usr/local/bin:/usr/bin:/bin bash -lc werkstatt-sessions" \
    || echo "werkstatt: WARNING: the Claude sessions did not start (werkstatt-sessions)" >&2
}
sessions
every="${WERKSTATT_SESSIONS_EVERY:-300}"
if [ "$every" -gt 0 ] 2>/dev/null; then
  (while sleep "$every"; do sessions; done) &
fi
# sshd's host keys live in their own data path (${DATA_DIR}/werkstatt/ssh-host-keys),
# root's and 0600: made on the first start, then kept — the app trusts this key
mkdir -p /etc/ssh/keys && chmod 700 /etc/ssh/keys
hostkeys=()
for t in ed25519 ecdsa rsa; do
  k=/etc/ssh/keys/ssh_host_${t}_key
  if [ ! -s "$k" ]; then rm -f "$k" "$k.pub"; ssh-keygen -q -t "$t" -N '' -C "werkstatt" -f "$k"; fi
  hostkeys+=(-h "$k")
done
exec /usr/sbin/sshd -D -e -p 22 "${hostkeys[@]}"
