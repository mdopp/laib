#!/usr/bin/env bash
# End-to-end run of the CLI against the golden state with shims for
# systemctl, journalctl, podman, restic and sudo. No box needed; the real
# boot job (scripts/boot-test.sh) covers Quadlet itself.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="${LAIB_BIN:-$ROOT/laib}"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
mkdir -p "$WORK/bin"
cat > "$WORK/bin/systemctl" <<'SH'
#!/usr/bin/env sh
echo "$@" >> "${SYSTEMCTL_LOG:-/dev/null}"
case "$*" in *is-active*) echo active;; esac
exit 0
SH
cat > "$WORK/bin/journalctl" <<'SH'
#!/usr/bin/env sh
echo "shim journal for $*"
SH
cat > "$WORK/bin/podman" <<'SH'
#!/usr/bin/env sh
case "$1" in
  exec) shift; c=$1; shift; echo "exec in $c: $*";;
  inspect) echo "running exit=0 image=shim";;
esac
exit 0
SH
cat > "$WORK/bin/restic" <<'SH'
#!/usr/bin/env sh
echo "restic $@" >> "${SYSTEMCTL_LOG:-/dev/null}"
exit 0
SH
cat > "$WORK/bin/sudo" <<'SH'
#!/usr/bin/env sh
echo "sudo $@" >> "${SYSTEMCTL_LOG:-/dev/null}"
exit 0
SH
chmod +x "$WORK/bin/"*
export PATH="$WORK/bin:$PATH" SYSTEMCTL_LOG="$WORK/systemctl.log"
export LAIB_HOME="$WORK/home" LAIB_QUADLET="$WORK/quadlet" LAIB_SYSTEMD="$WORK/systemd"
fail() { echo "FAIL: $*"; "$BIN" journal 2>/dev/null | tail -5; exit 1; }
j() { "$BIN" journal --json | python3 -c "import sys,json; es=json.load(sys.stdin); $1"; }

