#!/usr/bin/env sh
# One budget per program (ADR 0023, ADR 0005 amendment #309): the kernel
# (cmd/, internal/), the Operator (operator/) and each program that moved out
# of the kernel. Each: production under its cap, tests at most equal to
# production (docs/TESTING.md). They meet over MCP, files or HTTP, never by
# import.
set -e
cd "$(dirname "$0")/.."
bad=0
budget() { # budget <name> <cap> <dir…>
  name=$1 cap=$2; shift 2
  prod=$(find "$@" -name '*.go' ! -name '*_test.go' -exec cat {} + 2>/dev/null | wc -l)
  test=$(find "$@" -name '*_test.go' -exec cat {} + 2>/dev/null | wc -l)
  echo "$name production: $prod  tests: $test  (cap $cap)"
  [ "$prod" -lt "$cap" ] || { echo "$name over $cap lines (ADR 0005, 0023)"; bad=1; }
  [ "$test" -le "$prod" ] || { echo "$name: more test lines than production (docs/TESTING.md)"; bad=1; }
}
budget kernel 16000 cmd internal
budget operator 3000 operator
# the programs that moved out of the kernel (#309): their size then, plus a quarter
budget stick 1800 stick         # the laptop's install-stick builder, 1,468
budget frage 400 frage          # "was ist …", 261
budget automat 600 automat      # the Bote's HA automations, 421
budget persona 900 persona      # the Bote's persona engine, 701
budget mcpserver 200 mcpserver  # the Bote's tools' MCP server, 111

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
for prog in stick frage automat persona mcpserver; do
  up=$(grep -rlE '"github.com/mdopp/laib/(internal|cmd)/' --include='*.go' "$prog" 2>/dev/null || true)
  [ -z "$up" ] || { echo "$prog imports the kernel: $up"; bad=1; }
  down=$(grep -rlE "\"github.com/mdopp/laib/$prog[/\"]" --include='*.go' cmd internal || true)
  [ -z "$down" ] || { echo "the kernel imports $prog: $down"; bad=1; }
done
# inside the kernel: the box chores (ADR 0005 amendment #309) report and
# maintain; the trust kernel never depends on them. Only cmd/laib and other
# chores import a chore.
chores="diag disk intake registry"
for c in $chores; do
  users=$(grep -rlE "\"github.com/mdopp/laib/internal/$c\"" --include='*.go' internal | grep -vE "^internal/($(echo $chores | tr ' ' '|'))/" || true)
  [ -z "$users" ] || { echo "the trust kernel imports the chore $c: $users"; bad=1; }
done
exit $bad
