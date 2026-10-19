#!/bin/bash
# laib-boot-check (#368), root, after every boot. A deployment is proven when
# the GPU answers and the CDI spec names its driver (on a box with an NVIDIA
# card), and laib's /ready answers with no more red services than the last
# proven boot had. The first run only records the baseline. A new deployment
# that is not proven within LAIB_BOOT_CHECK_TIMEOUT seconds is rolled back to
# the proven one, once: Zincati is paused, or it would stage the same update
# again, and the owner gets an incident. Never on the proven one.
set -u
ETC="${LAIB_ETC:-/etc}"; PROVEN="$ETC/laib/boot-proven"; ROLLED="$ETC/laib/boot-rolled-back"
PAUSE="$ETC/zincati/config.d/95-laib-boot-check.toml"
say() { echo "laib-boot-check: $*"; }
note() { runuser -u core -- env LAIB_HOME=/mnt/data/laib LAIB_ACTOR=laib /var/opt/laib/laib note --kind "$1" "$2" >/dev/null 2>&1 || true; }
st="$(ostree admin status 2>/dev/null)"
booted="$(sed -n 's/^\* [^ ]* \([0-9a-f]\{8,\}\)\.[0-9]*.*/\1/p' <<<"$st")"
back="$(sed -n 's/^  [^ ]* \([0-9a-f]\{8,\}\)\.[0-9]* (rollback).*/\1/p' <<<"$st")"
[ -n "$booted" ] || { say "not an ostree deployment: nothing to prove"; exit 0; }
proven=""; base=0
[ -f "$PROVEN" ] && read -r proven base <"$PROVEN"
[ "$booted" != "$proven" ] || { say "deployment ${booted:0:12} is the proven one"; exit 0; }
gpu=""; [ -z "$(lspci -n -d 10de: 2>/dev/null)" ] || gpu=1
why=""; red=0
check() {
  if [ -n "$gpu" ]; then
    local v; v="$(nvidia-smi --query-gpu=driver_version --format=csv,noheader 2>/dev/null | head -1)"
    [ -n "$v" ] || { why="die GPU antwortet nicht (nvidia-smi)"; return 1; }
    grep -qF ".so.$v" "$ETC/cdi/nvidia.yaml" 2>/dev/null || { why="die CDI-Datei nennt nicht den Treiber $v (systemctl status laib-cdi)"; return 1; }
  fi
  local r; r="$(curl -fsS --max-time 5 http://127.0.0.1:5900/ready 2>/dev/null)" || { why="laib antwortet nicht auf /ready"; return 1; }
  red="$(sed -n 's/.*"red":\([0-9]*\).*/\1/p' <<<"$r")"; red="${red:-0}"
  [ "$red" -le "${base:-0}" ] || { why="$red Dienste rot, nach dem letzten bewiesenen Start ${base:-0}"; return 1; }
}
end=$((SECONDS + ${LAIB_BOOT_CHECK_TIMEOUT:-900})); ok=""
while :; do
  check && { ok=1; break; }
  [ "$SECONDS" -lt "$end" ] || break
  sleep "${LAIB_BOOT_CHECK_INTERVAL:-10}"
done
if [ -n "$ok" ] || [ -z "$proven" ]; then
  echo "$booted $red" >"$PROVEN"
  if [ -n "$ok" ]; then say "deployment ${booted:0:12} proven"; note watch "OS-Stand ${booted:0:12} nach dem Start geprüft: grün"; exit 0; fi
  say "baseline ${booted:0:12} recorded although red: $why"
  note incident "OS-Stand ${booted:0:12} als Ausgangsstand gemerkt, aber nicht grün: $why"; exit 0
fi
if [ "$(cat "$ROLLED" 2>/dev/null)" = "$booted" ] || [ "$back" != "$proven" ]; then
  say "not proven ($why); no rollback: already rolled back once from it, or the rollback deployment is not the proven one"
  note incident "OS-Stand ${booted:0:12} ist nicht grün ($why); kein automatisches Zurück (schon einmal zurückgerollt, oder der Rückfall-Stand ist nicht der bewiesene)"; exit 1
fi
say "not proven within ${LAIB_BOOT_CHECK_TIMEOUT:-900}s: $why; rolling back to ${proven:0:12}"
echo "$booted" >"$ROLLED"
mkdir -p "$(dirname "$PAUSE")" && printf '[updates]\nenabled = false\n' >"$PAUSE"
note incident "OS-Update ${booted:0:12} zurückgerollt auf ${proven:0:12}: $why. Zincati pausiert, bis $PAUSE entfernt ist."
rpm-ostree rollback || { say "rpm-ostree rollback failed"; exit 1; }
sync; systemctl reboot
