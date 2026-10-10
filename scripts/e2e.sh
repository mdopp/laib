#!/usr/bin/env bash
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
# End-to-end run of the CLI against the golden state with shims for
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
# systemctl, journalctl, podman, restic and sudo. No box needed; the real
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
# boot job (scripts/boot-test.sh) covers Quadlet itself.
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
set -euo pipefail
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
BIN="${LAIB_BIN:-$ROOT/laib}"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
WORK="$(mktemp -d)"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
trap 'rm -rf "$WORK"' EXIT
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
mkdir -p "$WORK/bin"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
cat > "$WORK/bin/systemctl" <<'SH'
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
#!/usr/bin/env sh
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
echo "$@" >> "${SYSTEMCTL_LOG:-/dev/null}"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
case "$*" in *is-active*) echo active;; esac
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
exit 0
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
SH
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
cat > "$WORK/bin/journalctl" <<'SH'
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
#!/usr/bin/env sh
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
echo "shim journal for $*"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
SH
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
cat > "$WORK/bin/podman" <<'SH'
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
#!/usr/bin/env sh
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
case "$1" in
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
  exec) shift; c=$1; shift; echo "exec in $c: $*";;
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
  inspect) echo "running exit=0 image=shim";;
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
esac
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
exit 0
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
SH
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
cat > "$WORK/bin/restic" <<'SH'
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
#!/usr/bin/env sh
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
echo "restic $@" >> "${SYSTEMCTL_LOG:-/dev/null}"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
exit 0
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
SH
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
cat > "$WORK/bin/sudo" <<'SH'
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
#!/usr/bin/env sh
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
echo "sudo $@" >> "${SYSTEMCTL_LOG:-/dev/null}"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
exit 0
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
SH
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
chmod +x "$WORK/bin/"*
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
export PATH="$WORK/bin:$PATH" SYSTEMCTL_LOG="$WORK/systemctl.log"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
export LAIB_HOME="$WORK/home" LAIB_QUADLET="$WORK/quadlet" LAIB_SYSTEMD="$WORK/systemd"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
fail() { echo "FAIL: $*"; "$BIN" journal 2>/dev/null | tail -5; exit 1; }
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
j() { "$BIN" journal --json | python3 -c "import sys,json; es=json.load(sys.stdin); $1"; }
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"

