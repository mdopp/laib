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
if [ -n "${LAIB_TOKEN:-}" ]; then
  # Every session on this box gets Laib as an MCP server, as the hand the token names.
  su dev -c "claude mcp add --transport http laib ${LAIB_MCP_URL:-http://host.containers.internal:5900/mcp} --header 'Authorization: Bearer ${LAIB_TOKEN}' >/dev/null 2>&1 || true"
fi
ssh-keygen -A
exec /usr/sbin/sshd -D -e -p 22
