#!/usr/bin/env python3
"""`laib` inside the Werkstatt: the Laib MCP as a shell command, for pi, for
Claude Code and for a human in the container (the analogue of solarisbay's
pi_servicebay.py). The token is read from a 0600 file, never from argv.

  laib                      list the verbs this hand may call
  laib status [args…]       call laib_status with the arguments as one string
  laib propose --set services/x/values.yml:K=V "grund"
  laib write media /path < file     (stdin becomes the content)
"""
import json, os, sys, urllib.request

URL = (os.environ.get("LAIB_MCP_URL") or "http://host.containers.internal:5900/mcp").rstrip("/")
TOKEN_FILE = os.environ.get("LAIB_TOKEN_FILE") or "/workspace/.laib/token"

def token() -> str:
    try:
        return open(TOKEN_FILE).read().strip()
    except OSError:
        return os.environ.get("LAIB_TOKEN", "")

_id = 0
def rpc(method: str, params: dict):
    global _id
    _id += 1
    body = json.dumps({"jsonrpc": "2.0", "id": _id, "method": method, "params": params}).encode()
    req = urllib.request.Request(URL, data=body, headers={"Content-Type": "application/json", "Authorization": "Bearer " + token()})
    with urllib.request.urlopen(req, timeout=900) as r:
        env = json.load(r)
    if env.get("error"):
        raise SystemExit(f"laib: {env['error'].get('message')}")
    return env["result"]

def main(argv: list[str]) -> int:
    rpc("initialize", {})
    if not argv:
        for t in rpc("tools/list", {})["tools"]:
            print(f"{t['name'][5:]:<10} {t['description']}")
        return 0
    verb, rest = argv[0], argv[1:]
    args = {"args": " ".join(f'"{a}"' if " " in a else a for a in rest)}
    if verb in ("write",) or (verb == "propose" and "--patch" in rest):
        key = "patch" if verb == "propose" else "stdin"
        args[key] = sys.stdin.read()
    res = rpc("tools/call", {"name": "laib_" + verb, "arguments": args})
    for c in res.get("content", []):
        sys.stdout.write(c.get("text", ""))
    return 3 if res.get("isError") else 0

if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
