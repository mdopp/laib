#!/usr/bin/env bash
# One vm-install per code state (#604). Prints "<run> <sha> <tree> <why>" and
# exits 0 when a green vm-install already proved <commit>'s code. Nonzero when
# none did, or when it cannot tell (gh's error is on stderr): the caller then
# runs or waits for a run of its own, as before #604. Run from the repo root.
#
#   scripts/vm-install-reuse.sh <commit> [depth]
#
# A run proved a tree when its job "proven: tree <T>" succeeded: vm-install
# names that job after the tree it checked out, and it runs only when every
# install part was green. The claim is taken only from a run whose head
# commit has exactly that tree, so the workflow that made it is part of the
# tree it vouches for (a PR whose test merge differs from its head proves
# nothing here). <commit> reuses the run when its tree differs from <T> in
# release-please's own files at most (CHANGELOG.md, version.txt, the
# manifest and the extra-files of release-please-config.json).
#
# Candidates are the commits near <commit>: <depth> first parents (default
# 8) and each one's parents, so main's merge of a train finds the train head.
# gh is asked twice at most per candidate run; the run list is one call.
set -euo pipefail
commit=${1:?usage: vm-install-reuse.sh <commit> [depth]}
depth=${2:-8}
commit=$(git rev-parse --verify "$commit^{commit}")

release_files() {
  printf '%s\n' CHANGELOG.md version.txt .release-please-manifest.json
  [ -f release-please-config.json ] || return 0
  jq -r '
    def path_of: if type == "string" then . else .path end;
    ((."extra-files" // [])[] | path_of),
    ((.packages // {}) | to_entries[] | .key as $k
      | ((.value."extra-files" // [])[] | path_of), (.value."changelog-path" // empty)
      | if $k == "." then . else "\($k)/\(.)" end)' release-please-config.json
}
allowed=$(release_files | sed 's|^\./||' | sort -u)

candidates=$(for c in $(git rev-list --first-parent -n "$depth" "$commit"); do git rev-list --parents -n 1 "$c" | tr ' ' '\n'; done | awk 'NF && !seen[$0]++')

runs=$(gh run list --workflow vm-install --status success --limit 100 --json databaseId,headSha | jq -r '.[] | "\(.databaseId) \(.headSha)"')
while read -r id sha; do
  [ -n "${id:-}" ] || continue
  grep -qx "$sha" <<<"$candidates" || continue
  head_tree=$(git rev-parse --verify -q "$sha^{tree}") || continue
  jobs=$(gh run view "$id" --json jobs | jq -r '.jobs[] | select(.conclusion == "success") | .name')
  for tree in $(sed -n 's/^proven: tree \([0-9a-f]\{40\}\)$/\1/p' <<<"$jobs"); do
    [ "$tree" = "$head_tree" ] || { echo "run $id claims tree $tree, its head $sha has $head_tree: not taken" >&2; continue; }
    diff=$(git diff --name-only "$tree" "$commit")
    other=$(grep -vxF -f <(printf '%s\n' "$allowed") <<<"$diff" || true)
    if [ -n "$other" ]; then
      echo "run $id ($sha) proved another tree: $(tr '\n' ' ' <<<"$other")" >&2
      continue
    fi
    if [ -z "$diff" ]; then why=same-tree; else why="only-release-files:$(paste -sd, <<<"$diff")"; fi
    echo "$id $sha $tree $why"
    exit 0
  done
done <<<"$runs"
exit 1
