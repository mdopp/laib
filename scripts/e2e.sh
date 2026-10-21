#!/usr/bin/env bash
# End-to-end run of the CLI against the golden state with shims for
# systemctl, journalctl, podman, restic and sudo. No box needed; the real
# boot job (scripts/boot-test.sh) covers Quadlet itself.
#
# The shims can be told to fail (#52), so the red paths run too:
#   SHIM_FAIL_RESTART="a.service b.service"  systemctl restart of these exits 1
#   SHIM_FAILED_UNITS="a.service"            is-active says "failed" for these
#   SHIM_RESTIC_FAIL=1                       every restic call exits 1
#   SHIM_PODMAN_EXEC_FAIL=1                  podman exec exits 125
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="${LAIB_BIN:-$ROOT/laib}"
WORK="$(mktemp -d)"
trap 'kill $(cat "$WORK"/*.pid 2>/dev/null) 2>/dev/null || true; rm -rf "$WORK"' EXIT
mkdir -p "$WORK/bin"
cat > "$WORK/bin/systemctl" <<'SH'
#!/usr/bin/env sh
echo "$@" >> "${SYSTEMCTL_LOG:-/dev/null}"
for u in ${SHIM_FAIL_RESTART:-}; do
  case "$*" in *" restart $u"*) echo "Job for $u failed (shim)" >&2; exit 1;; esac
done
# a CoreOS box has no firewalld (#445): systemctl answers as for a unit that is not there
case "$*" in *firewalld*) echo inactive; exit 4;; esac
case "$*" in *is-active*)
  for u in ${SHIM_FAILED_UNITS:-}; do
    case "$*" in *"$u"*) echo failed; exit 3;; esac
  done
  echo active;;
esac
exit 0
SH
cat > "$WORK/bin/journalctl" <<'SH'
#!/usr/bin/env sh
echo "shim journal for $*"
SH
cat > "$WORK/bin/podman" <<'SH'
#!/usr/bin/env sh
# run: a hook in the hooks image (#36) — here it runs on this machine with the env file, same paths
case "$1" in
  unshare) shift; exec "$@";;   # restic in the user namespace (#40): the shim runs it as is
  exec)
    [ -n "${SHIM_PODMAN_EXEC_FAIL:-}" ] && { echo "Error: container is not running (shim)" >&2; exit 125; }
    shift; c=$1; shift; echo "exec in $c: $*";;
  inspect) echo "running exit=0 image=shim";;
  secret) # the box's podman secrets (#224), one file each; a value comes on stdin only
    echo "podman $*" >> "${SYSTEMCTL_LOG:-/dev/null}"; d="${SYSTEMCTL_LOG%/*}/podsec"; mkdir -p "$d"
    case "$2" in
      create) for a; do n=$p; p=$a; done; cat > "$d/$n";;
      ls) ls "$d";;
      inspect) shift 5; for n; do printf '%s "%s"\n' "$n" "$(cat "$d/$n")"; done;;
      rm) rm "$d/$3";;
    esac;;
  run) shift; envf=""; while [ $# -gt 0 ]; do case "$1" in --env-file) envf=$2; shift 2;; -v|-w|--network) shift 2;; --rm) shift;; ghcr.io/*) shift; break;; *) shift;; esac; done
       # a hook (with an env file) runs here; anything else in a container (caddy validate, #50) just succeeds
       [ -n "$envf" ] || { echo "podman run $*" >> "${SYSTEMCTL_LOG:-/dev/null}"; exit 0; }
       echo "podman run hook $*" >> "${SYSTEMCTL_LOG:-/dev/null}"; set -a; . "$envf"; set +a; exec "$@";;
esac
exit 0
SH
cat > "$WORK/bin/restic" <<'SH'
#!/usr/bin/env sh
# RESTIC_RC=1 makes it fail like a repo that is gone (#35: the envelope must refuse then)
if [ "${RESTIC_RC:-0}" != 0 ]; then echo "restic FAIL $@" >> "${SYSTEMCTL_LOG:-/dev/null}"; exit "$RESTIC_RC"; fi
echo "restic $@" >> "${SYSTEMCTL_LOG:-/dev/null}"
[ -n "${SHIM_RESTIC_FAIL:-}" ] && { echo "Fatal: unable to open repository (shim)" >&2; exit 1; }
# a repo exists only after init (#40: backupPrep asks for the sheet's password when it does)
case "$1" in
  init) touch "${SYSTEMCTL_LOG%/*}/restic.initialized";;
  cat) [ -f "${SYSTEMCTL_LOG%/*}/restic.initialized" ] || { echo "Fatal: repository does not exist"; exit 1; };;
esac
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
export LAIB_ETC="$WORK/etc"; bash "$ROOT/scripts/install-box.sh" --box-config >/dev/null   # the box config as every box has it (#281); diag is red without it
# the boot check the install writes beside the binaries (#368); these harnesses run no install
mkdir -p "$LAIB_ETC/systemd/system" && : > "$LAIB_ETC/systemd/system/laib-boot-check.service"
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
grep -q '"status": "open"' <<<"$("$BIN" inbox --json)" || fail "inbox lists the open proposal"
if LAIB_ACTOR=ops "$BIN" approve 1 >/dev/null 2>&1; then fail "ops approved its own proposal"; fi
LAIB_ACTOR=human:e2e "$BIN" approve 1 >/dev/null || fail "approve #1 exit $?"
test -f "$LAIB_QUADLET/whoami/whoami.container" || fail "unit not written"
grep -qx "cf-token" "$WORK/podsec/laib.caddy.caddy.LAIB_DNS_TOKEN" || fail "secret not on the box as a podman secret"
if grep -rq "cf-token" "$LAIB_QUADLET"; then fail "a secret value in a Quadlet unit, which puts it on podman run's argv (#224)"; fi
if grep -rq "cf-token" "$LAIB_HOME/state"; then fail "secret in state repo"; fi
grep -q "daemon-reload" "$SYSTEMCTL_LOG" && grep -q "restart whoami.service" "$SYSTEMCTL_LOG" && grep -q "restart auth-pod.service" "$SYSTEMCTL_LOG" || fail "systemctl not driven"
# a pod's containers restart through the pod unit, never one by one (--exit-policy stop, #36)
if grep -q "restart auth-authelia.service\|restart auth-lldap.service" "$SYSTEMCTL_LOG"; then fail "a pod container was restarted on its own"; fi
grep -q "sudo nft -f" "$SYSTEMCTL_LOG" || fail "nftables not loaded"
grep -q "X-Laib-Proxy" "$LAIB_HOME/render/caddy/Caddyfile" || fail "laib route missing"

# --- 3. the read verbs after a clean apply
j 'r=es and None' >/dev/null
"$BIN" status --json | python3 -c 'import sys,json; r=json.load(sys.stdin)["ready"]; assert r["state"]=="bereit" and r["red"]==0 and r["drift"]==0, r'
grep -q "Keine Änderung" <<<"$("$BIN" plan)" || fail "plan not empty after apply"
"$BIN" diag --json | python3 -c 'import sys,json; f=json.load(sys.stdin); assert not [x for x in f if x["severity"]=="rot"], f'
grep -q "shim journal" <<<"$("$BIN" logs whoami)" || fail "logs"

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
grep -q "already published" <<<"$OUT" || fail "duplicate host not refused: $OUT"

