#!/bin/bash
# laib-boot-check (#368), root, after every boot. A deployment is proven when
# the GPU answers and the CDI spec names its driver (on a box with an NVIDIA
# card), laib's /ready answers with no more red services than the last
# proven boot had, and the LAN reaches the box (#445): no more of its
# listening TCP ports blocked than at the last proven boot. The first run only records the baseline. A new deployment
# that is not proven within LAIB_BOOT_CHECK_TIMEOUT seconds is rolled back to
# the proven one, once: Zincati is paused, or it would stage the same update
# again, and the owner gets an incident. Never on the proven one.
set -u
# /etc belongs to one deployment: what is written there while booted into a
# new deployment is gone once the box rolls back. So the records live in
# /var/lib/laib, which every deployment shares, and the Zincati pause is
# written into the /etc of the deployment the box rolls back to (train 22).
ETC="${LAIB_ETC:-/etc}"; VAR="${LAIB_VAR:-/var/lib/laib}"; ROOT="${LAIB_ROOT:-}"   # the test harness only
PROVEN="$VAR/boot-proven"; ROLLED="$VAR/boot-rolled-back"
PAUSE=zincati/config.d/95-laib-boot-check.toml
mkdir -p "$VAR"
[ -f "$PROVEN" ] || [ ! -f "$ETC/laib/boot-proven" ] || cp "$ETC/laib/boot-proven" "$PROVEN"   # from before: once
say() { echo "laib-boot-check: $*"; }
note() { runuser -u core -- env LAIB_HOME=/mnt/data/laib LAIB_ACTOR=laib /var/opt/laib/laib note --kind "$1" "$2" >/dev/null 2>&1 || true; }
st="$(ostree admin status 2>/dev/null)"
# a deployment is its commit and its serial: a kernel-argument change is a new
# deployment of the same commit, and can break the box as well
booted="$(sed -n 's/^\* [^ ]* \([0-9a-f]\{8,\}\.[0-9]*\).*/\1/p' <<<"$st")"
back="$(sed -n 's/^  [^ ]* \([0-9a-f]\{8,\}\.[0-9]*\) (rollback).*/\1/p' <<<"$st")"
[ -n "$booted" ] || { say "not an ostree deployment: nothing to prove"; exit 0; }
proven=""; base=0; base_blocked=0
[ -f "$PROVEN" ] && read -r proven base base_blocked <"$PROVEN"
# a boot-proven from before the serial names the bare commit: it meant that commit's deployment
case "$proven" in *.* | "") ;; *) for d in "$booted" "$back"; do [ "${d%.*}" != "$proven" ] || { proven=$d; break; }; done ;; esac
base_blocked="${base_blocked:-0}"
[ "$booted" != "$proven" ] || { say "deployment ${booted:0:12} is the proven one"; exit 0; }
gpu=""; [ -z "$(lspci -n -d 10de: 2>/dev/null)" ] || gpu=1
why=""; red=0; blocked=0
# #445: /ready over loopback says nothing about the LAN: a firewall lets
# loopback through (uCore's firewalld blocked all but SSH for 2h20 while this
# check was green). So every TCP port the box listens on outside loopback is
# probed on the LAN address from a network namespace over a veth pair: the
# packets arrive on an interface, as a LAN client's do. Laib's own table drops
# lan: false ports on physical interfaces only (en*, eth*, wl*), so they
# answer here too. A port that listens and does not answer is blocked; a
# service that is down does not listen and is no blocked port.
NS=laib-probe
probe_down() { ip netns del "$NS" 2>/dev/null; ip link del laibp0 2>/dev/null; }
probe_down
probe=""
if ip netns add "$NS" && ip link add laibp0 type veth peer name laibp1 netns "$NS" &&
  ip addr add 169.254.254.1/30 dev laibp0 && ip link set laibp0 up &&
  ip -n "$NS" addr add 169.254.254.2/30 dev laibp1 && ip -n "$NS" link set laibp1 up &&
  ip -n "$NS" link set lo up && ip -n "$NS" route add default via 169.254.254.1; then
  probe=1
