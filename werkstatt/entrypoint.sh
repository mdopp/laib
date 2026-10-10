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
  # the token as a 0600 file: `laib` (laibctl) and pi read it from there, never from argv
  mkdir -p /workspace/.laib && printf '%s' "$LAIB_TOKEN" > /workspace/.laib/token && chmod 600 /workspace/.laib/token && chown -R dev:dev /workspace/.laib
  # Every session on this box gets Laib as an MCP server, as the hand the token names.
  su dev -c "claude mcp add --transport http laib ${LAIB_MCP_URL:-http://host.containers.internal:5900/mcp} --header 'Authorization: Bearer ${LAIB_TOKEN}' >/dev/null 2>&1 || true"
fi
if [ -n "${WERKSTATT_GIT_TOKEN:-}" ]; then
  mkdir -p /workspace/.laib
  printf 'https://x-access-token:%s@github.com\n' "$WERKSTATT_GIT_TOKEN" > /workspace/.laib/git-credentials
  chmod 600 /workspace/.laib/git-credentials; chown -R dev:dev /workspace/.laib
  su dev -c "git config --global credential.helper 'store --file /workspace/.laib/git-credentials'"
fi
if [ "${WERKSTATT_ROLE:-ssh}" = "autoloop" ]; then
  # the second container of the pod: pi works tickets, same /workspace
  exec su dev -c "python3 /opt/werkstatt/autoloop.py"
fi
ssh-keygen -A
exec /usr/sbin/sshd -D -e -p 22