# --- 6. secret rotation
OLD="$(cat "$WORK/podsec/laib.whoami.whoami.TOKEN")"; N=$(wc -l < "$SYSTEMCTL_LOG")
LAIB_ACTOR=human:e2e "$BIN" secret rotate WHOAMI_TOKEN >/dev/null || fail "rotate exit $?"
NEW="$(cat "$WORK/podsec/laib.whoami.whoami.TOKEN")"
[ -n "$OLD" ] && [ "$OLD" != "$NEW" ] || fail "rotate changed nothing"
grep -q "restart whoami.service" <<<"$(tail -n +"$N" "$SYSTEMCTL_LOG")" || fail "a rotated secret did not restart its unit"
if grep -qF -e "$OLD" -e "$NEW" -e cf-token "$SYSTEMCTL_LOG"; then fail "a secret value on an argv (#224)"; fi
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
grep -q "konvergent" <<<"$(LAIB_ACTOR=laib "$BIN" converge)" || fail "second converge not idle"

# --- 9. the operator verbs (ADR 0013): look unbounded, correct inside the envelope
mkdir -p "$WORK/data/whoami/cfg"; echo "key=value" > "$WORK/data/whoami/cfg/app.conf"
sed -i "s#^data: \[\]#data:\n  - path: $WORK/data/whoami/cfg#" "$LAIB_HOME/state/services/whoami/laib.yml"
grep -q "container systemd-whoami" <<<"$(LAIB_ACTOR=ops "$BIN" inspect whoami)" || fail "inspect"
grep -q "key=value" <<<"$(LAIB_ACTOR=ops "$BIN" read whoami "$WORK/data/whoami/cfg/app.conf")" || fail "read"
if LAIB_ACTOR=ops "$BIN" read whoami /etc/passwd >/dev/null 2>&1; then fail "read escaped the jail"; fi
# #35: the envelope fails closed — no snapshot, no write; looking costs no budget; exec stays in the service's own containers
if echo "key=evil" | RESTIC_RC=1 LAIB_ACTOR=ops "$BIN" write whoami "$WORK/data/whoami/cfg/app.conf" >/dev/null 2>&1; then fail "write went ahead although the snapshot failed"; fi
grep -q "key=value" "$WORK/data/whoami/cfg/app.conf" || fail "the refused write changed the file"
if LAIB_ACTOR=ops "$BIN" exec whoami --container systemd-caddy -- true >/dev/null 2>&1; then fail "exec reached another service's container"; fi
grep -q "exec in systemd-whoami: cat /etc/hostname" <<<"$(LAIB_ACTOR=ops "$BIN" look whoami -- cat /etc/hostname)" || fail "look"
if LAIB_ACTOR=ops "$BIN" look whoami -- rm -rf / >/dev/null 2>&1; then fail "look ran a command that changes things"; fi
echo "key=new" | LAIB_ACTOR=ops "$BIN" write whoami "$WORK/data/whoami/cfg/app.conf" >/dev/null 2>&1 || fail "write exit $?"
grep -q "key=new" "$WORK/data/whoami/cfg/app.conf" || fail "write content"
grep -q "exec in systemd-whoami: cat /etc/hostname" <<<"$(LAIB_ACTOR=ops "$BIN" exec whoami -- cat /etc/hostname)" || fail "exec"
grep -q "grün" <<<"$(LAIB_ACTOR=ops "$BIN" restart whoami)" || fail "restart"
if LAIB_ACTOR=resident "$BIN" exec whoami -- id >/dev/null 2>&1; then fail "resident may exec"; fi
# budget: write + exec + restart are three; five more reach RepairBudget (8); the ninth is refused
for i in 1 2 3 4 5; do LAIB_ACTOR=ops "$BIN" exec whoami -- true >/dev/null 2>&1 || true; done
if LAIB_ACTOR=ops "$BIN" exec whoami -- true >/dev/null 2>&1; then fail "budget not enforced"; fi
grep -q "eskaliert" <<<"$(LAIB_ACTOR=ops "$BIN" escalate whoami "budget spent, needs a look")" || fail "escalate"
[ "$(grep -c 'restic backup --tag pre-repair' "$SYSTEMCTL_LOG")" = 1 ] || fail "pre-repair snapshot expected exactly once per hour"

# --- 10. the operator against a real laib serve and a scripted model
cat > "$WORK/bin/fake-model.py" <<'PY2'
import json, sys
from http.server import BaseHTTPRequestHandler, HTTPServer
# a scripted model on the typed surface (#53): inspect, a refused call
# (a service that does not exist), restart, note, then stop
SCRIPT = [
  {"role":"assistant","tool_calls":[{"id":"1","type":"function","function":{"name":"laib_inspect","arguments":json.dumps({"service":"caddy"})}}]},
  {"role":"assistant","tool_calls":[{"id":"2","type":"function","function":{"name":"laib_restart","arguments":json.dumps({"service":"nas"})}}]},
  {"role":"assistant","tool_calls":[{"id":"3","type":"function","function":{"name":"laib_restart","arguments":json.dumps({"service":"caddy"})}}]},
  {"role":"assistant","tool_calls":[{"id":"4","type":"function","function":{"name":"laib_note","arguments":json.dumps({"kind":"incident","text":"caddy war rot, Neustart, jetzt grün."})}}]},
  {"role":"assistant","content":"fertig"},
]
i = 0
class H(BaseHTTPRequestHandler):
    def log_message(self, *a): pass
    def do_POST(self):
        global i
        n = int(self.headers.get("Content-Length", 0)); body = json.loads(self.rfile.read(n))
        names = [t["function"]["name"] for t in body["tools"]]
        # the incident menu for the ops hand: the kernel's typed tools the skill is granted, without approve/apply/converge
        assert "laib_restart" in names and "laib_note" in names and "laib_diag" in names, names
        assert not set(names) & {"laib_approve", "laib_apply", "laib_converge"} and len(names) <= 8, names
        svc = body["tools"][names.index("laib_restart")]["function"]["parameters"]["properties"]["service"]
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
grep -q "^status" <<<"$(LAIB_MCP_URL=http://127.0.0.1:5976/mcp LAIB_TOKEN_FILE="$WORK/token" python3 "$ROOT/werkstatt/laibctl.py")" || fail "laibctl list"
LAIB_MCP_URL=http://127.0.0.1:5976/mcp LAIB_TOKEN_FILE="$WORK/token" python3 "$ROOT/werkstatt/laibctl.py" status | python3 -c 'import sys,json; assert json.load(sys.stdin)["ready"]["state"]' || fail "laibctl status"
if LAIB_MCP_URL=http://127.0.0.1:5976/mcp LAIB_TOKEN_FILE="$WORK/none" python3 "$ROOT/werkstatt/laibctl.py" status >/dev/null 2>&1; then fail "laibctl without token got through"; fi
# skills (ADR 0023): a call in a skill's name gets only that skill's tools, on the real server
mcp_skill() { python3 - "$OPS_TOKEN" "$@" <<'PY3'
import json, sys, urllib.request
tok, skill, method, name = sys.argv[1], sys.argv[2], sys.argv[3], (sys.argv[4] if len(sys.argv) > 4 else "")
params = {"name": name, "arguments": {"args": ""}} if name else {}
req = urllib.request.Request("http://127.0.0.1:5976/mcp", json.dumps({"jsonrpc": "2.0", "id": 1, "method": method, "params": params}).encode(),
    {"Authorization": "Bearer " + tok, "Content-Type": "application/json", "X-Laib-Skill": skill})
print(json.dumps(json.load(urllib.request.urlopen(req))["result"]))
PY3
}
mcp_skill routineblick tools/list | python3 -c 'import sys,json; n=[t["name"] for t in json.load(sys.stdin)["tools"]]; assert n==["laib_status","laib_diag","laib_note","laib_repair"], n' || fail "routineblick sees more than its tools"
mcp_skill routineblick tools/call laib_restart | python3 -c 'import sys,json; r=json.load(sys.stdin); assert r["isError"] and "nicht ausgef" in r["content"][0]["text"], r' || fail "a skill called a tool it does not have"
mcp_skill gibtsnicht tools/call laib_status | python3 -c 'import sys,json; assert json.load(sys.stdin)["isError"]' || fail "an unknown skill's call ran"
kill $FAKE $SERVE 2>/dev/null; wait $FAKE $SERVE 2>/dev/null || true
python3 -c 'import sys,json; s=json.load(open(sys.argv[1])); assert s["outcome"]=="green" and s["skill"]=="vorfall" and len(s["calls"])==4, s' "$WORK/op.json"
# the switch: listed for everyone, set by a human, a commit to policy.yml
grep -qE "^aktiv +morgenbericht" <<<"$("$BIN" skill list)" || fail "skill list"
if LAIB_ACTOR=ops "$BIN" skill pause routineblick >/dev/null 2>&1; then fail "ops paused a skill"; fi
grep -q "morgenbericht: ruht" <<<"$(LAIB_ACTOR=human:e2e "$BIN" skill pause morgenbericht)" || fail "skill pause"
grep -qE "^ruht +morgenbericht" <<<"$("$BIN" skill list)" || fail "paused skill listed as active"
grep -q "morgenbericht: paused" "$LAIB_HOME/state/policy.yml" || fail "pause not in policy.yml"
[ -z "$(git -C "$LAIB_HOME/state" status --porcelain)" ] || fail "skill pause left policy.yml uncommitted"
LAIB_ACTOR=human:e2e "$BIN" skill enable morgenbericht >/dev/null || fail "skill enable"
grep -qE "^aktiv +nutzung" <<<"$("$BIN" skill list)" || fail "the weekly usage skill is not active"
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
# the token sits in the unit's env file, never in the unit (#443)
grep -q 'LAIB_TOKEN="laib_ops_' "$LAIB_SYSTEMD/laib-operator.env" || fail "ops token not in the unit's env file"
if grep -q "laib_ops_" "$LAIB_SYSTEMD/laib-operator.service"; then fail "the ops token is in the unit itself"; fi
[ "$(stat -c %a "$LAIB_SYSTEMD/laib-operator.env")" = 600 ] || fail "the env file with the token is not 0600"
grep -q "enable --now laib-operator.service" "$SYSTEMCTL_LOG" || fail "operator unit not enabled"
if LAIB_ACTOR=ops "$BIN" operator disable >/dev/null 2>&1; then fail "ops disabled the operator"; fi

