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
  echo 'if [ -r /run/laib/claude-token ]; then export CLAUDE_CODE_OAUTH_TOKEN="$(cat /run/laib/claude-token)"; fi' > /etc/profile.d/claude.sh
fi
if [ -n "${LAIB_TOKEN:-}" ]; then
  # the token as a 0600 file: `laib` (laibctl) and pi read it from there, never from argv
  printf '%s' "$LAIB_TOKEN" > /run/laib/token && chmod 600 /run/laib/token && chown dev:dev /run/laib/token
  # Every session of dev gets Laib as an MCP server, as the hand the token names.
  # The header names the token and Claude Code expands it from the session's
  # environment: on su's and claude's argv the host's ps would show it (#217).
  # A login shell reads it from the 0600 file, so only dev's processes hold it.
  echo 'if [ -r /run/laib/token ]; then export LAIB_TOKEN="$(cat /run/laib/token)"; fi' > /etc/profile.d/laib.sh
  # the home is kept now: drop the entry a previous start wrote, so a new URL takes
  su dev -c "claude mcp remove -s user laib >/dev/null 2>&1 || true"
  su dev -c "claude mcp add -s user --transport http laib ${LAIB_MCP_URL:-http://host.containers.internal:5900/mcp} --header 'Authorization: Bearer \${LAIB_TOKEN}' >/dev/null 2>&1 || true"
fi
if [ -n "${WERKSTATT_GIT_TOKEN:-}" ]; then
  printf 'https://x-access-token:%s@github.com\n' "$WERKSTATT_GIT_TOKEN" > /run/laib/git-credentials
  chmod 600 /run/laib/git-credentials; chown dev:dev /run/laib/git-credentials
  su dev -c "git config --global credential.helper 'store --file /run/laib/git-credentials'"
fi
# the guide's skills where pi and Claude Code look, from this image (#394)
su dev -c "werkstatt-skills" || echo "werkstatt-skills failed: the agents start without the guide's skills"
if [ "${WERKSTATT_ROLE:-ssh}" = "autoloop" ]; then
  # the second container of the pod: pi works tickets, same /workspace
  exec su dev -c "python3 /opt/werkstatt/autoloop.py"
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