# --- 1. init and the golden state
LAIB_ACTOR=human:e2e "$BIN" init --domain e2e.home --lan-ip 10.0.0.2 --data-dir "$WORK/data" >/dev/null   # apply creates data paths; /mnt/data is not writable on a runner
cp -r "$ROOT/testdata/golden/basic/state/services/." "$LAIB_HOME/state/services/"
cp "$ROOT/testdata/golden/basic/state/box.yml" "$ROOT/testdata/golden/basic/state/policy.yml" "$LAIB_HOME/state/"
sed -i "s|^dataDir: .*|dataDir: $WORK/data|" "$LAIB_HOME/state/box.yml"   # apply creates data paths; the golden /mnt/data is not writable on a runner
sed -i '/^health:/,/^[a-z]/{/http:/d}' "$LAIB_HOME"/state/services/*/laib.yml   # nothing listens here
echo "cf-token" | LAIB_ACTOR=human:e2e "$BIN" secret set DNS_API_TOKEN >/dev/null

# --- 2. a tree proposal by ops, approved by the human (#1)
LAIB_ACTOR=ops "$BIN" propose "e2e: three services" >/dev/null
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
if LAIB_ACTOR=ops "$BIN" approve 1 >/dev/null 2>&1; then fail "ops approved its own proposal"; fi
LAIB_ACTOR=human:e2e "$BIN" approve 1 >/dev/null || fail "approve #1 exit $?"
test -f "$LAIB_QUADLET/whoami/whoami.container" || fail "unit not written"
grep -q "cf-token" "$LAIB_QUADLET/caddy/caddy.container" || fail "secret not rendered on the box"
if grep -rq "cf-token" "$LAIB_HOME/state"; then fail "secret in state repo"; fi
grep -q "daemon-reload" "$SYSTEMCTL_LOG" && grep -q "restart whoami.service" "$SYSTEMCTL_LOG" && grep -q "restart auth-authelia.service" "$SYSTEMCTL_LOG" || fail "systemctl not driven"
grep -q "sudo nft -f" "$SYSTEMCTL_LOG" || fail "nftables not loaded"
grep -q "X-Laib-Proxy" "$LAIB_HOME/render/caddy/Caddyfile" || fail "laib route missing"

# --- 3. the read verbs after a clean apply
j 'r=es and None' >/dev/null
"$BIN" status --json | python3 -c 'import sys,json; r=json.load(sys.stdin)["ready"]; assert r["state"]=="bereit" and r["red"]==0 and r["drift"]==0, r'
"$BIN" plan | grep -q "Keine Änderung" || fail "plan not empty after apply"
"$BIN" diag --json | python3 -c 'import sys,json; f=json.load(sys.stdin); assert not [x for x in f if x["severity"]=="rot"], f'
"$BIN" logs whoami | grep -q "shim journal" || fail "logs"

# --- 4. a direct human change, then rollback
sed -i 's/WHOAMI_NAME=\${SERVICE}/WHOAMI_NAME=changed-${SERVICE}/' "$LAIB_HOME/state/services/whoami/whoami.container"
LAIB_ACTOR=human:e2e "$BIN" apply --reason "e2e change" >/dev/null || fail "apply exit $?"
grep -q "changed-whoami" "$LAIB_QUADLET/whoami/whoami.container" || fail "change not applied"
LAIB_ACTOR=human:e2e "$BIN" rollback >/dev/null || fail "rollback exit $?"
grep -q "changed-whoami" "$LAIB_QUADLET/whoami/whoami.container" && fail "rollback did not revert"

# --- 5. a values proposal by ops: class values is auto, laib approves itself (#2)
LAIB_ACTOR=ops "$BIN" propose --set services/whoami/values.yml:IMAGE=docker.io/traefik/whoami:v1.11 "e2e: newer whoami" >/dev/null || fail "auto propose exit $?"
grep -q "whoami:v1.11" "$LAIB_QUADLET/whoami/whoami.container" || fail "--set proposal not applied"
j 'assert any(e["actor"]=="laib" and e["verb"]=="approve" for e in es), "laib did not approve"'
if LAIB_ACTOR=human:e2e "$BIN" approve 2 >/dev/null 2>&1; then fail "an auto-approved proposal was approved twice"; fi
OUT="$(LAIB_ACTOR=ops "$BIN" propose --add whoami --name whoami2 IMAGE=docker.io/traefik/whoami:v1.11 "e2e: second whoami" 2>&1 || true)"
echo "$OUT" | grep -q "already published" || fail "duplicate host not refused: $OUT"

# --- 6. secret rotation
OLD="$(grep -o 'TOKEN=[^ ]*' "$LAIB_QUADLET/whoami/whoami.container")"
LAIB_ACTOR=human:e2e "$BIN" secret rotate WHOAMI_TOKEN >/dev/null || fail "rotate exit $?"
NEW="$(grep -o 'TOKEN=[^ ]*' "$LAIB_QUADLET/whoami/whoami.container")"
[ "$OLD" != "$NEW" ] || fail "rotate changed nothing"
if LAIB_ACTOR=ops "$BIN" secret rotate WHOAMI_TOKEN >/dev/null 2>&1; then fail "ops rotated"; fi

# --- 7. template update: a proposal (#3); the template brings its health URL back,
#        nothing listens, so the human's approval ends red (exit 2) with files applied
LAIB_ACTOR=ops "$BIN" template update whoami >/dev/null || fail "template update exit $?"
LAIB_ACTOR=human:e2e "$BIN" approve 3 >/dev/null && CODE=0 || CODE=$?
[ "$CODE" = 2 ] || fail "approve #3 expected exit 2 (red verify), got $CODE"
grep -q "whoami:v1.11" "$LAIB_HOME/state/services/whoami/values.yml" || fail "values.yml not kept on template update"
j 'reds=[e for e in es if e["result"]=="red"]; assert any("health" in e.get("detail","") for e in reds), "red verify not journaled: %r" % reds'
sed -i '/^health:/,/^[a-z]/{/http:/d}' "$LAIB_HOME/state/services/whoami/laib.yml"
LAIB_ACTOR=human:e2e "$BIN" apply whoami --reason "e2e: health stripped again" >/dev/null || fail "re-apply exit $?"

# --- 8. converge: a hand edit on the box is put back
echo "# hand edit" >> "$LAIB_QUADLET/whoami/whoami.container"
"$BIN" status --json | python3 -c 'import sys,json; assert json.load(sys.stdin)["ready"]["drift"]==1'
LAIB_ACTOR=laib "$BIN" converge >/dev/null || fail "converge exit $?"
grep -q "hand edit" "$LAIB_QUADLET/whoami/whoami.container" && fail "converge left the hand edit"
LAIB_ACTOR=laib "$BIN" converge | grep -q "konvergent" || fail "second converge not idle"

# --- 9. the operator verbs (ADR 0013): look unbounded, correct inside the envelope
mkdir -p "$WORK/data/whoami/cfg"; echo "key=value" > "$WORK/data/whoami/cfg/app.conf"
sed -i "s#^data: \[\]#data:\n  - path: $WORK/data/whoami/cfg#" "$LAIB_HOME/state/services/whoami/laib.yml"
LAIB_ACTOR=ops "$BIN" inspect whoami | grep -q "container systemd-whoami" || fail "inspect"
LAIB_ACTOR=ops "$BIN" read whoami "$WORK/data/whoami/cfg/app.conf" | grep -q "key=value" || fail "read"
if LAIB_ACTOR=ops "$BIN" read whoami /etc/passwd >/dev/null 2>&1; then fail "read escaped the jail"; fi
echo "key=new" | LAIB_ACTOR=ops "$BIN" write whoami "$WORK/data/whoami/cfg/app.conf" >/dev/null 2>&1 || fail "write exit $?"
grep -q "key=new" "$WORK/data/whoami/cfg/app.conf" || fail "write content"
LAIB_ACTOR=ops "$BIN" exec whoami -- cat /etc/hostname | grep -q "exec in systemd-whoami: cat /etc/hostname" || fail "exec"
LAIB_ACTOR=ops "$BIN" restart whoami | grep -q "grün" || fail "restart"
if LAIB_ACTOR=resident "$BIN" exec whoami -- id >/dev/null 2>&1; then fail "resident may exec"; fi
# budget: write + exec + restart are three; five more reach RepairBudget (8); the ninth is refused
for i in 1 2 3 4 5; do LAIB_ACTOR=ops "$BIN" exec whoami -- true >/dev/null 2>&1 || true; done
if LAIB_ACTOR=ops "$BIN" exec whoami -- true >/dev/null 2>&1; then fail "budget not enforced"; fi
LAIB_ACTOR=ops "$BIN" escalate whoami "budget spent, needs a look" | grep -q "eskaliert" || fail "escalate"
[ "$(grep -c 'restic backup --tag pre-repair' "$SYSTEMCTL_LOG")" = 1 ] || fail "pre-repair snapshot expected exactly once per hour"

# --- 10. the operator against a real laib serve and a scripted model
cat > "$WORK/bin/fake-model.py" <<'PY2'
import json, sys
from http.server import BaseHTTPRequestHandler, HTTPServer
# a scripted model on the typed surface (#53): inspect, a refused call
# (a service that does not exist), restart, note, then stop
SCRIPT = [
  {"role":"assistant","tool_calls":[{"id":"1","type":"function","function":{"name":"inspect","arguments":json.dumps({"service":"caddy"})}}]},
  {"role":"assistant","tool_calls":[{"id":"2","type":"function","function":{"name":"restart","arguments":json.dumps({"service":"nas"})}}]},
  {"role":"assistant","tool_calls":[{"id":"3","type":"function","function":{"name":"restart","arguments":json.dumps({"service":"caddy"})}}]},
  {"role":"assistant","tool_calls":[{"id":"4","type":"function","function":{"name":"note","arguments":json.dumps({"kind":"incident","text":"caddy war rot, Neustart, jetzt grün."})}}]},
  {"role":"assistant","content":"fertig"},
]
i = 0
class H(BaseHTTPRequestHandler):
    def log_message(self, *a): pass
    def do_POST(self):
        global i
        n = int(self.headers.get("Content-Length", 0)); body = json.loads(self.rfile.read(n))
        names = [t["function"]["name"] for t in body["tools"]]
        # the incident menu for the ops hand: typed, small, without approve/apply/converge
        assert "restart" in names and "note" in names and "diagnose" in names, names
        assert not any(n.startswith("laib_") for n in names) and len(names) <= 8, names
        svc = body["tools"][names.index("restart")]["function"]["parameters"]["properties"]["service"]
        assert "caddy" in svc.get("enum", []), svc
        if i == 2:  # the refused call came back as a tool message, not as an execution
            last = body["messages"][-1]; assert last["role"] == "tool" and "nicht ausgef" in last["content"] and "nas" in last["content"], last
        m = SCRIPT[min(i, len(SCRIPT)-1)]; i += 1
        out = json.dumps({"choices":[{"message": m}]}).encode()
        self.send_response(200); self.send_header("Content-Type","application/json"); self.send_header("Content-Length", str(len(out))); self.end_headers(); self.wfile.write(out)
HTTPServer(("127.0.0.1", int(sys.argv[1])), H).serve_forever()
PY2
python3 "$WORK/bin/fake-model.py" 5977 & FAKE=$!
LAIB_LISTEN=127.0.0.1:5976 "$BIN" serve >/dev/null 2>&1 & SERVE=$!
sleep 1
OPS_TOKEN="$(LAIB_ACTOR=human:e2e "$BIN" token mint ops --note e2e --json | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')"
OPERATOR="${LAIB_OPERATOR_BIN:-$ROOT/laib-operator}"
LAIB_MCP_URL=http://127.0.0.1:5976/mcp LAIB_TOKEN="$OPS_TOKEN" MODEL_URL=http://127.0.0.1:5977/v1 OPERATOR_HOME="$WORK/op" "$OPERATOR" -once event:caddy > "$WORK/op.json" || { echo "FAIL: operator exit $?"; cat "$WORK/op.json"; kill $FAKE $SERVE; exit 1; }
# laibctl: the Werkstatt's `laib` shell command against the same server
printf '%s' "$OPS_TOKEN" > "$WORK/token"; chmod 600 "$WORK/token"
LAIB_MCP_URL=http://127.0.0.1:5976/mcp LAIB_TOKEN_FILE="$WORK/token" python3 "$ROOT/werkstatt/laibctl.py" | grep -q "^status" || fail "laibctl list"
LAIB_MCP_URL=http://127.0.0.1:5976/mcp LAIB_TOKEN_FILE="$WORK/token" python3 "$ROOT/werkstatt/laibctl.py" status | python3 -c 'import sys,json; assert json.load(sys.stdin)["ready"]["state"]' || fail "laibctl status"
if LAIB_MCP_URL=http://127.0.0.1:5976/mcp LAIB_TOKEN_FILE="$WORK/none" python3 "$ROOT/werkstatt/laibctl.py" status >/dev/null 2>&1; then fail "laibctl without token got through"; fi
kill $FAKE $SERVE 2>/dev/null; wait $FAKE $SERVE 2>/dev/null || true
python3 -c 'import sys,json; s=json.load(open(sys.argv[1])); assert s["outcome"]=="green" and len(s["calls"])==3, s' "$WORK/op.json"
j 'notes=[e for e in es if e["verb"]=="note" and e["actor"]=="ops"]; assert notes and "jetzt grün" in notes[-1]["detail"], notes'
j 'assert any(e["verb"]=="restart" and e["actor"]=="ops" and e.get("service")=="caddy" for e in es), "operator restart not journaled"'
test -s "$WORK/op/incidents.jsonl" || fail "incidents.jsonl not written"
# operator enable: needs a model; mints the token into secrets.age, renders and enables the unit
if LAIB_ACTOR=human:e2e "$BIN" operator enable >/dev/null 2>&1; then fail "operator enabled without a model"; fi
printf 'model:\n  url: http://127.0.0.1:5977/v1\n' >> "$LAIB_HOME/state/box.yml"
LAIB_ACTOR=human:e2e "$BIN" operator enable --report-at 06:30 >/dev/null || fail "operator enable exit $?"
grep -q "MODEL_URL=http://127.0.0.1:5977/v1" "$LAIB_SYSTEMD/laib-operator.service" || fail "model url not rendered into the unit"
test -f "$LAIB_SYSTEMD/laib-operator.service" || fail "operator unit not rendered"
grep -q "OPERATOR_REPORT_AT=06:30" "$LAIB_SYSTEMD/laib-operator.service" || fail "report time not rendered"
grep -q "LAIB_TOKEN=laib_ops_" "$LAIB_SYSTEMD/laib-operator.service" || fail "ops token not in the unit"
[ "$(stat -c %a "$LAIB_SYSTEMD/laib-operator.service")" = 600 ] || fail "unit with a secret is not 0600"
grep -q "enable --now laib-operator.service" "$SYSTEMCTL_LOG" || fail "operator unit not enabled"
if LAIB_ACTOR=ops "$BIN" operator disable >/dev/null 2>&1; then fail "ops disabled the operator"; fi

# --- 11. the bote and frage hooks render their configs from values and secrets
HOOKDIR="$WORK/hooks"; mkdir -p "$HOOKDIR"
( cd "$ROOT/templates/bote" && LAIB_DATA_DIR="$HOOKDIR" LAIB_SERVICE=bote LAIB_DOMAIN=e2e.home LAIB_TZ=UTC LAIB_LAN_IP=10.0.0.2 \
  MODEL_URL=http://127.0.0.1:5977/v1 LAIB_MCP_URL=http://host.containers.internal:5900/mcp \
  SIGNAL_ACCOUNT=+491 SIGNAL_ALLOWED_USERS=+492 ADMIN_NUMBER=+492 MODEL_NAME=gemma-4-e4b THINKING_MODEL=qwen \
  LAIB_SECRET_BOTE_LAIB_TOKEN=laib_bote_x LAIB_SECRET_BOTE_HUMAN_TOKEN=laib_human_y LAIB_SECRET_BOTE_MODEL_KEY= HAUSHALT_MCP_URL= ./post-deploy >/dev/null ) || fail "bote hook"
grep -q "SIGNAL_ACCOUNT=+491" "$HOOKDIR/bote/hermes/.env" || fail "bote .env"
grep -q 'Authorization: "Bearer ${LAIB_TOKEN}"' "$HOOKDIR/bote/hermes/config.yaml" || fail "bote mcp header keeps the env reference"
grep -q "base_url: http://127.0.0.1:5977/v1" "$HOOKDIR/bote/hermes/config.yaml" || fail "bote model url"
grep -q "trust: untrusted" "$HOOKDIR/bote/hermes/config.yaml" || fail "bote admin entry must be untrusted"
grep -q "+492" "$HOOKDIR/bote/hermes/SOUL.md" || fail "bote soul names the admin"
[ "$(stat -c %a "$HOOKDIR/bote/hermes/.env")" = 600 ] || fail "bote .env not 0600"
( cd "$ROOT/templates/frage" && LAIB_DATA_DIR="$HOOKDIR" LAIB_SECRET_SEARX_SECRET=s3 ./post-deploy >/dev/null ) || fail "frage hook"
grep -q "formats: \[html, json\]" "$HOOKDIR/frage/searxng/settings.yml" || fail "frage searx json format"

# --- 12. remove: units stopped, files gone, data kept, state without the service
if LAIB_ACTOR=ops "$BIN" remove whoami >/dev/null 2>&1; then fail "ops removed a service"; fi
if LAIB_ACTOR=human:e2e "$BIN" remove caddy >/dev/null 2>&1; then fail "a role holder was removed"; fi
LAIB_ACTOR=human:e2e "$BIN" remove whoami | grep -q "Daten bleiben" || fail "remove exit $?"
test -f "$LAIB_QUADLET/whoami/whoami.container" && fail "remove left the unit file"
test -d "$LAIB_HOME/state/services/whoami" && fail "remove left the state folder"
test -f "$WORK/data/whoami/cfg/app.conf" || fail "remove deleted data"
grep -q "stop whoami.service" "$SYSTEMCTL_LOG" || fail "remove did not stop the unit"
LAIB_ACTOR=human:e2e "$BIN" rollback >/dev/null || fail "rollback after remove exit $?"
test -f "$LAIB_QUADLET/whoami/whoami.container" || fail "rollback did not bring whoami back"

# --- 13. the journal holds the whole story
j 'verbs=[e["verb"] for e in es]; assert all(v in verbs for v in ["apply","rollback","approve","converge","exec","write","restart","escalate","snapshot","note","operator"]), verbs'
echo "e2e ok: $(wc -l < "$SYSTEMCTL_LOG") systemctl calls, $(ls "$LAIB_HOME"/journal | wc -l) journal file(s)"
