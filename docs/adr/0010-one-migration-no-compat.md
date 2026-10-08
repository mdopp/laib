# ADR 0010 — One user, one migration, no compatibility layer

Applies when you consider a feature flag, a dual-write, a legacy reader, or
keeping ServiceBay's format "for now".

- **Status:** Accepted, 2026-10-08

## Context

Exactly one box runs ServiceBay, operated by the author. There is no fleet,
no other user, no upgrade path to protect.

## Decision

ServiceBay is migrated once, in the order in `docs/MIGRATION.md`, with a
one-shot `laib migrate servicebay` that reads its config and secrets a single
time. Laib never reads ServiceBay's stores at runtime, never writes its
format, and carries no flag for it. A service belongs to exactly one control
plane at any moment.

## Consequences

- The migration script is throw-away code and lives in `internal/migrate`
  until step 7 of the plan is done, then it is deleted.
- Rollback during the migration is per service (re-enable the old unit); after
  it, a restore of the kept ServiceBay data dir for 30 days.
