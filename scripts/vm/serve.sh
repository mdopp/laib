#!/usr/bin/env bash
# Build this commit's binaries under the release's asset names, plus VERSION and
# install-box.sh (what install-box.sh --source expects), and serve them on
# 127.0.0.1:8000 for the rest of the job: QEMU's user net shows the runner to
# the guest as 10.0.2.2, so the box installs from http://10.0.2.2:8000 (#38).
# $1 = the version string (the commit's short sha). Run from the repo root.
set -euo pipefail
v=${1:?version}
mkdir -p dist
for b in laib laib-operator laib-lease; do CGO_ENABLED=0 go build -ldflags "-s -w -X main.Version=$v" -o dist/$b-linux-amd64 ./cmd/$b; done
cp scripts/install-box.sh dist/install-box.sh
(cd dist && sha256sum laib-*-amd64 > SHA256SUMS && cat SHA256SUMS)
printf '%s' "$v" > dist/VERSION
nohup python3 -m http.server 8000 --bind 127.0.0.1 --directory dist > http.log 2>&1 &
for i in $(seq 1 30); do curl -fsS http://127.0.0.1:8000/VERSION >/dev/null 2>&1 && break; sleep 1; done
echo "serving $(curl -fsS http://127.0.0.1:8000/VERSION) on :8000"
