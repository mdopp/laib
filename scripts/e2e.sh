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
LAIB_ACTOR=human:e2e "$BIN" init --domain e2e.home --lan-ip 10.0.0.2 >/dev/null
cp -r "$ROOT/testdata/golden/basic/state/services/." "$LAIB_HOME/state/services/"
cp "$ROOT/testdata/golden/basic/state/box.yml" "$ROOT/testdata/golden/basic/state/policy.yml" "$LAIB_HOME/state/"
sed -i '/^health:/,/^[a-z]/{/http:/d}' "$LAIB_HOME"/state/services/*/laib.yml   # nothing listens here
echo "cf-token" | LAIB_ACTOR=human:e2e "$BIN" secret set CLOUDFLARE_API_TOKEN >/dev/null

# --- 2. a tree proposal by ops, approved by the human (#1)
LAIB_ACTOR=ops "$BIN" propose "e2e: three services" >/dev/null
if LAIB_ACTOR=ops "$BIN" approve 1 >/dev/null 2>&1; then fail "ops approved its own proposal"; fi
LAIB_ACTOR=human:e2e "$BIN" approve 1 >/dev/null || fail "approve #1 exit $?"
test -f "$LAIB_QUADLET/whoami/whoami.container" || fail "unit not written"
grep -q "cf-token" "$LAIB_QUADLET/caddy/caddy.container" || fail "secret not rendered on the box"
if grep -rq "cf-token" "$LAIB_HOME/state"; then fail "secret in state repo"; fi
grep -q "daemon-reload" "$SYSTEMCTL_LOG" && grep -q "restart whoami.service" "$SYSTEMCTL_LOG" || fail "systemctl not driven"
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

# --- 10. the journal holds the whole story
j 'verbs=[e["verb"] for e in es]; assert all(v in verbs for v in ["apply","rollback","approve","converge","exec","write","restart","escalate","snapshot"]), verbs'
echo "e2e ok: $(wc -l < "$SYSTEMCTL_LOG") systemctl calls, $(ls "$LAIB_HOME"/journal | wc -l) journal file(s)"
