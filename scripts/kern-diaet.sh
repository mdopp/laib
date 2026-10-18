#!/usr/bin/env sh
# The "Kern-Diät" issue (#309): when the kernel reaches 95 % of its cap,
# CI on main opens it, or updates the open one, with what sits in the
# kernel but is not the trust kernel (scripts/loc.sh --diet). Below 95 %
# it does nothing. Needs gh and GH_TOKEN with issues: write.
set -e
cd "$(dirname "$0")/.."
title="Kern-Diät"
body=$(mktemp)
trap 'rm -f "$body"' EXIT
rc=0
scripts/loc.sh --diet > "$body" || rc=$?
[ "$rc" = 3 ] && { echo "kern-diaet: kernel under 95 %, nothing to do"; exit 0; }
[ "$rc" = 0 ] || exit "$rc"
n=$(gh issue list --state open --search "in:title $title" --json number,title --jq ".[] | select(.title == \"$title\") | .number" | head -1)
if [ -n "$n" ]; then
  gh issue edit "$n" --body-file "$body"
  echo "kern-diaet: updated #$n"
else
  gh issue create --title "$title" --body-file "$body"
fi
