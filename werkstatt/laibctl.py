#!/usr/bin/env python3
"""`laib` inside the Werkstatt: the Laib MCP as a shell command, for pi, for
Claude Code and for a human in the container (the analogue of solarisbay's
pi_servicebay.py). The token is read from a 0600 file, never from argv.

  laib                      list the verbs this hand may call
  laib status [args…]       call laib_status with the arguments as one string
  laib propose --set services/x/values.yml:K=V "grund"
  laib write media /path < file     (stdin becomes the content)
  laib template lint <dir>  runs here, with the release's laib binary
  laib --headers            the Authorization header as JSON: Claude Code's
                            headersHelper for the laib MCP server (#380)

Which hand speaks (#380): a hand's own file wins (LAIB_TOKEN_FILE, the web
pi's; /run/laib/token, the autoloop's pi). Otherwise this is a Claude
session in the dev container: it holds only the werkstatt hand's token and
asks Laib for a claude token for its session (laib_session <name>, the name
from LAIB_SESSION, "shell" outside one), kept 0600 in the tmpfs until ten
minutes before it runs out.
"""
import json, os, sys, urllib.request
from datetime import datetime, timedelta, timezone

URL = (os.environ.get("LAIB_MCP_URL") or "http://host.containers.internal:5900/mcp").rstrip("/")
RUN = os.environ.get("LAIB_RUN_DIR") or "/run/laib"   # this container's own tmpfs (#44)
TOKEN_FILE = os.environ.get("LAIB_TOKEN_FILE") or RUN + "/token"
MARGIN = timedelta(minutes=10)
# the kernel binary of the latest release, for what needs no box (#262)
LOCAL_BIN = os.environ.get("LAIB_LOCAL_BIN") or "/opt/werkstatt/laib/laib"

def read(path: str) -> str:
    try:
        return open(path).read().strip()
    except OSError:
        return ""

def session_token() -> str:
    """the claude token of this session, from the cache or freshly asked for"""
    name = "".join(c for c in os.environ.get("LAIB_SESSION", "shell") if c.isalnum() or c in "-_.")[:60].lstrip(".") or "shell"
    cache = os.path.join(RUN, "sessions", name + ".json")
    try:
        got = json.load(open(cache))
        if datetime.fromisoformat(got["expires"]) - MARGIN > datetime.now(timezone.utc):
            return got["token"]
    except (OSError, ValueError, KeyError):
        pass
    res = rpc("tools/call", {"name": "laib_session", "arguments": {"args": name}}, read(RUN + "/werkstatt-token"))
    text = "".join(c.get("text", "") for c in res.get("content", []))
    if res.get("isError"):
        raise SystemExit("laib: no session token: " + text)
    got = json.loads(text)
    os.makedirs(os.path.dirname(cache), mode=0o700, exist_ok=True)
    fd = os.open(cache + ".tmp", os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w") as f:
        json.dump({"token": got["token"], "expires": got["expires"]}, f)
    os.replace(cache + ".tmp", cache)
    return got["token"]

def token() -> str:
    return read(TOKEN_FILE) or (session_token() if os.path.exists(RUN + "/werkstatt-token") else os.environ.get("LAIB_TOKEN", ""))

_id = 0
def rpc(method: str, params: dict, bearer: str | None = None):
    global _id
    _id += 1
    body = json.dumps({"jsonrpc": "2.0", "id": _id, "method": method, "params": params}).encode()
    req = urllib.request.Request(URL, data=body, headers={"Content-Type": "application/json", "Authorization": "Bearer " + (token() if bearer is None else bearer)})
    with urllib.request.urlopen(req, timeout=900) as r:
        env = json.load(r)
    if env.get("error"):
        raise SystemExit(f"laib: {env['error'].get('message')}")
    return env["result"]

def main(argv: list[str]) -> int:
    if argv == ["--headers"]:
        print(json.dumps({"Authorization": "Bearer " + token()}))
        return 0
    if argv[:2] == ["template", "lint"]:
        # the contract check is pure and runs here, on the Werkstatt's own
        # files: over MCP the box would look for this path on its disk (#262)
        os.execv(LOCAL_BIN, [LOCAL_BIN, *argv])
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
