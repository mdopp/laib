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
  systemctl --user stop usage-metrics.service haus-boot.service 2>/dev/null || true
  podman secret rm laib.whoami.whoami.TOKEN >/dev/null 2>&1 || true
  podman secret rm laib.usage-metrics.usage-metrics.USAGE_METRICS_TOKEN >/dev/null 2>&1 || true
  for s in HAUS_PROXY_SECRET LAIB_TOKEN HAUS_USAGE_TOKEN; do podman secret rm "laib.haus-boot.haus-boot.$s" >/dev/null 2>&1 || true; done
  systemctl --user stop laib-operator.service 2>/dev/null || true
  podman secret rm laib.laib-operator.laib-operator.LAIB_TOKEN >/dev/null 2>&1 || true
  [ -z "${CANARY:-}" ] || sudo rm -rf /mnt/data/laib
  rm -rf "$LAIB_QUADLET/whoami" "$LAIB_QUADLET/usage-metrics" "$LAIB_QUADLET/haus-boot" "$LAIB_QUADLET/laib-operator" "$WORK" "$(dirname "$LAIB_QUADLET")/containers.conf.d/50-laib.conf"
  systemctl --user daemon-reload 2>/dev/null || true
}
trap cleanup EXIT

systemctl --user --version >/dev/null
podman --version
mkdir -p "$LAIB_QUADLET"
# the box config as every box has it (#281): journald's drop-in under a scratch /etc, the log driver for real
export LAIB_ETC="$WORK/etc"; bash "$ROOT/scripts/install-box.sh" --box-config
# the boot check the install writes beside the binaries (#368); these harnesses run no install
mkdir -p "$LAIB_ETC/systemd/system" && : > "$LAIB_ETC/systemd/system/laib-boot-check.service"

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
grep -q "whoami.boot.test" <<<"$(curl -fsS http://127.0.0.1:18080/)" || { echo "FAIL: whoami does not answer with its name"; exit 1; }
# #224: the token is a podman secret: in the container's environment (whoami's ?env=true), in no unit
# file, on no podman run argv; a rotation replaces it and restarts the container with the new value
tokenOK() {
  local tok; tok="$(podman secret inspect --showsecret --format "{{.SecretData}}" laib.whoami.whoami.TOKEN)"
  [ -n "$tok" ] && [ "$tok" != "$1" ] || { echo "FAIL: no podman secret for TOKEN, or the old value" >&2; exit 1; }
  grep -qF "TOKEN=$tok" <<<"$(curl -fsS "http://127.0.0.1:18080/?env=true")" || { echo "FAIL: the container's TOKEN is not the podman secret" >&2; exit 1; }
  if grep -rqF -- "$tok" "$LAIB_QUADLET/whoami" || grep -qF -- "$tok" <<<"$(podman inspect systemd-whoami --format "{{.Config.CreateCommand}}")"; then echo "FAIL: the token is in the unit or on podman run's argv" >&2; exit 1; fi
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
# #266: an unanswered sentence, the one text usage-metrics keeps: only with the token, read back, and
# gone once it is past its 30 days. A real container's clock cannot be faked (a static Go binary reads
# the vDSO clock, not libfaketime), so the row is moved back instead, which is the same to retention:
# stopped, its day set to 30 days ago, started; the sweep at boot deletes it before the first request
MISS="{\"source\":\"boot\",\"text\":\"Wann kommt der Müll?\",\"day\":\"$DAY\"}"
[ "$(umCode -X POST -H 'Content-Type: application/json' -d "$MISS" "$UM/miss")" = 401 ] || { echo "FAIL: /miss without the token is not refused"; exit 1; }
[ "$(umCode "$UM/misses")" = 401 ] || { echo "FAIL: /misses without the token is not refused"; exit 1; }
curl -fsS -X POST -H @"$WORK/um.auth" -H 'Content-Type: application/json' -d "$MISS" "$UM/miss" | python3 -c 'import sys,json; r=json.load(sys.stdin); assert r["count"]==1, r'
curl -fsS -H @"$WORK/um.auth" "$UM/misses?days=30" | python3 -c 'import sys,json; m=json.load(sys.stdin)["misses"]; assert m==[{"source":"boot","text":"Wann kommt der Müll?","count":1,"lastDay":sys.argv[1]}], m' "$DAY"
systemctl --user stop usage-metrics.service
python3 - "$WORK/data/usage-metrics/usage-metrics.db" <<'PY'
import sqlite3, sys
c = sqlite3.connect(sys.argv[1])
n = c.execute("UPDATE misses SET day = date('now', '-30 days')").rowcount
c.commit()
assert n == 1, n
PY
systemctl --user start usage-metrics.service
for _ in $(seq 1 30); do curl -fsS "$UM/healthz" >/dev/null 2>&1 && break; sleep 1; done
curl -fsS -H @"$WORK/um.auth" "$UM/misses?days=30" | python3 -c 'import sys,json; m=json.load(sys.stdin); assert m["misses"]==[] and m["total"]==0, m'
python3 -c 'import sqlite3,sys; n=sqlite3.connect(sys.argv[1]).execute("SELECT count(*) FROM misses").fetchone()[0]; assert n==0, "the sweep left %d misses in the file" % n' "$WORK/data/usage-metrics/usage-metrics.db"
curl -fsS -H @"$WORK/um.auth" "$UM/summary?days=1" | python3 -c 'import sys,json; s=json.load(sys.stdin); assert s["total"]==2, s'
"$BIN" status --json | python3 -c 'import sys,json; r=json.load(sys.stdin)["ready"]; assert r["state"]=="bereit" and r["red"]==0 and r["drift"]==0, r'
"$BIN" diag --json | python3 -c 'import sys,json; f=json.load(sys.stdin); assert not [x for x in f if x["severity"]=="rot"], f'
grep -qi "whoami" <<<"$("$BIN" logs whoami --since 10m)" || { echo "FAIL: no journald lines"; exit 1; }
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
grep -q "whoami.boot.test" <<<"$(curl -fsS http://127.0.0.1:18080/)" || { echo "FAIL: whoami not back after the operator's restart"; exit 1; }
"$BIN" journal --json | python3 -c 'import sys,json; es=json.load(sys.stdin); assert any(e["verb"]=="restart" and e["actor"]=="ops" for e in es) and any(e["verb"]=="note" for e in es), [e["verb"] for e in es]'
# (before Haus: its test stops haus-boot on purpose, and enable's apply verifies every service)
# ADR 0027 (#624): the Operator in its own container, for real — the image
# boot.yml built from this commit (OPERATOR_IMAGE, pinned into this laib),
# the unit laib renders. Laib's key, secrets.age and the state repo stay out
# of reach; its folder is in. The folders are /mnt/data/laib, as on every box
# (render.LaibHome): a scratch one here, with a canary key, secrets.age and
# state, and the test's own LAIB_HOME beside it.
[ -n "${OPERATOR_IMAGE:-}" ] && podman image exists "$OPERATOR_IMAGE" || { echo "FAIL: no operator image $OPERATOR_IMAGE (boot.yml builds it)"; exit 1; }
[ ! -e /mnt/data/laib ] || { echo "FAIL: /mnt/data/laib exists on this machine: the boot test will not touch it"; exit 1; }
CANARY=1; sudo mkdir -p /mnt/data/laib/state /mnt/data/laib/operator && sudo chown -R "$(id -un)" /mnt/data/laib
echo canary-key > /mnt/data/laib/age.key; echo canary-secrets > /mnt/data/laib/state/secrets.age; echo mine > /mnt/data/laib/operator/canary
printf 'laib:\n  port: 18090\nmodel:\n  url: http://127.0.0.1:18081/v1\n' >> "$LAIB_HOME/state/box.yml"
LAIB_ACTOR=human:boot "$BIN" operator enable > op-enable.out || { echo "FAIL: operator enable exit $?"; cat op-enable.out; journalctl --user -u laib-operator.service --no-pager | tail -30; exit 1; }
[ "$(systemctl --user is-active laib-operator.service)" = active ] || { echo "FAIL: laib-operator.service is not active"; journalctl --user -u laib-operator.service --no-pager | tail -30; exit 1; }
for _ in $(seq 1 30); do curl -fsS http://127.0.0.1:5901/ready >/dev/null 2>&1 && break; sleep 1; done
grep -q bereit <<<"$(curl -fsS http://127.0.0.1:5901/ready)" || { echo "FAIL: the Operator in its container does not answer on 127.0.0.1:5901"; journalctl --user -u laib-operator.service --no-pager | tail -30; exit 1; }
OPC=systemd-laib-operator
grep -qx "$OPERATOR_IMAGE" <<<"$(podman inspect "$OPC" --format '{{.ImageName}}')" || { echo "FAIL: the Operator runs another image: $(podman inspect "$OPC" --format '{{.ImageName}}')"; exit 1; }
# the control that can go red: its own folder is there, written by core
[ "$(podman cp "$OPC:/operator/canary" - | tar -xO)" = mine ] || { echo "FAIL: the Operator does not see its own folder"; exit 1; }
# what it must not reach, by every path it has on the host
for f in /mnt/data/laib/age.key /mnt/data/laib/state/secrets.age /mnt/data/laib/state "$LAIB_HOME/age.key" "$LAIB_HOME/state/secrets.age" "$LAIB_HOME/state" "$LAIB_HOME/journal"; do
  if podman cp "$OPC:$f" - >/dev/null 2>&1; then echo "FAIL: $f is readable inside the Operator's container"; exit 1; fi
done
mounts="$(podman inspect "$OPC" --format '{{range .Mounts}}{{.Source}}={{.Destination}} {{end}}')"
for m in $mounts; do
  src="${m%%=*}"   # a source that is the key or a folder above it
  for k in /mnt/data/laib/age.key "$LAIB_HOME/age.key"; do
    case "$k" in "$src" | "${src%/}/"*) echo "FAIL: the Operator mounts $src, which holds $k"; exit 1;; esac
  done
