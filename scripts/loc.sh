#!/usr/bin/env sh
# Production vs test lines (docs/TESTING.md): tests may not exceed production.
set -e
cd "$(dirname "$0")/.."
prod=$(find cmd internal -name '*.go' ! -name '*_test.go' -exec cat {} + | wc -l)
test=$(find cmd internal -name '*_test.go' -exec cat {} + | wc -l)
echo "production: $prod  tests: $test"
[ "$prod" -lt 16000 ] || { echo "kernel over 16k lines (ADR 0005, amended 2026-10-13)"; exit 1; }
[ "$test" -le "$prod" ] || { echo "more test lines than production (docs/TESTING.md)"; exit 1; }