# --- 11. the bote and frage hooks render their configs from values and secrets
HOOKDIR="$WORK/hooks"; mkdir -p "$HOOKDIR"
( cd "$ROOT/templates/bote" && LAIB_DATA_DIR="$HOOKDIR" LAIB_SERVICE=bote LAIB_DOMAIN=e2e.home LAIB_TZ=UTC LAIB_LAN_IP=10.0.0.2 \
  MODEL_URL=http://127.0.0.1:5977/v1 LAIB_MCP_URL=http://host.containers.internal:5900/mcp \
  SIGNAL_ACCOUNT=+491 SIGNAL_ALLOWED_USERS=+492 ADMIN_NUMBER=+492 MODEL_NAME=gemma-4-e4b THINKING_MODEL=qwen \
  LAIB_SECRET_BOTE_LAIB_TOKEN=laib_bote_x LAIB_SECRET_BOTE_APPROVER_TOKEN=laib_approver_z LAIB_SECRET_BOTE_MODEL_KEY= HAUSHALT_MCP_URL= ./post-deploy >"$WORK/bote.out" ) || fail "bote hook"
if grep -q "HUMAN_TOKEN\|laib-admin" "$HOOKDIR/bote/hermes/config.yaml" "$HOOKDIR/bote/hermes/.env"; then fail "hermes holds a token that can approve (#48)"; fi
grep -q "SIGNAL_ACCOUNT=+491" "$HOOKDIR/bote/hermes/.env" || fail "bote .env"
# #178: inside the pod signal-cli listens on 8080; 8085 is only the host's side of PublishPort
grep -q "^SIGNAL_HTTP_URL=http://127.0.0.1:8080$" "$HOOKDIR/bote/hermes/.env" || fail "Hermes reaches signal-cli inside the pod on 8080, not the host port (#178)"
if grep -l "127.0.0.1:8085" "$ROOT"/templates/bote/*.container >/dev/null 2>&1; then fail "a container in the bote pod uses the host port 8085 (#178)"; fi
grep -q 'Authorization: "Bearer ${LAIB_TOKEN}"' "$HOOKDIR/bote/hermes/config.yaml" || fail "bote mcp header keeps the env reference"
grep -q "base_url: http://127.0.0.1:5977/v1" "$HOOKDIR/bote/hermes/config.yaml" || fail "bote model url"
grep -q "trust: untrusted" "$HOOKDIR/bote/hermes/config.yaml" || fail "bote's laib entry must be untrusted"
# #181: no platform gives the model a terminal, code, files, a browser, delegation, cron or skills;
# every Hermes platform carries the same safe allowlist, so the Bote acts only through its MCP tools
python3 - "$HOOKDIR/bote/hermes/config.yaml" <<'PY' || fail "the Bote's platforms must carry only the safe toolsets (#181)"
import re, sys
text = open(sys.argv[1]).read()
m = re.search(r"^platform_toolsets:\n((?:  .*\n)+)", text, re.M)
assert m, "no platform_toolsets"
rows = dict(re.findall(r"^  ([a-z_]+): \[(.*)\]$", m.group(1), re.M))
want = {"cli", "telegram", "discord", "slack", "whatsapp", "whatsapp_cloud", "signal", "bluebubbles", "email",
        "mattermost", "matrix", "dingtalk", "feishu", "wecom", "wecom_callback", "weixin", "qqbot", "yuanbao",
        "webhook", "api_server", "cron"}
assert want <= set(rows), sorted(want - set(rows))
bad = {"terminal", "code_execution", "file", "browser", "delegation", "cronjob", "skills", "web", "search",
       "debugging", "computer_use", "hermes-signal", "hermes-api-server"}
for k, v in rows.items():
    names = {x.strip() for x in v.split(",") if x.strip()}
    assert names and not (names & bad), (k, names & bad)
PY
grep -q "+492" "$HOOKDIR/bote/hermes/SOUL.md" || fail "bote soul names the admin"
[ "$(stat -c %a "$HOOKDIR/bote/hermes/.env")" = 600 ] || fail "bote .env not 0600"
# #54: only MCP servers that answer are rendered, and the SOUL names only the tools the Bote has
if grep -q "haushalt:" "$HOOKDIR/bote/hermes/config.yaml"; then fail "an unset Haushalt must not be configured (#54)"; fi
if grep -qi "haushalt\|Gedächtnis\|(frage\|automat\|haus:\|(ha:" "$HOOKDIR/bote/hermes/SOUL.md"; then fail "the SOUL promises a tool the Bote does not have (#54)"; fi
grep -q "nicht merken\|erinnere mich nicht\|vergesse" "$HOOKDIR/bote/hermes/SOUL.md" || fail "the SOUL must say what the Bote does not remember (#54)"
# #470: the chat answers without thinking (Hermes reads extra_body only from a custom_providers entry
# matched by provider and base_url: agent/agent_init.py), /model denken thinks, through its own named entry
python3 - "$HOOKDIR/bote/hermes/config.yaml" <<'PY' || fail "the Bote's chat thinks before every answer, or denken does not (#470)"
import re, sys
t = open(sys.argv[1]).read()
cp = t[t.index("\ncustom_providers:"):]
first, second = cp.index("- name: laib\n"), cp.index("- name: denken\n")
assert first < second, "laib's entry must come first: it is the default model's fallback"
assert "enable_thinking: false" in cp[first:second], cp[first:second]
assert "enable_thinking: true" in cp[second:second + 400], cp[second:second + 400]
alias = t[t.index("model_aliases:"):t.index("mcp_servers:")]
assert "provider: custom:denken" in alias, alias
PY
# #502: every tool in the model's array (Hermes' tool search cost three rounds per tool call and
# cannot exempt one MCP server), and Hermes' own opt-out drops its stock skills in its container
python3 - "$HOOKDIR/bote/hermes/config.yaml" <<'PY' || fail "the Bote's tools still hide behind tool_search (#502)"
import re, sys
t = open(sys.argv[1]).read()
m = re.search(r"^tools:\n  tool_search:\n    enabled: (.*)$", t, re.M)
assert m and m.group(1).strip().strip('"') == "off", m and m.group(1)
PY
grep -q "^  threshold_tokens: 32000$" "$HOOKDIR/bote/hermes/config.yaml" || fail "a long conversation is never compressed by token count (#502)"
grep -qx "laib-exec: systemd-bote-hermes hermes skills opt-out --remove --yes" "$WORK/bote.out" || fail "Hermes' stock skills stay in the Bote (#502)"
# #122: the Bote's persona is Joshua, every message goes to the persona tool first (ADR 0021), the rules stay,
# and approvals, warnings and errors are plain words, not role-play
grep -q "Du bist Joshua" "$HOOKDIR/bote/hermes/SOUL.md" || fail "the Bote's persona is Joshua (#122)"
grep -q "Laibs Persona sieht jede Nachricht vor dir" "$HOOKDIR/bote/hermes/SOUL.md" || fail "every message goes to the persona engine first (#122, #133, #471)"
test -s "$HOOKDIR/bote/hermes/plugins/laib-persona/__init__.py" && grep -q "enabled: \[laib-persona" "$HOOKDIR/bote/hermes/config.yaml" || fail "the persona engine is the plugin laib-persona in front of the model (#471)"
grep -q "Klartext" "$HOOKDIR/bote/hermes/SOUL.md" || fail "approvals and warnings stay plain words (#122)"
grep -q "Freigaben kannst du nicht erteilen" "$HOOKDIR/bote/hermes/SOUL.md" || fail "the persona dropped the approval rule"
grep -q "Löschen, Neustart der Box, öffentliche Routen: nie" "$HOOKDIR/bote/hermes/SOUL.md" || fail "the persona dropped the never-list"
grep -q "Laib schreibt dem Admin hierher" "$HOOKDIR/bote/hermes/SOUL.md" || fail "the SOUL does not say Laib's messages arrive in the chat (#441)"
grep -q "nur in der Inbox, mit dem zweiten Faktor" "$HOOKDIR/bote/hermes/SOUL.md" || fail "the SOUL lets the chat decide what only the Inbox may (#441)"
grep -qx "laib" "$HOOKDIR/bote/persona/faehigkeiten.txt" || fail "the persona's directory lists the Bote's tools (#121, #133)"
# #184: Signal, Matrix and Telegram are each optional; an unset channel leaves no entry in Hermes' .env
if grep -q "^MATRIX_\|^TELEGRAM_" "$HOOKDIR/bote/hermes/.env"; then fail "a Signal-only Bote has Matrix or Telegram entries (#184)"; fi
bote_ch() { ( cd "$ROOT/templates/bote" && env LAIB_DATA_DIR="$HOOKDIR" LAIB_SERVICE=bote LAIB_DOMAIN=e2e.home LAIB_TZ=UTC LAIB_LAN_IP=10.0.0.2 \
  MODEL_URL=http://127.0.0.1:5977/v1 LAIB_MCP_URL=http://host.containers.internal:5900/mcp MODEL_NAME=gemma-4-e4b THINKING_MODEL=qwen \
  LAIB_SECRET_BOTE_LAIB_TOKEN=laib_bote_x LAIB_SECRET_BOTE_APPROVER_TOKEN=laib_approver_z LAIB_SECRET_BOTE_MODEL_KEY= HAUSHALT_MCP_URL= \
  SIGNAL_ACCOUNT= SIGNAL_ALLOWED_USERS= ADMIN_NUMBER= MATRIX_HOMESERVER= MATRIX_ALLOWED_USERS= ADMIN_MXID= ADMIN_ROOM= TELEGRAM_ALLOWED_USERS= DISCORD_ALLOWED_USERS= ADMIN_DISCORD_ID= \
  "$@" ./post-deploy > "$WORK/bote-ch.out" 2>&1 ); }
bote_ch MATRIX_HOMESERVER=https://hs.e2e.home MATRIX_ALLOWED_USERS=@admin:e2e.home ADMIN_MXID=@admin:e2e.home 'ADMIN_ROOM=!raum:e2e.home' \
  LAIB_SECRET_BOTE_MATRIX_TOKEN=mx_tok || fail "bote hook with Matrix only"
for want in "MATRIX_HOMESERVER=https://hs.e2e.home" "MATRIX_ACCESS_TOKEN=mx_tok" "MATRIX_ALLOWED_USERS=@admin:e2e.home" "MATRIX_E2EE_MODE=off" "MATRIX_HOME_ROOM=!raum:e2e.home"; do
  grep -qx "$want" "$HOOKDIR/bote/hermes/.env" || fail "a Matrix Bote's .env lacks $want (#184)"
done
if grep -q "^SIGNAL_\|^TELEGRAM_" "$HOOKDIR/bote/hermes/.env"; then fail "a Matrix-only Bote has Signal or Telegram entries (#184)"; fi
grep -qx "MATRIX_ACCESS_TOKEN=mx_tok" "$HOOKDIR/bote/notify/.env" || fail "bote-notify gets the Matrix credentials (#184)"
grep -q "@admin:e2e.home" "$HOOKDIR/bote/hermes/SOUL.md" || fail "the SOUL names the admin's Matrix ID as the one who approves (#184)"
if grep -q "+49" "$HOOKDIR/bote/hermes/SOUL.md"; then fail "a Matrix-only SOUL names a phone number (#184)"; fi
bote_ch TELEGRAM_ALLOWED_USERS=123456 LAIB_SECRET_BOTE_TELEGRAM_TOKEN=tg_tok || fail "bote hook with Telegram only"
grep -qx "TELEGRAM_BOT_TOKEN=tg_tok" "$HOOKDIR/bote/hermes/.env" || fail "a Telegram Bote's .env lacks its bot token (#184)"
grep -qx "TELEGRAM_ALLOWED_USERS=123456" "$HOOKDIR/bote/hermes/.env" || fail "a Telegram Bote's .env lacks who may talk (#184)"
if grep -q "^SIGNAL_\|^MATRIX_" "$HOOKDIR/bote/hermes/.env"; then fail "a Telegram-only Bote has Signal or Matrix entries (#184)"; fi
grep -q "Freigaben kannst du nicht erteilen" "$HOOKDIR/bote/hermes/SOUL.md" || fail "the approval rule holds without an approval channel"
grep -q "Box-Seite" "$HOOKDIR/bote/hermes/SOUL.md" || fail "without Signal or Matrix approvals the SOUL points at the Box page (#184)"
if bote_ch MATRIX_HOMESERVER=https://hs.e2e.home; then fail "Matrix without BOTE_MATRIX_TOKEN must fail the hook (#184)"; fi
grep -q "BOTE_MATRIX_TOKEN" "$WORK/bote-ch.out" || fail "the hook names the missing Matrix token (#184)"
if bote_ch TELEGRAM_ALLOWED_USERS=123456; then fail "Telegram without BOTE_TELEGRAM_TOKEN must fail the hook (#184)"; fi
if bote_ch; then fail "a Bote without any channel must fail the hook (#184)"; fi
# #421: Discord as the fourth channel, Hermes' names (plugins/platforms/discord/gateway_env.py)
bote_ch DISCORD_ALLOWED_USERS=112233445566778899 LAIB_SECRET_BOTE_DISCORD_TOKEN=dc_tok || fail "bote hook with Discord only: $(cat "$WORK/bote-ch.out")"
grep -qx "DISCORD_BOT_TOKEN=dc_tok" "$HOOKDIR/bote/hermes/.env" || fail "a Discord Bote's .env lacks its bot token (#421)"
grep -qx "DISCORD_ALLOWED_USERS=112233445566778899" "$HOOKDIR/bote/hermes/.env" || fail "a Discord Bote's .env lacks who may talk (#421)"
if grep -q "^SIGNAL_\|^MATRIX_\|^TELEGRAM_" "$HOOKDIR/bote/hermes/.env"; then fail "a Discord-only Bote has other channels' entries (#421)"; fi
grep -qx "DISCORD_BOT_TOKEN=dc_tok" "$HOOKDIR/bote/notify/.env" || fail "bote-notify gets the Discord credentials (#421)"
grep -q "über Discord nie" "$HOOKDIR/bote/hermes/SOUL.md" || fail "the SOUL says Discord never approves (#421)"
if bote_ch DISCORD_ALLOWED_USERS=112233445566778899; then fail "Discord without BOTE_DISCORD_TOKEN must fail the hook (#421)"; fi
grep -q "BOTE_DISCORD_TOKEN" "$WORK/bote-ch.out" || fail "the hook names the missing Discord token (#421)"
# #463: the owner's verdicts on Discord: their id must be on the allowed list, and the SOUL names who decides
if bote_ch DISCORD_ALLOWED_USERS=112233445566778899 LAIB_SECRET_BOTE_DISCORD_TOKEN=dc_tok ADMIN_DISCORD_ID=999; then fail "an owner id not on DISCORD_ALLOWED_USERS must fail the hook (#463)"; fi
bote_ch DISCORD_ALLOWED_USERS=112233445566778899 LAIB_SECRET_BOTE_DISCORD_TOKEN=dc_tok ADMIN_DISCORD_ID=112233445566778899 || fail "bote hook with the owner on Discord: $(cat "$WORK/bote-ch.out")"
grep -q "Discord-Direktchat" "$HOOKDIR/bote/hermes/SOUL.md" || fail "the SOUL does not say the owner decides in the Discord DM (#463)"
if grep -q "über Discord nie" "$HOOKDIR/bote/hermes/SOUL.md"; then fail "the SOUL still says Discord never decides (#463)"; fi
# #127: the SOUL is the house persona's style.md plus the fixed rules; the rules are in every persona
mkdir -p "$WORK/render/persona/hal"
printf 'Du bist HAL, ruhig und höflich, ein wenig zu ruhig.\n' > "$WORK/render/persona/hal/style.md"
( cd "$ROOT/templates/bote" && LAIB_DATA_DIR="$HOOKDIR" LAIB_SERVICE=bote LAIB_DOMAIN=e2e.home LAIB_TZ=UTC LAIB_LAN_IP=10.0.0.2 \
  LAIB_RENDER_DIR="$WORK/render" PERSONA=hal \
  MODEL_URL=http://127.0.0.1:5977/v1 LAIB_MCP_URL=http://host.containers.internal:5900/mcp \
  SIGNAL_ACCOUNT=+491 SIGNAL_ALLOWED_USERS=+492 ADMIN_NUMBER=+492 MODEL_NAME=gemma-4-e4b THINKING_MODEL=qwen \
  LAIB_SECRET_BOTE_LAIB_TOKEN=laib_bote_x LAIB_SECRET_BOTE_APPROVER_TOKEN=laib_approver_z LAIB_SECRET_BOTE_MODEL_KEY= HAUSHALT_MCP_URL= ./post-deploy >/dev/null ) || fail "bote hook with a persona"
grep -q "Du bist HAL, ruhig und höflich" "$HOOKDIR/bote/hermes/SOUL.md" || fail "the SOUL speaks as the house persona's style.md (#127)"
if grep -q "Du bist Joshua" "$HOOKDIR/bote/hermes/SOUL.md"; then fail "another house persona still says Joshua (#127)"; fi
for rule in "Klartext" "Freigaben kannst du nicht erteilen" "Löschen, Neustart der Box, öffentliche Routen: nie" "Laibs Persona sieht jede Nachricht vor dir" "FIGUR"; do
  grep -q "$rule" "$HOOKDIR/bote/hermes/SOUL.md" || fail "the rules are in every persona's SOUL: $rule (#127)"
done
# a Frage that answers is wired as MCP; a Haushalt that does not answer is left out and named
python3 -c '
import http.server, threading, sys
class H(http.server.BaseHTTPRequestHandler):
    def do_POST(self): self.send_response(200); self.end_headers(); self.wfile.write(b"{}")
    def log_message(self, *a): pass
s = http.server.HTTPServer(("127.0.0.1", 0), H); open(sys.argv[1], "w").write(str(s.server_port)); s.serve_forever()' "$WORK/frage.port" &
FRAGE_PID=$!; sleep 0.5; FRAGE_PORT=$(cat "$WORK/frage.port")
( cd "$ROOT/templates/bote" && LAIB_DATA_DIR="$HOOKDIR" LAIB_SERVICE=bote LAIB_DOMAIN=e2e.home LAIB_TZ=UTC LAIB_LAN_IP=10.0.0.2 \
  MODEL_URL=http://127.0.0.1:5977/v1 LAIB_MCP_URL=http://host.containers.internal:5900/mcp \
  SIGNAL_ACCOUNT=+491 SIGNAL_ALLOWED_USERS=+492 ADMIN_NUMBER=+492 MODEL_NAME=gemma-4-e4b THINKING_MODEL=qwen \
  LAIB_SECRET_BOTE_LAIB_TOKEN=laib_bote_x LAIB_SECRET_BOTE_APPROVER_TOKEN=laib_approver_z LAIB_SECRET_BOTE_MODEL_KEY= \
  LAIB_SECRET_BOTE_HA_TOKEN=ha_y HA_URL=http://127.0.0.1:8123 \
  FRAGE_URL="http://127.0.0.1:$FRAGE_PORT/mcp" HAUSHALT_MCP_URL="http://127.0.0.1:1/mcp" ./post-deploy > "$WORK/bote-hook.out" ) || fail "bote hook with frage"
kill $FRAGE_PID 2>/dev/null || true
grep -q "frage:" "$HOOKDIR/bote/hermes/config.yaml" || fail "a Frage that answers is wired as an MCP server (#54)"
grep -q "url: http://127.0.0.1:$FRAGE_PORT/mcp" "$HOOKDIR/bote/hermes/config.yaml" || fail "frage mcp url"
if grep -q "haushalt:" "$HOOKDIR/bote/hermes/config.yaml"; then fail "a Haushalt that does not answer must be left out (#54)"; fi
grep -qi "haushalt.*antwortet nicht\|haushalt.*weggelassen" "$WORK/bote-hook.out" || fail "the hook names the server it left out"
grep -q "Frage" "$HOOKDIR/bote/hermes/SOUL.md" || fail "the SOUL names Frage once it is wired"
# #134: Home Assistant is the tool ha (ADR 0021); haus is the family's pages, never a Bote tool
grep -q "^  ha:" "$HOOKDIR/bote/hermes/config.yaml" || fail "Home Assistant is wired as the Bote tool ha (#134)"
grep -q "url: http://127.0.0.1:8123/api/mcp/assist" "$HOOKDIR/bote/hermes/config.yaml" || fail "the ha tool reaches Home Assistant's Assist API, which it serves a non-admin (#134, #349)"
if grep -q "^  haus:" "$HOOKDIR/bote/hermes/config.yaml"; then fail "a Bote tool is named haus (#134)"; fi
grep -qx "ha" "$HOOKDIR/bote/persona/faehigkeiten.txt" || fail "the persona's directory lists ha (#134)"
# #170: the Haushalt (haus#11) is wired with Haus's bearer; a Haushalt that refuses the token is left out and named
python3 -c '
import http.server, sys
class H(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        ok = self.headers.get("Authorization") == "Bearer hh_tok"
        self.send_response(200 if ok else 401); self.end_headers(); self.wfile.write(b"{}")
    def log_message(self, *a): pass
s = http.server.HTTPServer(("127.0.0.1", 0), H); open(sys.argv[1], "w").write(str(s.server_port)); s.serve_forever()' "$WORK/hh.port" >/dev/null 2>&1 &
HH_PID=$!; sleep 0.5; HH_PORT=$(cat "$WORK/hh.port")
bote_hh() { ( cd "$ROOT/templates/bote" && LAIB_DATA_DIR="$HOOKDIR" LAIB_SERVICE=bote LAIB_DOMAIN=e2e.home LAIB_TZ=UTC LAIB_LAN_IP=10.0.0.2 \
  MODEL_URL=http://127.0.0.1:5977/v1 LAIB_MCP_URL=http://host.containers.internal:5900/mcp \
  SIGNAL_ACCOUNT=+491 SIGNAL_ALLOWED_USERS=+492 ADMIN_NUMBER=+492 MODEL_NAME=gemma-4-e4b THINKING_MODEL=qwen \
  LAIB_SECRET_BOTE_LAIB_TOKEN=laib_bote_x LAIB_SECRET_BOTE_APPROVER_TOKEN=laib_approver_z LAIB_SECRET_BOTE_MODEL_KEY= \
  LAIB_SECRET_HAUS_HAUSHALT_TOKEN="$1" HAUSHALT_MCP_URL="http://127.0.0.1:$HH_PORT/mcp/haushalt" ./post-deploy > "$WORK/bote-hh.out" ) || fail "bote hook with a haushalt"; }
bote_hh hh_tok
grep -q "^  haushalt:" "$HOOKDIR/bote/hermes/config.yaml" || fail "a Haushalt that accepts the token is wired (#170)"
grep -q 'Authorization: "Bearer ${HAUSHALT_TOKEN}"' <<<"$(grep -A4 "^  haushalt:" "$HOOKDIR/bote/hermes/config.yaml")" || fail "the Haushalt entry sends Haus's bearer from .env (#170)"
grep -q "^HAUSHALT_TOKEN=hh_tok$" "$HOOKDIR/bote/hermes/.env" || fail "the Haushalt token is in .env, not in config.yaml (#170)"
if grep -q "hh_tok" "$HOOKDIR/bote/hermes/config.yaml"; then fail "the token itself landed in config.yaml (#170)"; fi
grep -qi "Gedächtnis des Hauses" "$HOOKDIR/bote/hermes/SOUL.md" || fail "the SOUL names the Haushalt once it is wired (#170)"
bote_hh wrong
if grep -q "^  haushalt:" "$HOOKDIR/bote/hermes/config.yaml"; then fail "a Haushalt that refuses the token must be left out (#170)"; fi
grep -qi "haushalt.*Token" "$WORK/bote-hh.out" || fail "the hook says the Haushalt refused the token (#170)"
kill $HH_PID 2>/dev/null || true
# #177, ADR 0022 §3: "sag mir Bescheid, wenn …" becomes an automation whose action is
# rest_command.mitteilung; the SOUL names the people by name only, and only with the automat wired
mkdir -p "$WORK/render/people"
printf '{"mama":["signal:+4917012345678","telegram:123"],"papa":["telegram:456"]}\n' > "$WORK/render/people/people.json"
bote_people() { ( cd "$ROOT/templates/bote" && LAIB_DATA_DIR="$HOOKDIR" LAIB_SERVICE=bote LAIB_DOMAIN=e2e.home LAIB_TZ=UTC LAIB_LAN_IP=10.0.0.2 \
  LAIB_RENDER_DIR="$WORK/render" MODEL_URL=http://127.0.0.1:5977/v1 LAIB_MCP_URL=http://host.containers.internal:5900/mcp \
  SIGNAL_ACCOUNT=+491 SIGNAL_ALLOWED_USERS=+492 ADMIN_NUMBER=+492 MODEL_NAME=gemma-4-e4b THINKING_MODEL=qwen \
  LAIB_SECRET_BOTE_LAIB_TOKEN=laib_bote_x LAIB_SECRET_BOTE_APPROVER_TOKEN=laib_approver_z LAIB_SECRET_BOTE_MODEL_KEY= \
  LAIB_SECRET_BOTE_HA_TOKEN="$1" LAIB_SECRET_BOTE_AUTOMAT_HA_TOKEN="$1" HAUSHALT_MCP_URL= ./post-deploy >/dev/null ) || fail "bote hook with people"; }
bote_people ha_y
grep -q "Sag mir Bescheid, wenn" "$HOOKDIR/bote/hermes/SOUL.md" || fail "the SOUL turns 'sag mir Bescheid' into an automation (#177)"
grep -q "rest_command.mitteilung" "$HOOKDIR/bote/hermes/SOUL.md" || fail "the automation's action is rest_command.mitteilung (#177)"
grep -q "an: <person>" "$HOOKDIR/bote/hermes/SOUL.md" || fail "the action's data names the person (#177)"
grep -q "mama, papa" "$HOOKDIR/bote/hermes/SOUL.md" || fail "the SOUL lists the people by name (#177)"
if grep -q "+4917012345678\|telegram:123\|456" "$HOOKDIR/bote/hermes/SOUL.md"; then fail "the SOUL carries a person's number or chat id (#177)"; fi
grep -q "Code" "$HOOKDIR/bote/hermes/SOUL.md" || fail "the mitteilung rule keeps the automat's confirmation (#177)"
# bote-notify gets the platform credentials only, never the Bote's tokens, and none of HERMES_HOME (#177 review)
N="$HOOKDIR/bote/notify/.env"
grep -q "^SIGNAL_ACCOUNT=+491$" "$N" || fail "bote-notify's .env carries the Signal credentials (#177)"
if grep -q "LAIB_TOKEN\|HA_TOKEN\|HAUSHALT_TOKEN\|MODEL_API_KEY\|laib_bote_x\|ha_y" "$N"; then fail "bote-notify's .env carries a Bote secret (#177)"; fi
[ "$(stat -c %a "$N")" = 600 ] || fail "bote-notify's .env is not 0600 (#177)"
grep -q "bote/notify:/hermes:ro" "$ROOT/templates/bote/bote-notify.container" || fail "bote-notify mounts its own credentials (#177)"
if grep -q "bote/hermes:" "$ROOT/templates/bote/bote-notify.container"; then fail "bote-notify mounts the Bote's whole HERMES_HOME (#177)"; fi
bote_people ""
if grep -q "mitteilung" "$HOOKDIR/bote/hermes/SOUL.md"; then fail "without the automat the SOUL promises messages it cannot set up (#177)"; fi
# Home Assistant's pre-start (#253): rest_command.mitteilung points at bote-notify with the bearer from its own
# environment (#177), and the packages include is there before Home Assistant starts. The pinned auth_oidc
# component counts as installed here: pre-start fetches nothing (its own tests cover the archive)
HA_CFG="$HOOKDIR/home-assistant/homeassistant"; mkdir -p "$HA_CFG/custom_components/auth_oidc"
echo e2e0sha > "$HA_CFG/custom_components/auth_oidc/.laib-archive"
ha_hook() { ( cd "$ROOT/templates/home-assistant" && LAIB_DATA_DIR="$HOOKDIR" LAIB_SERVICE=home-assistant LAIB_DOMAIN=e2e.home \
  AUTH_OIDC_ARCHIVE=http://127.0.0.1:9/not-fetched AUTH_OIDC_SHA256=e2e0sha \
  BOTE_NOTIFY_URL=http://127.0.0.1:8645/mitteilung ./pre-start > "$WORK/ha-hook.out" ) || fail "home-assistant hook"; }
ha_hook
grep -q "^default_config:" "$HA_CFG/configuration.yaml" || fail "a fresh box gets Home Assistant's default configuration.yaml from pre-start (#253)"
[ "$(grep -c "packages: !include_dir_named packages" "$HA_CFG/configuration.yaml")" = 1 ] || fail "the first start has the packages include (#253)"
printf 'default_config:\n\nautomation: !include automations.yaml\n' > "$HA_CFG/configuration.yaml"
ha_hook; ha_hook
P="$HA_CFG/packages/laib_mitteilung.yaml"
grep -q "^  mitteilung:" "$P" || fail "rest_command.mitteilung is rendered (#177)"
grep -q 'url: "http://127.0.0.1:8645/mitteilung"' "$P" || fail "rest_command.mitteilung reaches bote-notify on loopback (#177)"
grep -q "Authorization: !env_var BOTE_NOTIFY_AUTH" "$P" || fail "the bearer comes from HA's environment, not the file (#177)"
grep -q '"an": {{ an | tojson }}' "$P" || fail "the payload carries an and text (#177)"
grep -q "client_secret: !env_var AUTH_OIDC_SECRET" "$HA_CFG/packages/laib_auth_oidc.yaml" || fail "the family login's secret comes from HA's environment (#253)"
[ "$(grep -c "packages: !include_dir_named packages" "$HA_CFG/configuration.yaml")" = 1 ] || fail "the packages include is added exactly once (#177)"
grep -q "automation: !include automations.yaml" "$HA_CFG/configuration.yaml" || fail "the HA hook changed the admin's configuration.yaml (#177)"
printf 'homeassistant:\n  name: Haus\n' > "$HA_CFG/configuration.yaml"
ha_hook
[ "$(grep -c "^homeassistant:" "$HA_CFG/configuration.yaml")" = 1 ] || fail "a second homeassistant: key was appended (#177)"
grep -q "^  packages: !include_dir_named packages" "$HA_CFG/configuration.yaml" || fail "the include goes under the existing homeassistant: block (#253)"
grep -q "^  name: Haus" "$HA_CFG/configuration.yaml" || fail "the admin's homeassistant: block lost a key (#253)"
( cd "$ROOT/templates/frage" && LAIB_DATA_DIR="$HOOKDIR" LAIB_SECRET_SEARX_SECRET=s3 ./pre-start >/dev/null ) || fail "frage hook"
grep -q "formats: \[html, json\]" "$HOOKDIR/frage/searxng/settings.yml" || fail "frage searx json format"

# --- 11b. a template with both hooks, added by the human (#36): pre-start runs in the hooks
# image before the unit starts, post-deploy after; both executable in the state repo
R="$LAIB_HOME/registries/e2e/templates/hooked"; mkdir -p "$R"
cat > "$R/laib.yml" <<'YML'
name: hooked
template: {registry: e2e, name: hooked, version: 1}
routes: []
secrets: []
data:
  - path: ${DATA_DIR}/hooked/cfg
YML
printf 'IMAGE: docker.io/library/busybox:stable\n' > "$R/values.example.yml"
printf '[Container]\nImage=${IMAGE}\nVolume=${DATA_DIR}/hooked/cfg:/cfg:z\n\n[Install]\nWantedBy=default.target\n' > "$R/hooked.container"
printf '#!/bin/sh\necho "phase $LAIB_PHASE"\nmkdir -p "$LAIB_DATA_DIR/hooked/cfg" && echo written > "$LAIB_DATA_DIR/hooked/cfg/by-pre-start"\n' > "$R/pre-start"
printf '#!/bin/sh\necho "phase $LAIB_PHASE"\ntest -f "$LAIB_DATA_DIR/hooked/cfg/by-pre-start" || exit 1\n' > "$R/post-deploy"
chmod 644 "$R/pre-start" "$R/post-deploy"   # the registry copy may lack the bit; the state repo must have it
OUT="$(LAIB_ACTOR=human:e2e "$BIN" add hooked 2>&1)" || fail "add hooked: $OUT"
grep -q "^ok  hooked pre-start" <<<"$OUT" || fail "pre-start not green: $OUT"
grep -q "^ok  hooked post-deploy" <<<"$OUT" || fail "post-deploy not green: $OUT"
test -f "$WORK/data/hooked/cfg/by-pre-start" || fail "pre-start did not write into the data path"
[ -x "$LAIB_HOME/state/services/hooked/pre-start" ] || fail "pre-start is not executable in the state repo"
# order: the pre-start hook ran before the unit was started
pre=$(grep -n "podman run hook .*pre-start" "$SYSTEMCTL_LOG" | head -1 | cut -d: -f1); start=$(grep -n "restart hooked.service" "$SYSTEMCTL_LOG" | head -1 | cut -d: -f1)
[ -n "$pre" ] && [ -n "$start" ] && [ "$pre" -lt "$start" ] || fail "pre-start ($pre) did not run before the first start ($start)"
LAIB_ACTOR=human:e2e "$BIN" remove hooked >/dev/null || fail "remove hooked"

# --- 12. remove: units stopped, files gone, data kept, state without the service
if LAIB_ACTOR=ops "$BIN" remove whoami >/dev/null 2>&1; then fail "ops removed a service"; fi
if LAIB_ACTOR=human:e2e "$BIN" remove caddy >/dev/null 2>&1; then fail "a role holder was removed"; fi
grep -q "Daten bleiben" <<<"$(LAIB_ACTOR=human:e2e "$BIN" remove whoami)" || fail "remove exit $?"
test -f "$LAIB_QUADLET/whoami/whoami.container" && fail "remove left the unit file"
test -d "$LAIB_HOME/state/services/whoami" && fail "remove left the state folder"
test -f "$WORK/data/whoami/cfg/app.conf" || fail "remove deleted data"
grep -q "stop whoami.service" "$SYSTEMCTL_LOG" || fail "remove did not stop the unit"
LAIB_ACTOR=human:e2e "$BIN" rollback >/dev/null || fail "rollback after remove exit $?"
test -f "$LAIB_QUADLET/whoami/whoami.container" || fail "rollback did not bring whoami back"

# --- 13. the journal holds the whole story
j 'verbs=[e["verb"] for e in es]; assert all(v in verbs for v in ["apply","rollback","approve","converge","exec","write","restart","escalate","snapshot","note","operator"]), verbs'

# --- 14. red paths (#52): a failure must end red, be journaled, and change nothing it should not
code() { set +e; "$@" >"$WORK/red.out" 2>&1; local c=$?; set -e; echo "$c"; }
last() { "$BIN" journal --json | python3 -c "import sys,json; es=[e for e in json.load(sys.stdin) if e['verb']=='$1' and e.get('service','')=='$2']; print(json.dumps(es[-1]) if es else '{}')"; }
# a restart that fails
C="$(SHIM_FAIL_RESTART="caddy.service" LAIB_ACTOR=human:e2e code "$BIN" restart caddy)"
[ "$C" = 2 ] || fail "failed restart: exit $C, want 2 (red): $(cat "$WORK/red.out")"
last restart caddy | python3 -c 'import sys,json; e=json.load(sys.stdin); assert e.get("result")=="red" and "caddy.service" in e.get("detail",""), e'
# a unit that never comes up: converge restarts it, verify sees it failed, converge ends red
C="$(SHIM_FAILED_UNITS="whoami.service" LAIB_ACTOR=laib code "$BIN" converge)"
[ "$C" = 2 ] || fail "converge with a failed unit: exit $C, want 2 (red): $(cat "$WORK/red.out")"
"$BIN" journal --json | python3 -c 'import sys,json; es=[e for e in json.load(sys.stdin) if e["verb"]=="converge"]; assert es and es[-1]["result"]=="red", es[-1:]'
# a backup whose repository fails: non-zero, journaled red, no snapshot claimed
C="$(SHIM_RESTIC_FAIL=1 LAIB_ACTOR=human:e2e code "$BIN" backup)"
[ "$C" != 0 ] || fail "backup with a failing restic exited 0"
grep -q "snapshot .* saved" "$WORK/red.out" && fail "backup claims a snapshot although restic failed"
"$BIN" journal --json | python3 -c 'import sys,json; es=[e for e in json.load(sys.stdin) if e["verb"]=="backup"]; assert es and es[-1]["result"]=="red", es[-1:]'
# a restore whose safety snapshot fails: refused before any unit is stopped
STOPS="$(grep -c "stop caddy.service" "$SYSTEMCTL_LOG" || true)"
C="$(SHIM_RESTIC_FAIL=1 LAIB_ACTOR=human:e2e code "$BIN" restore 0123abcd caddy)"
[ "$C" != 0 ] || fail "restore without its pre-restore snapshot exited 0"
[ "$(grep -c "stop caddy.service" "$SYSTEMCTL_LOG" || true)" = "$STOPS" ] || fail "restore stopped caddy although its safety snapshot failed"
# an exec into a container that is not running
C="$(SHIM_PODMAN_EXEC_FAIL=1 LAIB_ACTOR=human:e2e code "$BIN" exec caddy -- true)"
[ "$C" = 2 ] || fail "failed exec: exit $C, want 2 (red): $(cat "$WORK/red.out")"
last exec caddy | python3 -c 'import sys,json; e=json.load(sys.stdin); assert e.get("result")=="red", e'
# --- 15. #184 (B1): a Bote switched from Signal to Matrix applies green with no hand edit: pre-start
# renders the channel .env files before the units start, so neither Hermes nor the health check reads
# the last Signal config. bote-notify serves the pod's health; a fake signal-cli daemon (/api/v1/check, #469) answers while Signal is on.
B="$LAIB_HOME/state/services/bote"; mkdir -p "$B"
cp "$ROOT"/templates/bote/* "$B/" 2>/dev/null || true; mv "$B/values.example.yml" "$B/values.yml"; rm -f "$B/README.md"
grep -q "^laib:" "$LAIB_HOME/state/box.yml" || printf 'laib: {listen: 0.0.0.0}\n' >> "$LAIB_HOME/state/box.yml"
for s in BOTE_LAIB_TOKEN BOTE_APPROVER_TOKEN; do echo "tok_$s" | LAIB_ACTOR=human:e2e "$BIN" secret set "$s" >/dev/null; done
python3 -c '
import http.server
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self): self.send_response(200 if self.path == "/api/v1/check" else 404); self.end_headers(); self.wfile.write(b"{}")
    def log_message(self, *a): pass
http.server.HTTPServer(("127.0.0.1", 8080), H).serve_forever()' & echo $! > "$WORK/signal.pid"
NOTIFY_LISTEN=127.0.0.1:8645 NOTIFY_TOKEN=n NOTIFY_HERMES="$WORK/data/bote/notify" python3 "$ROOT/templates/bote/notify.py" >/dev/null & echo $! > "$WORK/notify.pid"
sleep 0.5
OUT="$(LAIB_ACTOR=human:e2e "$BIN" apply bote --reason "e2e: Bote on Signal" 2>&1)" || fail "the Bote on Signal does not apply green: $OUT"
grep -q "^SIGNAL_HTTP_URL=" "$WORK/data/bote/notify/.env" || fail "bote-notify has no Signal config while Signal is on"
test -f "$LAIB_QUADLET/bote/bote-signal.container" || fail "no signal-cli unit while Signal is on"
kill "$(cat "$WORK/signal.pid")"; rm "$WORK/signal.pid"   # the Signal side is gone
sed -i -e 's|^SIGNAL_ACCOUNT:.*|SIGNAL_ACCOUNT: ""|' -e 's|^MATRIX_HOMESERVER:.*|MATRIX_HOMESERVER: https://hs.e2e.home|' \
  -e 's|^ADMIN_MXID:.*|ADMIN_MXID: "@admin:e2e.home"|' -e 's|^ADMIN_ROOM:.*|ADMIN_ROOM: "!raum:e2e.home"|' "$B/values.yml"
echo mx_tok | LAIB_ACTOR=human:e2e "$BIN" secret set BOTE_MATRIX_TOKEN >/dev/null
"$BIN" plan > "$WORK/plan.out" 2>&1 || fail "plan with Matrix only: $(cat "$WORK/plan.out")"
grep -q "bote-signal.container.*SIGNAL_ACCOUNT" "$WORK/plan.out" || fail "plan does not say why bote-signal is off: $(cat "$WORK/plan.out")"
OUT="$(LAIB_ACTOR=human:e2e "$BIN" apply bote --reason "e2e: Signal off, Matrix on" 2>&1)" || fail "the switch to Matrix is not green without a hand edit (B1): $OUT"
if grep -q "^SIGNAL_" "$WORK/data/bote/notify/.env" "$WORK/data/bote/hermes/.env"; then fail "a Signal line survived the switch"; fi
grep -qx "MATRIX_ACCESS_TOKEN=mx_tok" "$WORK/data/bote/hermes/.env" || fail "Hermes has no Matrix config after the switch"
test -f "$LAIB_QUADLET/bote/bote-signal.container" && fail "the signal-cli unit stayed"
grep -q "stop bote-signal.service" "$SYSTEMCTL_LOG" || fail "the signal-cli unit was not stopped"
pre=$(grep -n "podman run hook .*bote/pre-start" "$SYSTEMCTL_LOG" | tail -1 | cut -d: -f1); start=$(grep -n "restart bote-pod.service" "$SYSTEMCTL_LOG" | tail -1 | cut -d: -f1)
[ -n "$pre" ] && [ -n "$start" ] && [ "$pre" -lt "$start" ] || fail "the Bote's pre-start ($pre) did not run before the pod started ($start)"
kill "$(cat "$WORK/notify.pid")"; rm "$WORK/notify.pid"

# the Operator's wish waits for the admin (#270): a proposal of class human; approving it files the ticket (no intake here: it stays in wishes/)
grep -q "nach der Freigabe" <<<"$(LAIB_ACTOR=ops "$BIN" wish "Den Abfuhrkalender anbinden. Gesehen: 9 offene Fragen.")" || fail "an ops wish is not a proposal"
WID=$("$BIN" inbox --json | python3 -c 'import sys,json; ps=[p for p in json.load(sys.stdin) if p["actor"]=="ops" and p["reason"].startswith("Wunsch:")]; assert ps and ps[0]["class"]=="human", ps; print(ps[0]["id"])') || fail "the wish is not in the inbox as class human"
LAIB_ACTOR=human:e2e "$BIN" approve "$WID" >/dev/null || fail "approving the wish"
j 'assert any(e["verb"]=="wish" and "bleibt in wishes/" in e.get("detail","") for e in es), "the approved wish is not journaled"'

# the Operator applies only its own proposal, inside the envelope (#286): a bare apply by ops is refused
rc=0; LAIB_ACTOR=ops "$BIN" apply whoami >/dev/null 2>&1 || rc=$?; [ "$rc" = 3 ] || fail "ops applied without a proposal (rc $rc)"

echo "e2e ok: $(wc -l < "$SYSTEMCTL_LOG") systemctl calls, $(ls "$LAIB_HOME"/journal | wc -l) journal file(s)"
