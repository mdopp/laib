#!/usr/bin/env bash
# Runs move-servicebay-data.py (#229) on the box, as core: in the laib-hooks
# image (Fedora CoreOS has no Python), as the container's root, which is root
# over the box's container uids, so ownership is kept. Syncthing's podman
# volume is mounted by name when it exists.
#
# Usage: scripts/move-servicebay-data.sh [--dry-run] <service>...
#   DATA_DIR (/mnt/data/stacks), QUADLET (~/.config/containers/systemd),
#   HOOKS_IMAGE (ghcr.io/mdopp/laib-hooks:latest) override the defaults.
set -euo pipefail
data=${DATA_DIR:-/mnt/data/stacks}
quadlet=${QUADLET:-$HOME/.config/containers/systemd}
here=$(cd "$(dirname "$0")" && pwd)
vol=()
if podman volume exists file-share-syncthing-config 2>/dev/null; then
  vol=(-v file-share-syncthing-config:/servicebay-volumes/file-share-syncthing-config:ro)
fi
exec podman run --rm --network none --security-opt label=disable \
  -v "$data:$data" -v "$quadlet:/quadlet:ro" -v "$here:/m:ro" "${vol[@]}" \
  "${HOOKS_IMAGE:-ghcr.io/mdopp/laib-hooks:latest}" \
  python3 /m/move-servicebay-data.py --data-dir "$data" --quadlet /quadlet --volumes /servicebay-volumes "$@"