done
grep -q "/mnt/data/laib/operator=/operator" <<<"$mounts" || { echo "FAIL: the Operator's folder is not its mount: $mounts"; exit 1; }
TOKV="$(podman secret inspect --showsecret --format "{{.SecretData}}" laib.laib-operator.laib-operator.LAIB_TOKEN)"
[ -n "$TOKV" ] || { echo "FAIL: no podman secret with the Operator's token"; exit 1; }
if grep -rqF -- "$TOKV" "$LAIB_QUADLET/laib-operator" || grep -qF -- "$TOKV" <<<"$(podman inspect "$OPC" --format "{{.Config.CreateCommand}}")"; then echo "FAIL: the Operator's token is in its unit or on podman run's argv"; exit 1; fi
LAIB_ACTOR=human:boot "$BIN" operator disable >/dev/null || { echo "FAIL: operator disable exit $?"; exit 1; }
[ "$(systemctl --user is-active laib-operator.service)" != active ] || { echo "FAIL: the Operator still runs after disable"; exit 1; }
# #266: Haus counts what the family uses into usage-metrics, server-side, never who. The image as it
# ships, as a pod reaches usage-metrics (host.containers.internal) with its token as a podman secret.
# A page view is counted in memory and flushed on the next tick or when Haus stops: stopping it is the
# tick here, so the test does not wait five minutes
mkdir -p "$LAIB_HOME/state/services/haus-boot"
cat > "$LAIB_HOME/state/services/haus-boot/laib.yml" <<YML
name: haus-boot
template: {registry: builtin, name: haus-boot, version: 1}
routes: []
secrets:
  - name: HAUS_BOOT_PROXY
  - name: HAUS_BOOT_LAIB
  - name: USAGE_METRICS_TOKEN
    external: true
