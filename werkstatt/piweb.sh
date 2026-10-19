#!/usr/bin/env bash
# werkstatt-pi-web: pi in the browser (#384), pi-web-ui as ServiceBay's
# pi-web served it, now at pi.<domain> through Caddy and Authelia (admins,
# two factors). Runs as dev; the entrypoint keeps it running. pi-web-ui
# carries the pi SDK in-process, so the `pi` wrapper's -e and
# --append-system-prompt do not reach it: its own agent dir (~/.pi/web)
# names the laib-llama extension in settings.json, gets the Werkstatt guide
# as APPEND_SYSTEM.md and Laib as an MCP server with the pi hand. Only the
# keys this script owns are written; what the web UI saved there stays.
#
# No PI_WEB_TOKEN: Authelia is the gate, the port is published on the
# host's loopback only (werkstatt.container), and pi-web-ui refuses a Host
# that is not pi.<domain> (PI_WEB_ALLOW_HOSTS).
set -euo pipefail
# the entrypoint's values for this process: the domain, the model, the pi hand (0600, /run/laib)
ENV_FILE="${WERKSTATT_PI_WEB_ENV:-/run/laib/pi-web.env}"
if [ -r "$ENV_FILE" ]; then set -a; . "$ENV_FILE"; set +a; fi
AGENT="${PI_CODING_AGENT_DIR:-$HOME/.pi/web}"
EXT="${PI_LAIB_EXTENSION:-/opt/werkstatt/extensions/laib-llama.js}"
GUIDE="${PI_LAIB_GUIDE:-/opt/werkstatt/guide/AGENTS.md}"
mkdir -p "$AGENT"
cp "$GUIDE" "$AGENT/APPEND_SYSTEM.md"
python3 - "$AGENT" "$EXT" "${LAIB_MCP_URL:-http://host.containers.internal:5900/mcp}" <<'PY'
import json, os, sys
agent, ext, url = sys.argv[1:]
def merge(name, change):
    path = os.path.join(agent, name)
    try:
        data = json.load(open(path))
    except (OSError, ValueError):
        data = {}
    change(data)
    tmp = path + ".tmp"
    with open(tmp, "w") as f:
        json.dump(data, f, indent=2)
    os.replace(tmp, path)
def settings(d):
    exts = [e for e in d.get("extensions", []) if e != ext]
    d["extensions"] = [ext, *exts]
def mcp(d):
    # the header names the variable; pi expands it from this process's environment
    d.setdefault("mcpServers", {})["laib"] = {"url": url, "headers": {"Authorization": "Bearer ${LAIB_TOKEN}"},
                                              "description": "Laib, the box's control plane, as the pi hand"}
merge("settings.json", settings)
merge("mcp.json", mcp)
PY
host="pi.${WERKSTATT_DOMAIN:?WERKSTATT_DOMAIN is not set}"
export PI_CODING_AGENT_DIR="$AGENT" PI_WEB_PORT="${PI_WEB_PORT:-8791}" PI_WEB_HOST=0.0.0.0 PI_WEB_CWD="${PI_WEB_CWD:-/workspace}"
export PI_WEB_DATA_DIR="$HOME/.pi-web" PI_WEB_ALLOW_HOSTS="$host,127.0.0.1,localhost" PI_WEB_ALLOW_ORIGINS="https://$host"
# `laib` in pi's shell (laibctl) speaks as the pi hand too, not as claude
export LAIB_TOKEN_FILE=/run/laib/pi-token
# the SDK installed beside it (/opt/pi-web), the same pi version as the CLI
export PI_WEB_SDK=bundled
# the Claude login and the shell's claude hand are not pi's
unset PI_WEB_TOKEN CLAUDE_CODE_OAUTH_TOKEN
exec pi-web-ui --no-browser
