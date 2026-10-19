#!/usr/bin/env sh
# One budget per program (ADR 0023, ADR 0005 amendment #309): the kernel
# (cmd/, internal/), the Operator (operator/) and each program that moved out
# of the kernel. Each: production under its cap; test lines are counted
# but not capped (owner, 2026-10-18). They meet over MCP, files or HTTP,
# never by import.
#
# Crossing a cap means raising it in the same PR, with its reason next to
# the number and the owner's approval; at 95 % this warns, and on main CI
# opens or updates the "Kern-Diät" issue (scripts/kern-diaet.sh). Lines are
# counted as gofmt writes them: squeezing code to fit is a review finding.
#
# --diet prints that issue's body and exits 0 when the kernel is at 95 % or
# more, 3 when it is not.
set -e
cd "$(dirname "$0")/.."
bad=0
budget() { # budget <name> <cap> <dir…>
  name=$1 cap=$2; shift 2
  prod=$(find "$@" -name '*.go' ! -name '*_test.go' -exec cat {} + 2>/dev/null | wc -l)
  test=$(find "$@" -name '*_test.go' -exec cat {} + 2>/dev/null | wc -l)
  [ "$name" = kernel ] && kernel=$prod kernelcap=$cap
  [ -n "$diet" ] && return
  echo "$name production: $prod  tests: $test  (cap $cap)"
  [ "$prod" -lt "$cap" ] || { echo "$name over $cap lines (ADR 0005, 0023)"; bad=1; }
  [ "$((prod * 100))" -lt "$((cap * 95))" ] || [ "$prod" -ge "$cap" ] || echo "warning: $name at $prod of $cap lines, 95 % or more (#309)"
}
diet=""
[ "${1:-}" = --diet ] && diet=1
lines() { find "$@" -name '*.go' ! -name '*_test.go' -exec cat {} + 2>/dev/null | wc -l; }
# 15,000 (owner, 2026-10-17, #309): the structured kernel needs ~13,150 with
# the lease; the rest, ~14 %, is room for the open trust-kernel and chore work
budget kernel 15000 cmd internal
budget operator 3000 operator
# the programs that moved out of the kernel (#309): their size then, plus a quarter
budget stick 1800 stick         # the laptop's install-stick builder, 1,468
budget frage 400 frage          # "was ist …", 261
budget automat 600 automat      # the Bote's HA automations, 421
budget persona 900 persona      # the Bote's persona engine, 701
budget mcpserver 200 mcpserver  # the Bote's tools' MCP server, 111
budget disk 700 disk            # laib-disk: the data RAID, SMART, thermal (#443), 564

# the boundary: the Operator's code is not in the kernel's tree, and
# neither program imports the other
inside=$(grep -rlE '^package operator$' --include='*.go' cmd internal || true)
[ ! -d cmd/laib-operator ] || inside="$inside cmd/laib-operator"
[ -z "$inside" ] || { echo "Operator code inside the kernel's count: $inside"; bad=1; }
up=$(grep -rlE '"github.com/mdopp/laib/(internal|cmd)/' --include='*.go' operator 2>/dev/null || true)
[ -z "$up" ] || { echo "the Operator imports the kernel: $up"; bad=1; }
down=$(grep -rlE '"github.com/mdopp/laib/operator' --include='*.go' cmd internal || true)
[ -z "$down" ] || { echo "the kernel imports the Operator: $down"; bad=1; }
# a program that moved out of the kernel imports none of it, and the kernel
# imports none of them
for prog in stick frage automat persona mcpserver disk; do
  up=$(grep -rlE '"github.com/mdopp/laib/(internal|cmd)/' --include='*.go' "$prog" 2>/dev/null || true)
  [ -z "$up" ] || { echo "$prog imports the kernel: $up"; bad=1; }
  down=$(grep -rlE "\"github.com/mdopp/laib/$prog[/\"]" --include='*.go' cmd internal || true)
  [ -z "$down" ] || { echo "the kernel imports $prog: $down"; bad=1; }
done
if [ -n "$diet" ]; then
  [ "$((kernel * 100))" -ge "$((kernelcap * 95))" ] || exit 3
fi

# inside the kernel: the box chores (ADR 0005 amendment #309) report and
# maintain; the trust kernel never depends on them. Only cmd/laib and other
# chores import a chore.
chores="diag registry pages"
for c in $chores; do
  users=$(grep -rlE "\"github.com/mdopp/laib/internal/$c\"" --include='*.go' internal | grep -vE "^internal/($(echo $chores | tr ' ' '|'))/" || true)
  [ -z "$users" ] || { echo "the trust kernel imports the chore $c: $users"; bad=1; }
done

if [ -n "$diet" ]; then
  echo "The kernel is at **$kernel of $kernelcap** production lines (\`scripts/loc.sh\`), 95 % or more."
  echo
  echo "What sits in the kernel but is not the trust kernel (ADR 0019 §2, ADR 0023 §2): candidates to move out, as the Operator (#268) and the programs (#309) did. Measured by the owner's maxim (#246): easier to maintain, nothing worse for the family. Squeezing lines is not a fix."
  echo
  echo "| Package | Lines | What it is |"
  echo "|---|---:|---|"
  for c in $chores; do echo "| \`internal/$c\` | $(lines internal/$c) | box chore |"; done
  echo "| \`internal/lease\`, \`cmd/laib-lease\` | $(lines internal/lease cmd/laib-lease) | the llama template's lease; stays by the owner's decision (2026-10-17), counts |"
  echo "| \`cmd/laib\` | $(lines cmd/laib) | the small verbs; \`internal/hands\` (secret, token, operator) is the next cut |"
  echo
  echo "Crossing the cap means raising it in the same PR, with its reason in \`scripts/loc.sh\` and the owner's approval (#309)."
  echo
  echo "This issue is opened and updated by CI on main (\`scripts/kern-diaet.sh\`)."
  exit 0
fi
exit $bad
