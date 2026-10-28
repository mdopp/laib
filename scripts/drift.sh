#!/usr/bin/env sh
# Docs vs code (#47): cheap checks that catch the drift the review found.
# Each check prints what disagrees; the script fails when any did.
set -u
cd "$(dirname "$0")/.."
bad=0
found() { # found <label> <lines>: print them as drift, mark red
  [ -n "$2" ] || return 0
  echo "$2" | sed "s/^/drift: $1: /"; bad=1
}

# 1. README's layout names directories that exist
missing=""
for d in $(sed -n '/^## Layout/,/^## /p' README.md | grep -oE '^(cmd|internal|operator|templates|usb|werkstatt|docs|testdata|scripts|hooks)[A-Za-z0-9_/.-]*'); do
  [ -e "$d" ] || missing="$missing$d does not exist
"
done
found "README layout" "$missing"

# 2. the Operator's tools are verbs the MCP exposes, also those its skills' scripts call (#839)
lacking=""
for t in $(grep -ohE 'laib_[a-z_]+' operator/*.go operator/cmd/laib-operator/*.go skills/*/run.py 2>/dev/null | sort -u); do
  v=${t#laib_}
  grep -qE "\{Name: \"$v\", Tier: \"[a-z]+\", MCP: true," cmd/laib/main.go || lacking="$lacking$t is no MCP row of the verb table
"
done
found "operator tools" "$lacking"

# 3. the docs' ports are the code's defaults
lease_port=$(grep -oE 'port := [0-9]+' internal/state/load.go | head -1 | grep -oE '[0-9]+')
laib_port=$(grep -oE 'Laib\.Port(, | = )[0-9]+' internal/state/load.go | grep -oE '[0-9]+$')
# a default the grep cannot read is drift too: the check would compare against nothing
found "port defaults readable" "$([ -n "$lease_port" ] && [ -n "$laib_port" ] || echo "lease '$lease_port', laib '$laib_port' in internal/state/load.go")"
found "lease port (code: $lease_port)" "$(grep -rnE ':[0-9]+/api/model-lease' docs/*.md README.md | grep -vE ":$lease_port/api/model-lease" || true)"
found "laib port (code: $laib_port)" "$(grep -rnE '127\.0\.0\.1:[0-9]+/mcp' docs/*.md README.md | grep -vE "127\.0\.0\.1:$laib_port/mcp" || true)"

# 4. counts the docs state match the tree
n=$(ls -d templates/*/ | wc -l | tr -d ' ')
found "template count (tree: $n)" "$(grep -nE 'Templates \([0-9]+\)|[0-9]+ templates' README.md docs/REVIEW.md | grep -vE "\($n\)|$n templates" || true)"

# 5. nothing a test run wrote is tracked
found "tracked artifact" "$(git ls-files | grep -E '\.pyc$|^internal/apply/.*\.(service|timer|conf)$' || true)"

# 6. the release manifest is at the last tag
tag=$(git tag --list 'v*' | sort -V | tail -1 | sed 's/^v//')
stale=""
for f in .release-please-manifest.json version.txt; do
  grep -q "$tag" "$f" || stale="$stale$f is not at the last tag v$tag
"
done
found "release manifest" "$stale"

# 7. a template change brings a new line in its news (#687): the sentence a
# template update's card shows the household. Its tests and README are no
# change of the template, nor is laib.yml's title or beschreibung alone: the
# Box page's name, which the box takes over without a card (#785). Against
# main's merge base; on main itself, nothing.
base=$(git merge-base HEAD "${DRIFT_BASE:-origin/main}" 2>/dev/null || true)
silent=""
for t in $( [ -n "$base" ] && git diff --name-only "$base" -- 'templates/*/*' | grep -vE '^templates/[^/]+/(tests/|README\.md$|news$)' | cut -d/ -f2 | sort -u); do
  [ -d "templates/$t" ] || continue
  [ "$(git diff -U0 "$base" -- "templates/$t" ":(exclude)templates/$t/tests" ":(exclude)templates/$t/README.md" ":(exclude)templates/$t/news" |
    grep -E '^([+-]|Binary)' | grep -vE '^(\+\+\+|---) ' | grep -cvE '^[+-](title|beschreibung):')" -gt 0 ] || continue
  [ "$(git diff "$base" -- "templates/$t/news" | grep -cE '^\+[^+#]')" -gt 0 ] || silent="${silent}templates/$t changed without a new line in templates/$t/news
"
done
found "template news" "$(printf %s "$silent")"

# 8. docs say what is true today (#865): a retired name (ADR 0021's table
# "Retired names") stands in a current doc (README.md, docs/*.md) only where
# its line, its section or its file (**History.** in its first lines) says
# it is past. The ADRs are history by nature and not read.
retired=$(sed -n '/^### Retired names/,/^#/p' docs/adr/0021-names.md 2>/dev/null | sed -nE 's/^\| `?([A-Za-z][A-Za-z0-9_-]*)`? \|.*/\1/p' | grep -vx Name || true)
if [ -z "$retired" ]; then
  found "retired name" "docs/adr/0021-names.md has no table \"Retired names\""
else
  names=$(echo "$retired" | paste -sd'|' -)
  found "retired name" "$(for f in README.md docs/*.md; do [ -f "$f" ] && ! awk 'NR<=6 && index($0, "**History.**") {f=1} END {exit !f}' "$f" && echo "$f"; done | xargs -r awk -v names="$names" '
    FNR == 1 { past = 0 }
    /^#+ / { past = ($0 ~ /[Hh]istory|[Mm]oved out|[Rr]etired|[Gg]one|[Ee]nded|[Ss]uperseded/) }
    past { next }
    /[Rr]etire|[Rr]emoved|is gone|are gone|was gone|[Ee]nded|until #|[Hh]istory|[Ss]uperseded|[Pp]redecessor|[Mm]oved out|ADR 0015|#778/ { next }
    { if (match($0, "(^|[^A-Za-z0-9_-])(" names ")([^A-Za-z0-9_-]|$)")) print FILENAME ":" FNR ": " substr($0, RSTART, RLENGTH + 40) }
  ')"
fi

# 9. the version the docs name is version.txt's (#865): release-please bumps
# every line marked x-release-please-version in its extra-files, so each of
# those files has one, and none carries another version.
ver=$(tr -d ' \n' < version.txt 2>/dev/null)
extra=$(python3 -c 'import json,sys; print("\n".join(f for p in json.load(open(sys.argv[1]))["packages"].values() for f in p.get("extra-files", [])))' release-please-config.json 2>/dev/null || true)
off=""
for f in $extra; do grep -q 'x-release-please-version' "$f" 2>/dev/null || off="$off$f is in release-please's extra-files but has no x-release-please-version line
"; done
off="$off$(grep -rn 'x-release-please-version' README.md docs/*.md 2>/dev/null | grep -v "$ver" | sed "s/\$/ (version.txt: $ver)/" || true)"
found "docs version" "$(printf %s "$off")"

[ "$bad" -eq 0 ] && echo "drift: none"
exit $bad
