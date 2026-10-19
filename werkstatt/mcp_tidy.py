#!/usr/bin/env python3
"""werkstatt-mcp-tidy (#392): drop the MCP servers in ~/.claude.json that
point at ServiceBay's MCP (port 5888, `servicebay`, `atHome-Servicebay`),
user scope and per project. ServiceBay is off; Claude Code tries them on
every session start and reports them failed. Laib's own entry (`laib`, set
by the entrypoint) and everything else stay as they are; the file is
rewritten only when something goes, with its mode kept. Prints the names,
never an entry's headers. Run as dev at every start."""
import json, os, sys
from urllib.parse import urlparse

PATH = os.path.join(os.path.expanduser("~"), ".claude.json")


def servicebays(entry):
    try:
        return urlparse(entry.get("url") or "").port == 5888
    except (AttributeError, ValueError):
        return False


try:
    cfg = json.load(open(PATH))
except FileNotFoundError:
    sys.exit(0)
except ValueError as e:
    print(f"werkstatt-mcp-tidy: {PATH} is not JSON ({e}); left alone", file=sys.stderr)
    sys.exit(0)
gone = []
scopes = [("user", cfg)] + [(p, v) for p, v in (cfg.get("projects") or {}).items() if isinstance(v, dict)]
for where, scope in scopes:
    servers = scope.get("mcpServers")
    if not isinstance(servers, dict):
        continue
    for name in [n for n, e in servers.items() if isinstance(e, dict) and servicebays(e)]:
        del servers[name]
        gone.append(f"{name} ({where})")
if gone:
    tmp = PATH + ".tmp"
    with open(tmp, "w") as f:
        json.dump(cfg, f, indent=2)
    os.chmod(tmp, os.stat(PATH).st_mode & 0o777)
    os.replace(tmp, PATH)
    print("werkstatt-mcp-tidy: ServiceBay's MCP entries removed:", ", ".join(gone))
