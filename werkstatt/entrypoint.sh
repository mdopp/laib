#!/usr/bin/env bash
# Keys from the mounted secret dir, sshd in the foreground. The ServiceBay
# habit of a config UI inside the dev container is gone: configuration is
# the service's values.yml, applied by laib.
set -euo pipefail
mkdir -p /home/dev/.ssh
if [ -n "${WERKSTATT_AUTHORIZED_KEY:-}" ]; then
  echo "$WERKSTATT_AUTHORIZED_KEY" > /home/dev/.ssh/authorized_keys
  chmod 600 /home/dev/.ssh/authorized_keys
fi
chown -R dev:dev /home/dev/.ssh
if [ -n "${WERKSTATT_SSH_PASSWORD:-}" ]; then
  echo "dev:${WERKSTATT_SSH_PASSWORD}" | chpasswd
fi
if [ -n "${CLAUDE_CODE_OAUTH_TOKEN:-}" ]; then
  # Claude Code reads it from the environment of the login shell.
  echo "export CLAUDE_CODE_OAUTH_TOKEN=${CLAUDE_CODE_OAUTH_TOKEN}" > /etc/profile.d/claude.sh
fi
# Tokens live in this container's own tmpfs (/run/laib, Tmpfs= in the unit),
# never on the shared /workspace volume: the dev container and the autoloop
# each keep their own hand and their own GitHub token (#44).
mkdir -p /run/laib && chmod 700 /run/laib && chown dev:dev /run/laib
if [ -n "${LAIB_TOKEN:-}" ]; then
  # the token as a 0600 file: `laib` (laibctl) and pi read it from there, never from argv
  printf '%s' "$LAIB_TOKEN" > /run/laib/token && chmod 600 /run/laib/token && chown dev:dev /run/laib/token
  # Every session of dev gets Laib as an MCP server, as the hand the token names.
  # The header names the token and Claude Code expands it from the session's
  # environment: on su's and claude's argv the host's ps would show it (#217).
  # A login shell reads it from the 0600 file, so only dev's processes hold it.
  echo 'if [ -r /run/laib/token ]; then export LAIB_TOKEN="$(cat /run/laib/token)"; fi' > /etc/profile.d/laib.sh
  su dev -c "claude mcp add -s user --transport http laib ${LAIB_MCP_URL:-http://host.containers.internal:5900/mcp} --header 'Authorization: Bearer \${LAIB_TOKEN}' >/dev/null 2>&1 || true"
fi
if [ -n "${WERKSTATT_GIT_TOKEN:-}" ]; then
  printf 'https://x-access-token:%s@github.com\n' "$WERKSTATT_GIT_TOKEN" > /run/laib/git-credentials
  chmod 600 /run/laib/git-credentials; chown dev:dev /run/laib/git-credentials
  su dev -c "git config --global credential.helper 'store --file /run/laib/git-credentials'"
fi
if [ "${WERKSTATT_ROLE:-ssh}" = "autoloop" ]; then
  # the second container of the pod: pi works tickets, same /workspace
  exec su dev -c "python3 /opt/werkstatt/autoloop.py"
fi
ssh-keygen -A
exec /usr/sbin/sshd -D -e -p 22
