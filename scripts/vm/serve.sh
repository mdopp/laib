#!/usr/bin/env bash
# Build this commit's binaries under the release's asset names, plus VERSION and
# install-box.sh (what install-box.sh --source expects), and serve them on
# 127.0.0.1:8000 for the rest of the job: QEMU's user net shows the runner to
# the guest as 10.0.2.2, so the box installs from http://10.0.2.2:8000 (#38).
# $1 = the version string (the commit's short sha). Run from the repo root.
# $2 = build (build dist/ only, for vm-install's build job), serve (serve the
# dist/ a build job made, #343), or nothing: both.
set -euo pipefail
v=${1:?version} mode=${2:-}
if [ "$mode" != serve ]; then
mkdir -p dist
# laib-operator, laib-notify, laib-haushalt and laib-lease are containers
# (ADR 0027): laib pins this commit's images when the build job pushed them
# (OPERATOR_IMAGE, NOTIFY_IMAGE, HAUSHALT_IMAGE, LEASE_IMAGE)
pin=""; r=github.com/mdopp/laib/internal/render
[ -z "${OPERATOR_IMAGE:-}" ] || pin="$pin -X $r.OperatorImage=$OPERATOR_IMAGE"
[ -z "${NOTIFY_IMAGE:-}" ] || pin="$pin -X $r.NotifyImage=$NOTIFY_IMAGE"
[ -z "${HAUSHALT_IMAGE:-}" ] || pin="$pin -X $r.HaushaltImage=$HAUSHALT_IMAGE"
[ -z "${LEASE_IMAGE:-}" ] || pin="$pin -X $r.LeaseImage=$LEASE_IMAGE"
for b in laib laib-disk; do src=./cmd/$b; [ $b = laib-disk ] && src=./disk/cmd/$b; x=""; [ $b = laib ] && x=$pin; CGO_ENABLED=0 go build -ldflags "-s -w -X main.Version=$v$x" -o dist/$b-linux-amd64 $src; done
cp scripts/install-box.sh dist/install-box.sh
cp scripts/boot-check.sh dist/laib-boot-check.sh
cp scripts/host-image.sh dist/laib-host-image.sh
cp scripts/self-update.sh dist/laib-self-update.sh   # #547
(cd dist && sha256sum laib-*-amd64 laib-boot-check.sh laib-host-image.sh laib-self-update.sh > SHA256SUMS && cat SHA256SUMS)
printf '%s' "$v" > dist/VERSION
fi
[ "$mode" != build ] || exit 0
[ "$(cat dist/VERSION)" = "$v" ] || { echo "dist/ is $(cat dist/VERSION), not $v" >&2; exit 1; }
nohup python3 -m http.server 8000 --bind 127.0.0.1 --directory dist > http.log 2>&1 &
for i in $(seq 1 30); do curl -fsS http://127.0.0.1:8000/VERSION >/dev/null 2>&1 && break; sleep 1; done
echo "serving $(curl -fsS http://127.0.0.1:8000/VERSION) on :8000"
