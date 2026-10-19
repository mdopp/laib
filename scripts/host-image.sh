#!/bin/bash
# laib-host-image (#368), root, every 15 minutes: the host OS follows the
# image pinned in the state (host/image: "# tag: <tag>", then the rebase
# reference by digest). A pinned image that is not booted is staged with
# rpm-ostree; the reboot into it waits for laib's reboot lock (#467: the
# window after the backup, the backup not running, laib green, the running
# deployment proven). The boot check proves the new deployment or rolls it
# back once. A pause (Zincati's updates.enabled = false: by hand, or the boot
# check's after a rollback) holds it all.
set -u
STATE="${LAIB_STATE:-/mnt/data/laib/state}"; ETC="${LAIB_ETC:-/etc}"   # the test harness only
say() { echo "laib-host-image: $*"; }
note() { runuser -u core -- env LAIB_HOME=/mnt/data/laib LAIB_ACTOR=laib /var/opt/laib/laib note --kind "$1" "$2" >/dev/null 2>&1 || true; }
want="$(grep -v '^#' "$STATE/host/image" 2>/dev/null | grep -m1 .)" || { say "no host/image in the state: nothing to follow"; exit 0; }
st="$(rpm-ostree status --json)" || { say "rpm-ostree status failed"; exit 1; }
ref() { jq -r ".deployments[] | select(.$1) | .\"container-image-reference\" // empty" <<<"$st"; }
[ "$(ref booted)" != "$want" ] || exit 0
for f in "$ETC"/zincati/config.d/*.toml; do
  grep -qE '^\s*enabled\s*=\s*false' "$f" 2>/dev/null && { say "held by $f: $want waits until the pause is lifted"; exit 0; }
done
if [ "$(ref staged)" != "$want" ]; then
  say "staging $want"
  rpm-ostree rebase --bypass-driver "$want" || { note incident "OS-Image $want ließ sich nicht bereitstellen (rpm-ostree rebase): journalctl -u laib-host-image"; exit 1; }
  note watch "OS-Image $want bereitgestellt: der Neustart kommt im Update-Fenster, wenn alles grün ist"
fi
code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 30 -X POST -H 'fleet-lock-protocol: true' \
  -d '{"client_params":{"id":"laib-host-image","group":"default"}}' http://127.0.0.1:5900/v1/pre-reboot)"
[ "$code" = 200 ] || { say "staged, the reboot lock says not now ($code)"; exit 0; }
say "rebooting into $want"
sync; systemctl reboot
