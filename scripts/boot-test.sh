#!/usr/bin/env bash
# Real-boot test (issue #7): no shims. Needs a user systemd with Podman's
# Quadlet generator (podman >= 4.4), e.g. the ubuntu-24.04 GitHub runner or
# any box. Applies the whoami template for real, waits for the container's
# health, checks status, then removes the unit again.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="${LAIB_BIN:-$ROOT/laib}"
WORK="$(mktemp -d)"
export LAIB_HOME="$WORK/home"
export LAIB_QUADLET="${LAIB_QUADLET:-$HOME/.config/containers/systemd}"
export LAIB_SYSTEMD="${LAIB_SYSTEMD:-$HOME/.config/systemd/user}"
cleanup() {
  systemctl --user stop whoami.service 2>/dev/null || true
  rm -rf "$LAIB_QUADLET/whoami" "$WORK"
  systemctl --user daemon-reload 2>/dev/null || true
}
trap cleanup EXIT

systemctl --user --version >/dev/null
podman --version
mkdir -p "$LAIB_QUADLET"

LAIB_ACTOR=human:boot "$BIN" init --domain boot.test >/dev/null
# No proxy, no sso: just the one service, so the check is Quadlet itself.
cat > "$LAIB_HOME/state/box.yml" <<YML
domain: boot.test
dataDir: $WORK/data
roles: {}
gpu: {present: false}
YML
mkdir -p "$LAIB_HOME/state/services/whoami"
cat > "$LAIB_HOME/state/services/whoami/laib.yml" <<YML
name: whoami
template: {registry: builtin, name: whoami, version: 1}
routes: []
secrets:
  - name: WHOAMI_TOKEN
data: []
health:
  http: http://127.0.0.1:18080/
ports: []
YML
cat > "$LAIB_HOME/state/services/whoami/values.yml" <<YML
IMAGE: docker.io/traefik/whoami:v1.10
TOKEN: !secret WHOAMI_TOKEN
YML
cat > "$LAIB_HOME/state/services/whoami/whoami.container" <<'UNIT'
[Unit]
Description=whoami boot test

[Container]
Image=${IMAGE}
PublishPort=127.0.0.1:18080:80
Environment=WHOAMI_NAME=${SERVICE}.${DOMAIN}
Environment=TOKEN=${TOKEN}

[Service]
Restart=no

[Install]
WantedBy=default.target
UNIT

"$BIN" plan > plan.out; head -5 plan.out   # no pipe: head closing early made plan die of SIGPIPE under pipefail (exit 141)
LAIB_ACTOR=human:boot "$BIN" apply --reason "boot test" || { echo "apply exit $?"; journalctl --user -u whoami.service --no-pager | tail -20; exit 1; }
curl -fsS http://127.0.0.1:18080/ | grep -q "whoami.boot.test" || { echo "FAIL: whoami does not answer with its name"; exit 1; }
"$BIN" status --json | python3 -c 'import sys,json; r=json.load(sys.stdin)["ready"]; assert r["state"]=="bereit" and r["red"]==0 and r["drift"]==0, r'
"$BIN" diag --json | python3 -c 'import sys,json; f=json.load(sys.stdin); assert not [x for x in f if x["severity"]=="rot"], f'
"$BIN" logs whoami --since 10m | grep -qi "whoami" || { echo "FAIL: no journald lines"; exit 1; }
# the operator against the real unit with a scripted model: inspect, restart, note
python3 - 18081 <<'PY2' &
import json, sys
from http.server import BaseHTTPRequestHandler, HTTPServer
SCRIPT = [
  {"role":"assistant","tool_calls":[{"id":"1","type":"function","function":{"name":"laib_inspect","arguments":json.dumps({"args":"whoami"})}}]},
  {"role":"assistant","tool_calls":[{"id":"2","type":"function","function":{"name":"laib_restart","arguments":json.dumps({"args":"whoami"})}}]},
  {"role":"assistant","tool_calls":[{"id":"3","type":"function","function":{"name":"laib_note","arguments":json.dumps({"args":"--kind incident \"whoami neu gestartet, grün.\""})}}]},
  {"role":"assistant","content":"fertig"},
]
i = 0
class H(BaseHTTPRequestHandler):
    def log_message(self, *a): pass
    def do_POST(self):
        global i
        n = int(self.headers.get("Content-Length", 0)); json.loads(self.rfile.read(n))
        m = SCRIPT[min(i, len(SCRIPT)-1)]; i += 1
        out = json.dumps({"choices":[{"message": m}]}).encode()
        self.send_response(200); self.send_header("Content-Type","application/json"); self.send_header("Content-Length", str(len(out))); self.end_headers(); self.wfile.write(out)
HTTPServer(("127.0.0.1", int(sys.argv[1])), H).serve_forever()
PY2
FAKE=$!
LAIB_LISTEN=127.0.0.1:18090 "$BIN" serve >/dev/null 2>&1 & SERVE=$!
sleep 1
OPS_TOKEN="$(LAIB_ACTOR=human:boot "$BIN" token mint ops --json | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')"
LAIB_MCP_URL=http://127.0.0.1:18090/mcp LAIB_TOKEN="$OPS_TOKEN" MODEL_URL=http://127.0.0.1:18081/v1 OPERATOR_HOME="$WORK/op" "${LAIB_OPERATOR_BIN:-$ROOT/laib-operator}" -once event:whoami > "$WORK/op.json" || { echo "operator exit $?"; cat "$WORK/op.json"; kill $FAKE $SERVE; exit 1; }
kill $FAKE $SERVE 2>/dev/null; wait $FAKE $SERVE 2>/dev/null || true
python3 -c 'import sys,json; s=json.load(open(sys.argv[1])); assert s["outcome"]=="green", s' "$WORK/op.json"
curl -fsS http://127.0.0.1:18080/ | grep -q "whoami.boot.test" || { echo "FAIL: whoami not back after the operator's restart"; exit 1; }
"$BIN" journal --json | python3 -c 'import sys,json; es=json.load(sys.stdin); assert any(e["verb"]=="restart" and e["actor"]=="ops" for e in es) and any(e["verb"]=="note" for e in es), [e["verb"] for e in es]'
echo "boot test ok: whoami answered through a real Quadlet unit, and the operator restarted it for real"