else
  say "no probe namespace: reachability from the LAN is not checked this boot"
fi
trap probe_down EXIT
lan="$(ip -4 route get 1.1.1.1 2>/dev/null | sed -n 's/.* src \([0-9.]*\).*/\1/p')"
reach() {   # blocked = how many listening ports do not answer on $lan; why names them
  local p list=""
  blocked=0
  [ -n "$probe" ] && [ -n "$lan" ] || { blocked=$base_blocked; return 0; }
  for p in $(ss -Hltn 2>/dev/null | awk -v ip="$lan" '{h=$4; sub(/:[^:]*$/, "", h); n=split($4, a, ":"); if (h == "0.0.0.0" || h == "*" || h == "[::]" || h == ip) print a[n]}' | sort -un); do
    ip netns exec "$NS" timeout 3 bash -c "exec 3<>/dev/tcp/$lan/$p" 2>/dev/null || { blocked=$((blocked + 1)); list="$list $p"; }
  done
  [ "$blocked" -le "$base_blocked" ] || { why="aus dem LAN nicht erreichbar auf $lan: Port${list} (eine Firewall? systemctl status firewalld)"; return 1; }
}
check() {
  if [ -n "$gpu" ]; then
    local v; v="$(nvidia-smi --query-gpu=driver_version --format=csv,noheader 2>/dev/null | head -1)"
    [ -n "$v" ] || { why="die GPU antwortet nicht (nvidia-smi)"; return 1; }
    grep -qF ".so.$v" "$ETC/cdi/nvidia.yaml" 2>/dev/null || { why="die CDI-Datei nennt nicht den Treiber $v (systemctl status laib-cdi)"; return 1; }
  fi
  local r; r="$(curl -fsS --max-time 5 http://127.0.0.1:5900/ready 2>/dev/null)" || { why="laib antwortet nicht auf /ready"; return 1; }
  red="$(sed -n 's/.*"red":\([0-9]*\).*/\1/p' <<<"$r")"; red="${red:-0}"
  [ "$red" -le "${base:-0}" ] || { why="$red Dienste rot, nach dem letzten bewiesenen Start ${base:-0}"; return 1; }
  reach
}
end=$((SECONDS + ${LAIB_BOOT_CHECK_TIMEOUT:-900})); ok=""
while :; do
  check && { ok=1; break; }
  [ "$SECONDS" -lt "$end" ] || break
  sleep "${LAIB_BOOT_CHECK_INTERVAL:-10}"
done
if [ -n "$ok" ] || [ -z "$proven" ]; then
  echo "$booted $red $blocked" >"$PROVEN"
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
# the rollback deployment's /etc is /ostree/deploy/<os>/deploy/<id>/etc, on
# /sysroot, which Fedora CoreOS mounts read-only: written in a private mount
# namespace with /sysroot remounted rw, as ostree itself does, and checked
backetc="$ROOT/ostree/deploy/$(sed -n 's/^\* \([^ ]*\) .*/\1/p' <<<"$st")/deploy/$back/etc"
mkdir -p "$ETC/$(dirname "$PAUSE")" && printf '[updates]\nenabled = false\n' >"$ETC/$PAUSE"
unshare --mount -- sh -c 'mount -o remount,rw "$1/sysroot" && mkdir -p "$2/zincati/config.d" && printf "[updates]\nenabled = false\n" >"$2/$3"' _ "$ROOT" "$backetc" "$PAUSE"
[ -s "$backetc/$PAUSE" ] || say "could not pause Zincati in $backetc: it may stage the same update again"
note incident "OS-Update ${booted:0:12} zurückgerollt auf ${proven:0:12}: $why. Zincati pausiert, bis /etc/$PAUSE entfernt ist."
rpm-ostree rollback || { say "rpm-ostree rollback failed"; exit 1; }
sync; systemctl reboot