"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
# --- 1. init and the golden state
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
LAIB_ACTOR=human:e2e "$BIN" init --domain e2e.home --lan-ip 10.0.0.2 --data-dir "$WORK/data" >/dev/null   # apply creates data paths; /mnt/data is not writable on a runner
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
cp -r "$ROOT/testdata/golden/basic/state/services/." "$LAIB_HOME/state/services/"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
cp "$ROOT/testdata/golden/basic/state/box.yml" "$ROOT/testdata/golden/basic/state/policy.yml" "$LAIB_HOME/state/"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
sed -i "s|^dataDir: .*|dataDir: $WORK/data|" "$LAIB_HOME/state/box.yml"   # apply creates data paths; the golden /mnt/data is not writable on a runner
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
sed -i '/^health:/,/^[a-z]/{/http:/d}' "$LAIB_HOME"/state/services/*/laib.yml   # nothing listens here
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
echo "cf-token" | LAIB_ACTOR=human:e2e "$BIN" secret set DNS_API_TOKEN >/dev/null
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"

"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
# --- 2. a tree proposal by ops, approved by the human (#1)
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
LAIB_ACTOR=ops "$BIN" propose "e2e: three services" >/dev/null
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
if LAIB_ACTOR=ops "$BIN" approve 1 >/dev/null 2>&1; then fail "ops approved its own proposal"; fi
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
LAIB_ACTOR=human:e2e "$BIN" approve 1 >/dev/null || fail "approve #1 exit $?"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
test -f "$LAIB_QUADLET/whoami/whoami.container" || fail "unit not written"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
grep -q "cf-token" "$LAIB_QUADLET/caddy/caddy.container" || fail "secret not rendered on the box"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
if grep -rq "cf-token" "$LAIB_HOME/state"; then fail "secret in state repo"; fi
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
grep -q "daemon-reload" "$SYSTEMCTL_LOG" && grep -q "restart whoami.service" "$SYSTEMCTL_LOG" && grep -q "restart auth-authelia.service" "$SYSTEMCTL_LOG" || fail "systemctl not driven"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
grep -q "sudo nft -f" "$SYSTEMCTL_LOG" || fail "nftables not loaded"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
grep -q "X-Laib-Proxy" "$LAIB_HOME/render/caddy/Caddyfile" || fail "laib route missing"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"

"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
# --- 3. the read verbs after a clean apply
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
j 'r=es and None' >/dev/null
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
"$BIN" status --json | python3 -c 'import sys,json; r=json.load(sys.stdin)["ready"]; assert r["state"]=="bereit" and r["red"]==0 and r["drift"]==0, r'
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
"$BIN" plan | grep -q "Keine Änderung" || fail "plan not empty after apply"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
"$BIN" diag --json | python3 -c 'import sys,json; f=json.load(sys.stdin); assert not [x for x in f if x["severity"]=="rot"], f'
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
"$BIN" logs whoami | grep -q "shim journal" || fail "logs"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"

"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
# --- 4. a direct human change, then rollback
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
sed -i 's/WHOAMI_NAME=\${SERVICE}/WHOAMI_NAME=changed-${SERVICE}/' "$LAIB_HOME/state/services/whoami/whoami.container"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
LAIB_ACTOR=human:e2e "$BIN" apply --reason "e2e change" >/dev/null || fail "apply exit $?"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
grep -q "changed-whoami" "$LAIB_QUADLET/whoami/whoami.container" || fail "change not applied"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
LAIB_ACTOR=human:e2e "$BIN" rollback >/dev/null || fail "rollback exit $?"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
grep -q "changed-whoami" "$LAIB_QUADLET/whoami/whoami.container" && fail "rollback did not revert"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"

"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
# --- 5. a values proposal by ops: class values is auto, laib approves itself (#2)
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
LAIB_ACTOR=ops "$BIN" propose --set services/whoami/values.yml:IMAGE=docker.io/traefik/whoami:v1.11 "e2e: newer whoami" >/dev/null || fail "auto propose exit $?"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
grep -q "whoami:v1.11" "$LAIB_QUADLET/whoami/whoami.container" || fail "--set proposal not applied"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
j 'assert any(e["actor"]=="laib" and e["verb"]=="approve" for e in es), "laib did not approve"'
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
if LAIB_ACTOR=human:e2e "$BIN" approve 2 >/dev/null 2>&1; then fail "an auto-approved proposal was approved twice"; fi
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
OUT="$(LAIB_ACTOR=ops "$BIN" propose --add whoami --name whoami2 IMAGE=docker.io/traefik/whoami:v1.11 "e2e: second whoami" 2>&1 || true)"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
echo "$OUT" | grep -q "already published" || fail "duplicate host not refused: $OUT"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"

"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
# --- 6. secret rotation
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
OLD="$(grep -o 'TOKEN=[^ ]*' "$LAIB_QUADLET/whoami/whoami.container")"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
LAIB_ACTOR=human:e2e "$BIN" secret rotate WHOAMI_TOKEN >/dev/null || fail "rotate exit $?"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
NEW="$(grep -o 'TOKEN=[^ ]*' "$LAIB_QUADLET/whoami/whoami.container")"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
[ "$OLD" != "$NEW" ] || fail "rotate changed nothing"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
if LAIB_ACTOR=ops "$BIN" secret rotate WHOAMI_TOKEN >/dev/null 2>&1; then fail "ops rotated"; fi
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"

"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
# --- 7. template update: a proposal (#3); the template brings its health URL back,
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
#        nothing listens, so the human's approval ends red (exit 2) with files applied
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
LAIB_ACTOR=ops "$BIN" template update whoami >/dev/null || fail "template update exit $?"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
LAIB_ACTOR=human:e2e "$BIN" approve 3 >/dev/null && CODE=0 || CODE=$?
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
[ "$CODE" = 2 ] || fail "approve #3 expected exit 2 (red verify), got $CODE"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
grep -q "whoami:v1.11" "$LAIB_HOME/state/services/whoami/values.yml" || fail "values.yml not kept on template update"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
j 'reds=[e for e in es if e["result"]=="red"]; assert any("health" in e.get("detail","") for e in reds), "red verify not journaled: %r" % reds'
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
sed -i '/^health:/,/^[a-z]/{/http:/d}' "$LAIB_HOME/state/services/whoami/laib.yml"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
LAIB_ACTOR=human:e2e "$BIN" apply whoami --reason "e2e: health stripped again" >/dev/null || fail "re-apply exit $?"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"

"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
# --- 8. converge: a hand edit on the box is put back
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
echo "# hand edit" >> "$LAIB_QUADLET/whoami/whoami.container"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
"$BIN" status --json | python3 -c 'import sys,json; assert json.load(sys.stdin)["ready"]["drift"]==1'
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
LAIB_ACTOR=laib "$BIN" converge >/dev/null || fail "converge exit $?"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
grep -q "hand edit" "$LAIB_QUADLET/whoami/whoami.container" && fail "converge left the hand edit"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
LAIB_ACTOR=laib "$BIN" converge | grep -q "konvergent" || fail "second converge not idle"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"

"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
# --- 9. the operator verbs (ADR 0013): look unbounded, correct inside the envelope
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
mkdir -p "$WORK/data/whoami/cfg"; echo "key=value" > "$WORK/data/whoami/cfg/app.conf"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
sed -i "s#^data: \[\]#data:\n  - path: $WORK/data/whoami/cfg#" "$LAIB_HOME/state/services/whoami/laib.yml"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
LAIB_ACTOR=ops "$BIN" inspect whoami | grep -q "container systemd-whoami" || fail "inspect"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
LAIB_ACTOR=ops "$BIN" read whoami "$WORK/data/whoami/cfg/app.conf" | grep -q "key=value" || fail "read"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
if LAIB_ACTOR=ops "$BIN" read whoami /etc/passwd >/dev/null 2>&1; then fail "read escaped the jail"; fi
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
echo "key=new" | LAIB_ACTOR=ops "$BIN" write whoami "$WORK/data/whoami/cfg/app.conf" >/dev/null 2>&1 || fail "write exit $?"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
grep -q "key=new" "$WORK/data/whoami/cfg/app.conf" || fail "write content"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
LAIB_ACTOR=ops "$BIN" exec whoami -- cat /etc/hostname | grep -q "exec in systemd-whoami: cat /etc/hostname" || fail "exec"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
LAIB_ACTOR=ops "$BIN" restart whoami | grep -q "grün" || fail "restart"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
if LAIB_ACTOR=resident "$BIN" exec whoami -- id >/dev/null 2>&1; then fail "resident may exec"; fi
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
# budget: write + exec + restart are three; five more reach RepairBudget (8); the ninth is refused
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
for i in 1 2 3 4 5; do LAIB_ACTOR=ops "$BIN" exec whoami -- true >/dev/null 2>&1 || true; done
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
if LAIB_ACTOR=ops "$BIN" exec whoami -- true >/dev/null 2>&1; then fail "budget not enforced"; fi
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
LAIB_ACTOR=ops "$BIN" escalate whoami "budget spent, needs a look" | grep -q "eskaliert" || fail "escalate"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
[ "$(grep -c 'restic backup --tag pre-repair' "$SYSTEMCTL_LOG")" = 1 ] || fail "pre-repair snapshot expected exactly once per hour"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"

"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
# --- 10. the operator against a real laib serve and a scripted model
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
cat > "$WORK/bin/fake-model.py" <<'PY2'
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
import json, sys
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
from http.server import BaseHTTPRequestHandler, HTTPServer
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
# a scripted model: inspect, restart, note, then stop
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
SCRIPT = [
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
  {"role":"assistant","tool_calls":[{"id":"1","type":"function","function":{"name":"laib_inspect","arguments":json.dumps({"args":"caddy"})}}]},
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
  {"role":"assistant","tool_calls":[{"id":"2","type":"function","function":{"name":"laib_restart","arguments":json.dumps({"args":"caddy"})}}]},
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
  {"role":"assistant","tool_calls":[{"id":"3","type":"function","function":{"name":"laib_note","arguments":json.dumps({"args":"--kind incident \"whoami war rot, Neustart, jetzt grün.\""})}}]},
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
  {"role":"assistant","content":"fertig"},
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
]
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
i = 0
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
class H(BaseHTTPRequestHandler):
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
    def log_message(self, *a): pass
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
    def do_POST(self):
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
        global i
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
        n = int(self.headers.get("Content-Length", 0)); body = json.loads(self.rfile.read(n))
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
        assert body["tools"], "no tools"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
        m = SCRIPT[min(i, len(SCRIPT)-1)]; i += 1
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
        out = json.dumps({"choices":[{"message": m}]}).encode()
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
        self.send_response(200); self.send_header("Content-Type","application/json"); self.send_header("Content-Length", str(len(out))); self.end_headers(); self.wfile.write(out)
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
HTTPServer(("127.0.0.1", int(sys.argv[1])), H).serve_forever()
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
PY2
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
python3 "$WORK/bin/fake-model.py" 5977 & FAKE=$!
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
LAIB_LISTEN=127.0.0.1:5976 "$BIN" serve >/dev/null 2>&1 & SERVE=$!
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
sleep 1
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
OPS_TOKEN="$(LAIB_ACTOR=human:e2e "$BIN" token mint ops --note e2e --json | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
OPERATOR="${LAIB_OPERATOR_BIN:-$ROOT/laib-operator}"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
LAIB_MCP_URL=http://127.0.0.1:5976/mcp LAIB_TOKEN="$OPS_TOKEN" MODEL_URL=http://127.0.0.1:5977/v1 OPERATOR_HOME="$WORK/op" "$OPERATOR" -once event:caddy > "$WORK/op.json" || { echo "FAIL: operator exit $?"; cat "$WORK/op.json"; kill $FAKE $SERVE; exit 1; }
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
# laibctl: the Werkstatt's `laib` shell command against the same server
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
printf '%s' "$OPS_TOKEN" > "$WORK/token"; chmod 600 "$WORK/token"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
LAIB_MCP_URL=http://127.0.0.1:5976/mcp LAIB_TOKEN_FILE="$WORK/token" python3 "$ROOT/werkstatt/laibctl.py" | grep -q "^status" || fail "laibctl list"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
LAIB_MCP_URL=http://127.0.0.1:5976/mcp LAIB_TOKEN_FILE="$WORK/token" python3 "$ROOT/werkstatt/laibctl.py" status | python3 -c 'import sys,json; assert json.load(sys.stdin)["ready"]["state"]' || fail "laibctl status"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
if LAIB_MCP_URL=http://127.0.0.1:5976/mcp LAIB_TOKEN_FILE="$WORK/none" python3 "$ROOT/werkstatt/laibctl.py" status >/dev/null 2>&1; then fail "laibctl without token got through"; fi
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
kill $FAKE $SERVE 2>/dev/null; wait $FAKE $SERVE 2>/dev/null || true
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
python3 -c 'import sys,json; s=json.load(open(sys.argv[1])); assert s["outcome"]=="green" and len(s["calls"])==3, s' "$WORK/op.json"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
j 'notes=[e for e in es if e["verb"]=="note" and e["actor"]=="ops"]; assert notes and "jetzt grün" in notes[-1]["detail"], notes'
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
j 'assert any(e["verb"]=="restart" and e["actor"]=="ops" and e.get("service")=="caddy" for e in es), "operator restart not journaled"'
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
test -s "$WORK/op/incidents.jsonl" || fail "incidents.jsonl not written"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
# operator enable: needs a model; mints the token into secrets.age, renders and enables the unit
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
if LAIB_ACTOR=human:e2e "$BIN" operator enable >/dev/null 2>&1; then fail "operator enabled without a model"; fi
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
printf 'model:\n  url: http://127.0.0.1:5977/v1\n' >> "$LAIB_HOME/state/box.yml"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
LAIB_ACTOR=human:e2e "$BIN" operator enable --report-at 06:30 >/dev/null || fail "operator enable exit $?"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
grep -q "MODEL_URL=http://127.0.0.1:5977/v1" "$LAIB_SYSTEMD/laib-operator.service" || fail "model url not rendered into the unit"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
test -f "$LAIB_SYSTEMD/laib-operator.service" || fail "operator unit not rendered"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
grep -q "OPERATOR_REPORT_AT=06:30" "$LAIB_SYSTEMD/laib-operator.service" || fail "report time not rendered"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
grep -q "LAIB_TOKEN=laib_ops_" "$LAIB_SYSTEMD/laib-operator.service" || fail "ops token not in the unit"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
[ "$(stat -c %a "$LAIB_SYSTEMD/laib-operator.service")" = 600 ] || fail "unit with a secret is not 0600"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
grep -q "enable --now laib-operator.service" "$SYSTEMCTL_LOG" || fail "operator unit not enabled"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
if LAIB_ACTOR=ops "$BIN" operator disable >/dev/null 2>&1; then fail "ops disabled the operator"; fi
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"

"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
# --- 11. the bote and frage hooks render their configs from values and secrets
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
HOOKDIR="$WORK/hooks"; mkdir -p "$HOOKDIR"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
( cd "$ROOT/templates/bote" && LAIB_DATA_DIR="$HOOKDIR" LAIB_SERVICE=bote LAIB_DOMAIN=e2e.home LAIB_TZ=UTC LAIB_LAN_IP=10.0.0.2 \
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
  MODEL_URL=http://127.0.0.1:5977/v1 LAIB_MCP_URL=http://host.containers.internal:5900/mcp \
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
  SIGNAL_ACCOUNT=+491 SIGNAL_ALLOWED_USERS=+492 ADMIN_NUMBER=+492 MODEL_NAME=gemma-4-e4b THINKING_MODEL=qwen \
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
  LAIB_SECRET_BOTE_LAIB_TOKEN=laib_bote_x LAIB_SECRET_BOTE_HUMAN_TOKEN=laib_human_y LAIB_SECRET_BOTE_MODEL_KEY= HAUSHALT_MCP_URL= ./post-deploy >/dev/null ) || fail "bote hook"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
grep -q "SIGNAL_ACCOUNT=+491" "$HOOKDIR/bote/hermes/.env" || fail "bote .env"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
grep -q 'Authorization: "Bearer ${LAIB_TOKEN}"' "$HOOKDIR/bote/hermes/config.yaml" || fail "bote mcp header keeps the env reference"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
grep -q "base_url: http://127.0.0.1:5977/v1" "$HOOKDIR/bote/hermes/config.yaml" || fail "bote model url"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
grep -q "trust: untrusted" "$HOOKDIR/bote/hermes/config.yaml" || fail "bote admin entry must be untrusted"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
grep -q "+492" "$HOOKDIR/bote/hermes/SOUL.md" || fail "bote soul names the admin"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
[ "$(stat -c %a "$HOOKDIR/bote/hermes/.env")" = 600 ] || fail "bote .env not 0600"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
( cd "$ROOT/templates/frage" && LAIB_DATA_DIR="$HOOKDIR" LAIB_SECRET_SEARX_SECRET=s3 ./post-deploy >/dev/null ) || fail "frage hook"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
grep -q "formats: \[html, json\]" "$HOOKDIR/frage/searxng/settings.yml" || fail "frage searx json format"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"

"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
# --- 12. the journal holds the whole story
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
j 'verbs=[e["verb"] for e in es]; assert all(v in verbs for v in ["apply","rollback","approve","converge","exec","write","restart","escalate","snapshot","note","operator"]), verbs'
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
echo "e2e ok: $(wc -l < "$SYSTEMCTL_LOG") systemctl calls, $(ls "$LAIB_HOME"/journal | wc -l) journal file(s)"
"$BIN" inbox --json | grep -q '"status": "open"' || fail "inbox lists the open proposal"
