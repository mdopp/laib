#!/usr/bin/env sh
# Production vs test lines (docs/TESTING.md): tests may not exceed production.
set -e
cd "$(dirname "$0")/.."
prod=$(find cmd internal -name '*.go' ! -name '*_test.go' -exec cat {} + | wc -l)
test=$(find cmd internal -name '*_test.go' -exec cat {} + | wc -l)
echo "production: $prod  tests: $test"
[ "$prod" -lt 15000 ] || { echo "kernel over 15k lines (ADR 0005)"; exit 1; }
[ "$test" -le "$prod" ] || { echo "more test lines than production (docs/TESTING.md)"; exit 1; }
