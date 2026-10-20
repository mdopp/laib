#!/bin/bash
# laib-host-image (#368), root, every 15 minutes: the host OS follows the
# image pinned in the state (host/image: "# tag: <tag>", then the rebase
# reference by digest). A pinned image that is not booted is staged with
# rpm-ostree; the reboot into it waits for laib's reboot lock (#467: the
# window after the backup, the backup not running, laib green, the running
# deployment proven). The boot check proves the new deployment or rolls it
# back once. A pause (Zincati's updates.enabled = false: by hand, or the boot
# check's after a rollback) holds it all. On a box booted from an image
# Zincati's graph does not know (uCore) this is the one update path, and
# install-box.sh masks Zincati there (#486).
set -u
STATE="${LAIB_STATE:-/mnt/data/laib/state}"; ETC="${LAIB_ETC:-/etc}"   # the test harness only
say() { echo "laib-host-image: $*"; }
note() { runuser -u core -- env LAIB_HOME=/mnt/data/laib LAIB_ACTOR=laib /var/opt/laib/laib note --kind "$1" "$2" >/dev/null 2>&1 || true; }
want="$(grep -v '^#' "$STATE/host/image" 2>/dev/null | grep -m1 .)" || { say "no host/image in the state: nothing to follow"; exit 0; }
st="$(rpm-ostree status --json)" || { say "rpm-ostree status failed"; exit 1; }
# a deployment runs the pin when its reference is the pin, or its digest is
# the pin's (a box rebased by tag runs that digest already: no new deployment)
runs() { jq -e --arg w "$want" --arg d "${want##*@}" ".deployments[] | select(.$1) | select(.\"container-image-reference\" == \$w or .\"container-image-reference-digest\" == \$d)" <<<"$st" >/dev/null; }
! runs booted || exit 0
# the one pause (#486): Zincati's drop-ins, read as Zincati reads them: in
# name order, the last one that sets updates.enabled decides
pause=""
for f in "$ETC"/zincati/config.d/*.toml; do
  case "$(sed -n 's/^[[:space:]]*enabled[[:space:]]*=[[:space:]]*\([a-z]*\).*/\1/p' "$f" 2>/dev/null | tail -1)" in
    false) pause="$f" ;;
    true) pause="" ;;
  esac
done
[ -z "$pause" ] || { say "held by $pause: $want waits until the pause is lifted"; exit 0; }
if ! runs staged; then
  say "staging $want"
  rpm-ostree rebase --bypass-driver "$want" || { note incident "OS-Image $want ließ sich nicht bereitstellen (rpm-ostree rebase): journalctl -u laib-host-image"; exit 1; }
  note watch "OS-Image $want bereitgestellt: der Neustart kommt im Update-Fenster, wenn alles grün ist"
fi
code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 30 -X POST -H 'fleet-lock-protocol: true' \
  -d '{"client_params":{"id":"laib-host-image","group":"default"}}' http://127.0.0.1:5900/v1/pre-reboot)"
[ "$code" = 200 ] || { say "staged, the reboot lock says not now ($code)"; exit 0; }
say "rebooting into $want"
sync; systemctl reboot
