#!/usr/bin/env sh
# The owner's two overview pages, docs/C4.md and docs/SICHERHEIT.md, and
# README.md's c4 section, generated from the code and from docs/c4.yml and
# docs/sicherheit.yml by scripts/c4 (owner, 2026-11-15). Modes: none writes,
# --check (CI, every PR), --gate <main's checkout> (CI, every PR: the
# judgment gate), --issues (daily), --facts. See scripts/c4/main.go.
set -e
cd "$(dirname "$0")/.."
exec go run ./scripts/c4 "$@"
