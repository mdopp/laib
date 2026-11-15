#!/usr/bin/env sh
# The daily issue check of the security concept (owner, 2026-11-15): a point
# of docs/sicherheit.yml that is not „läuft“ names its issues; when one of
# them is closed, the point is stale. scripts/c4.sh --issues finds them; this
# opens the issue "Sicherheitskonzept: Stand nachtragen", or updates the open
# one, and closes it when every point is current again. Run by
# .github/workflows/architektur.yml, never in a PR: it gates nothing.
# Needs gh and GH_TOKEN with issues: write.
set -e
cd "$(dirname "$0")/.."
title="Sicherheitskonzept: Stand nachtragen"
body=$(mktemp)
trap 'rm -f "$body"' EXIT
rc=0
scripts/c4.sh --issues > "$body" || rc=$?
n=$(gh issue list --state open --search "in:title $title" --json number,title --jq ".[] | select(.title == \"$title\") | .number" | head -1)
case $rc in
  0)
    cat "$body"
    [ -z "$n" ] || gh issue close "$n" --comment "Jeder offene Punkt nennt wieder ein offenes Issue."
    ;;
  1)
    if [ -n "$n" ]; then
      gh issue edit "$n" --body-file "$body"
      echo "c4-punkte: updated #$n"
    else
      gh issue create --title "$title" --body-file "$body"
    fi
    ;;
  *) cat "$body"; exit "$rc" ;;
esac
