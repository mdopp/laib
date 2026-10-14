#!/usr/bin/env sh
# Make a persona package's icon.png from its icon.svg (#191, ADR 0020): a
# 512 px square with the SVG's transparency, the image a phone draws (Android
# has no SVG). Run it after every icon.svg change and commit both files.
# Needs resvg (https://github.com/linebender/resvg/releases, one static binary).
#
#   scripts/persona-icon.sh personas/joshua [<persona dir>…]
set -eu
[ $# -gt 0 ] || { echo "usage: $0 <persona dir>…" >&2; exit 2; }
for d in "$@"; do
  resvg -w 512 -h 512 "$d/icon.svg" "$d/icon.png"
done
