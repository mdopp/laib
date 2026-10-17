#!/usr/bin/env bash
# Real-boot test (issue #7): no shims. Needs a user systemd with Podman's
# Quadlet generator (podman >= 4.4), e.g. the ubuntu-24.04 GitHub runner or
# any box. Applies the whoami template for real, waits for the container's
# health, checks status, adds usage-metrics as it ships (#238), then removes
# the units again.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="${LAIB_BIN:-$ROOT/laib}"
WORK="$(mktemp -d)"
export LAIB_HOME="$WORK/home"
export LAIB_QUADLET="${LAIB_QUADLET:-$HOME/.config/containers/systemd}"
export LAIB_SYSTEMD="${LAIB_SYSTEMD:-$HOME/.config/systemd/user}"
cleanup() {
  systemctl --user stop whoami.service 2>/dev/null || true
  systemctl --user stop usage-metrics.service 2>/dev/null || true
  podman secret rm laib.whoami.whoami.TOKEN >/dev/null 2>&1 || true
  podman secret rm laib.usage-metrics.usage-metrics.USAGE_METRICS_TOKEN >/dev/null 2>&1 || true
  rm -rf "$LAIB_QUADLET/whoami" "$LAIB_QUADLET/usage-metrics" "$WORK"
  systemctl --user daemon-reload 2>/dev/null || true
}
trap cleanup EXIT

systemctl --user --version >/dev/null
podman --version
mkdir -p "$LAIB_QUADLET"

LAIB_ACTOR=human:boot "$BIN" init --domain boot.test --data-dir "$HOME/laib-data" >/dev/null
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
# #224: the token is a podman secret: in the container's environment (whoami's ?env=true), in no unit
# file, on no podman run argv; a rotation replaces it and restarts the container with the new value
tokenOK() {
  local tok; tok="$(podman secret inspect --showsecret --format "{{.SecretData}}" laib.whoami.whoami.TOKEN)"
  [ -n "$tok" ] && [ "$tok" != "$1" ] || { echo "FAIL: no podman secret for TOKEN, or the old value" >&2; exit 1; }
  curl -fsS "http://127.0.0.1:18080/?env=true" | grep -qF "TOKEN=$tok" || { echo "FAIL: the container's TOKEN is not the podman secret" >&2; exit 1; }
  if grep -rqF -- "$tok" "$LAIB_QUADLET/whoami" || podman inspect systemd-whoami --format "{{.Config.CreateCommand}}" | grep -qF -- "$tok"; then echo "FAIL: the token is in the unit or on podman run's argv" >&2; exit 1; fi
  echo "$tok"
}
TOK="$(tokenOK "")"
LAIB_ACTOR=human:boot "$BIN" secret rotate WHOAMI_TOKEN >/dev/null || { echo "FAIL: rotate exit $?"; exit 1; }
tokenOK "$TOK" >/dev/null
# #238: a stateful template as it ships (embedded, laib add): the counter API
# counts only with its generated token, reads the count back, and keeps it
# across a restart, because the SQLite file lives in the declared data path
LAIB_ACTOR=human:boot "$BIN" add usage-metrics "boot test" > add-um.out || { echo "add usage-metrics exit $?"; cat add-um.out; journalctl --user -u usage-metrics.service --no-pager | tail -20; exit 1; }
UM=http://127.0.0.1:8094
UMTOK="$(podman secret inspect --showsecret --format "{{.SecretData}}" laib.usage-metrics.usage-metrics.USAGE_METRICS_TOKEN)"
[ -n "$UMTOK" ] || { echo "FAIL: no podman secret for USAGE_METRICS_TOKEN"; exit 1; }
printf 'Authorization: Bearer %s\n' "$UMTOK" > "$WORK/um.auth"
DAY="$(date -u +%F)"
umCode() { curl -s -o /dev/null -w '%{http_code}' "$@"; }
for h in "X-None: 1" "Authorization: Bearer wrong"; do
  [ "$(umCode -X POST -H "$h" -H 'Content-Type: application/json' -d "{\"app\":\"boot\",\"event\":\"refused\",\"day\":\"$DAY\",\"count\":5}" "$UM/ingest")" = 401 ] || { echo "FAIL: /ingest without the token is not refused ($h)"; exit 1; }
  [ "$(umCode -H "$h" "$UM/summary")" = 401 ] || { echo "FAIL: /summary without the token is not refused ($h)"; exit 1; }
done
curl -fsS -X POST -H @"$WORK/um.auth" -H 'Content-Type: application/json' -d "{\"app\":\"boot\",\"event\":\"test\",\"day\":\"$DAY\",\"count\":2}" "$UM/ingest" | python3 -c 'import sys,json; r=json.load(sys.stdin); assert r["total"]==2, r'
systemctl --user restart usage-metrics.service
for _ in $(seq 1 30); do curl -fsS "$UM/healthz" >/dev/null 2>&1 && break; sleep 1; done
curl -fsS -H @"$WORK/um.auth" "$UM/summary?days=1" | python3 -c 'import sys,json; s=json.load(sys.stdin); assert s["total"]==2 and s["apps"]==[{"app":"boot","total":2,"events":[{"event":"test","count":2}]}], s'
test -s "$WORK/data/usage-metrics/usage-metrics.db" || { echo "FAIL: the counters are not in the declared data path"; exit 1; }
"$BIN" status --json | python3 -c 'import sys,json; r=json.load(sys.stdin)["ready"]; assert r["state"]=="bereit" and r["red"]==0 and r["drift"]==0, r'
"$BIN" diag --json | python3 -c 'import sys,json; f=json.load(sys.stdin); assert not [x for x in f if x["severity"]=="rot"], f'
"$BIN" logs whoami --since 10m | grep -qi "whoami" || { echo "FAIL: no journald lines"; exit 1; }
# the operator against the real unit with a scripted model on the typed
# surface (#53, the default): inspect, restart, note
python3 - 18081 <<'PY2' &
import json, sys
from http.server import BaseHTTPRequestHandler, HTTPServer
SCRIPT = [
  {"role":"assistant","tool_calls":[{"id":"1","type":"function","function":{"name":"laib_inspect","arguments":json.dumps({"service":"whoami"})}}]},
  {"role":"assistant","tool_calls":[{"id":"2","type":"function","function":{"name":"laib_restart","arguments":json.dumps({"service":"whoami"})}}]},
  {"role":"assistant","tool_calls":[{"id":"3","type":"function","function":{"name":"laib_note","arguments":json.dumps({"kind":"incident","text":"whoami neu gestartet, grün."})}}]},
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
echo "boot test ok: whoami answered through a real Quadlet unit, and the operator restarted it for real; usage-metrics counted with its token only and kept the count across a restart"
