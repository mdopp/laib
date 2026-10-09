#!/usr/bin/env bash
# End-to-end run of the CLI against the golden state with a systemctl shim:
# init → propose → approve (apply) → status → diag → rollback. No box needed.
# The real-boot job (issue #7) replaces the shim; this stays as the cheap gate.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="${LAIB_BIN:-$ROOT/laib}"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
mkdir -p "$WORK/bin"
cat > "$WORK/bin/systemctl" <<'SH'
#!/usr/bin/env sh
# shim: every unit is active; records calls
echo "$@" >> "${SYSTEMCTL_LOG:-/dev/null}"
case "$*" in *is-active*) echo active;; esac
exit 0
SH
cat > "$WORK/bin/journalctl" <<'SH'
#!/usr/bin/env sh
echo "shim journal for $*"
SH
chmod +x "$WORK/bin/"*
export PATH="$WORK/bin:$PATH" SYSTEMCTL_LOG="$WORK/systemctl.log"
export LAIB_HOME="$WORK/home" LAIB_QUADLET="$WORK/quadlet"

LAIB_ACTOR=human:e2e "$BIN" init --domain e2e.home --lan-ip 10.0.0.2 >/dev/null
cp -r "$ROOT/testdata/golden/basic/state/services/." "$LAIB_HOME/state/services/"
cp "$ROOT/testdata/golden/basic/state/box.yml" "$LAIB_HOME/state/"
# strip health URLs: nothing listens here
sed -i '/^health:/,/^[a-z]/{/http:/d}' "$LAIB_HOME"/state/services/*/laib.yml
echo "cf-token" | LAIB_ACTOR=human:e2e "$BIN" secret set CLOUDFLARE_API_TOKEN >/dev/null

LAIB_ACTOR=ops "$BIN" propose "e2e: three services" >/dev/null
if LAIB_ACTOR=ops "$BIN" approve 1 >/dev/null 2>&1; then echo "FAIL: ops approved"; exit 1; fi
LAIB_ACTOR=human:e2e "$BIN" approve 1 >/dev/null
test -f "$LAIB_QUADLET/whoami/whoami.container" || { echo "FAIL: unit not written"; exit 1; }
grep -q "cf-token" "$LAIB_QUADLET/caddy/caddy.container" || { echo "FAIL: secret not rendered on the box"; exit 1; }
if grep -rq "cf-token" "$LAIB_HOME/state"; then echo "FAIL: secret in state repo"; exit 1; fi
grep -q "daemon-reload" "$SYSTEMCTL_LOG" && grep -q "restart whoami.service" "$SYSTEMCTL_LOG" || { echo "FAIL: systemctl not driven"; exit 1; }

STATUS="$("$BIN" status --json)"
echo "$STATUS" | python3 -c 'import sys,json; r=json.load(sys.stdin)["ready"]; assert r["state"]=="bereit" and r["red"]==0 and r["drift"]==0, r'
"$BIN" plan | grep -q "Keine Änderung" || { echo "FAIL: plan not empty after apply"; exit 1; }
"$BIN" diag --json | python3 -c 'import sys,json; f=json.load(sys.stdin); assert not [x for x in f if x["severity"]=="rot"], f'
"$BIN" logs whoami | grep -q "shim journal" || { echo "FAIL: logs"; exit 1; }

# a change, applied directly by a human, then rolled back
sed -i 's/WHOAMI_NAME=\${SERVICE}/WHOAMI_NAME=changed-${SERVICE}/' "$LAIB_HOME/state/services/whoami/whoami.container"
LAIB_ACTOR=human:e2e "$BIN" apply --reason "e2e change" >/dev/null
grep -q "changed-whoami" "$LAIB_QUADLET/whoami/whoami.container" || { echo "FAIL: change not applied"; exit 1; }
LAIB_ACTOR=human:e2e "$BIN" rollback >/dev/null
grep -q "changed-whoami" "$LAIB_QUADLET/whoami/whoami.container" && { echo "FAIL: rollback did not revert"; exit 1; }

"$BIN" journal --json | python3 -c 'import sys,json; es=json.load(sys.stdin); verbs=[e["verb"] for e in es]; assert "apply" in verbs and "rollback" in verbs and "approve" in verbs, verbs'
echo "e2e ok: $(wc -l < "$SYSTEMCTL_LOG") systemctl calls, $(ls "$LAIB_HOME"/journal | wc -l) journal file(s)"
