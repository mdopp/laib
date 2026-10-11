#!/usr/bin/env sh
# Docs vs code (#47): cheap checks that catch the drift the review found.
# Each check prints what disagrees; the script fails when any did.
set -u
cd "$(dirname "$0")/.."
bad=0
say() { echo "drift: $*"; bad=1; }

# 1. README's layout names directories that exist
sed -n '/^## Layout/,/^## /p' README.md | grep -oE '^(cmd|internal|templates|usb|werkstatt|docs|testdata|scripts|hooks)[A-Za-z0-9_/.-]*' | while read -r d; do
  [ -e "$d" ] || echo "README layout: $d does not exist"
done | { grep . && bad=1; true; } | sed 's/^/drift: /'
if sed -n '/^## Layout/,/^## /p' README.md | grep -oE '^(cmd|internal|templates|usb|werkstatt|docs|testdata|scripts|hooks)[A-Za-z0-9_/.-]*' | while read -r d; do [ -e "$d" ] || exit 1; done; then :; else bad=1; fi

# 2. the Operator's tools are verbs the MCP exposes
for t in $(grep -ohE 'laib_[a-z_]+' internal/operator/*.go cmd/laib-operator/*.go 2>/dev/null | sort -u); do
  v=${t#laib_}
  grep -qE "\"$v\": *true" cmd/laib/mcp.go || say "operator uses $t but mcpVerbs lacks \"$v\""
done

# 3. the docs' ports are the code's defaults
lease_port=$(grep -oE 'port := [0-9]+' internal/state/load.go | head -1 | grep -oE '[0-9]+')
laib_port=$(grep -oE 'Laib.Port = [0-9]+' internal/state/load.go | grep -oE '[0-9]+')
grep -rnE 'model-lease' docs/*.md README.md | grep -E ":[0-9]+/api/model-lease" | grep -vE ":$lease_port/api/model-lease" | sed 's/^/drift: lease port: /' && bad=1
grep -rnE '127\.0\.0\.1:[0-9]+/mcp' docs/*.md README.md | grep -vE "127\.0\.0\.1:$laib_port/mcp" | sed 's/^/drift: laib port: /' && bad=1

# 4. counts that docs state match the tree
n=$(ls -d templates/*/ | wc -l | tr -d ' ')
grep -nE "Templates \([0-9]+\)|[0-9]+ templates" README.md docs/REVIEW.md | grep -vE "\($n\)|$n templates" | sed 's/^/drift: template count: /' && bad=1

# 5. nothing a test run wrote is tracked
git ls-files | grep -E '\.pyc$|^internal/apply/.*\.(service|timer|conf)$' | sed 's/^/drift: tracked artifact: /' && bad=1

# 6. the release manifest is at the last tag
tag=$(git tag --list 'v*' | sort -V | tail -1 | sed 's/^v//')
for f in .release-please-manifest.json version.txt; do
  grep -q "$tag" "$f" || say "$f is not at the last tag v$tag"
done

[ "$bad" -eq 0 ] && echo "drift: none"
exit $bad
