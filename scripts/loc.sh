#!/usr/bin/env sh
# Two programs, two budgets (ADR 0023): the kernel (cmd/, internal/) and the
# Operator (operator/). Each: production under its cap, tests at most equal
# to production (docs/TESTING.md). They meet over MCP, never by import.
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

# the boundary: the Operator's code is not in the kernel's tree, and
# neither program imports the other
inside=$(grep -rlE '^package operator$' --include='*.go' cmd internal || true)
[ ! -d cmd/laib-operator ] || inside="$inside cmd/laib-operator"
[ -z "$inside" ] || { echo "Operator code inside the kernel's count: $inside"; bad=1; }
up=$(grep -rlE '"github.com/mdopp/laib/(internal|cmd)/' --include='*.go' operator 2>/dev/null || true)
[ -z "$up" ] || { echo "the Operator imports the kernel: $up"; bad=1; }
down=$(grep -rlE '"github.com/mdopp/laib/operator' --include='*.go' cmd internal || true)
[ -z "$down" ] || { echo "the kernel imports the Operator: $down"; bad=1; }
exit $bad