data:
  - path: \${DATA_DIR}/haus-boot
health:
  http: http://127.0.0.1:18650/healthz
ports: []
YML
cat > "$LAIB_HOME/state/services/haus-boot/values.yml" <<YML
IMAGE: ghcr.io/mdopp/haus:latest
PROXY: !secret HAUS_BOOT_PROXY
LAIB: !secret HAUS_BOOT_LAIB
USAGE_TOKEN: !secret USAGE_METRICS_TOKEN
YML
cat > "$LAIB_HOME/state/services/haus-boot/haus-boot.container" <<'UNIT'
[Unit]
Description=Haus boot test (#266)

[Container]
Image=${IMAGE}
PublishPort=127.0.0.1:18650:8650
Environment=HAUS_LISTEN=0.0.0.0:8650
Environment=LAIB_URL=http://127.0.0.1:1
Environment=LAIB_TOKEN=${LAIB}
Environment=HAUS_PROXY_SECRET=${PROXY}
Environment=HAUS_DATA=/data
Environment=HAUS_USAGE_URL=http://host.containers.internal:8094
Environment=HAUS_USAGE_TOKEN=${USAGE_TOKEN}
Volume=${DATA_DIR}/haus-boot:/data:Z

[Service]
Restart=no

[Install]
WantedBy=default.target
UNIT
LAIB_ACTOR=human:boot "$BIN" apply --reason "boot test: haus" || { echo "apply haus exit $?"; journalctl --user -u haus-boot.service --no-pager | tail -30; exit 1; }
HP="$(podman secret inspect --showsecret --format "{{.SecretData}}" laib.haus-boot.haus-boot.HAUS_PROXY_SECRET)"
if grep -rqF -- "$UMTOK" "$LAIB_QUADLET/haus-boot"; then echo "FAIL: usage-metrics' token is in Haus's unit"; exit 1; fi
for _ in 1 2; do curl -fsS -o /dev/null -H "X-Haus-Proxy: $HP" -H "Remote-User: anna" -H "Remote-Name: Anna" "http://127.0.0.1:18650/start?von=widget.device"; done
systemctl --user stop haus-boot.service
curl -fsS -H @"$WORK/um.auth" "$UM/summary?days=1" | python3 -c '
import sys, json
s = json.load(sys.stdin)
haus = {e["event"]: e["count"] for a in s["apps"] if a["app"] == "haus" for e in a["events"]}
assert haus.get("seite.start") == 2 and haus.get("von.widget.device") == 2, s
assert "anna" not in json.dumps(s).lower(), "who looked must never reach usage-metrics"'
echo "boot test ok: whoami answered through a real Quadlet unit, and the operator restarted it for real; usage-metrics counted with its token only and kept the count across a restart, kept a miss and forgot it past its 30 days; Haus counted a page view and a widget tap into it, never who; the Operator ran in its own container and saw its folder, not the age key, secrets.age or the state repo"
