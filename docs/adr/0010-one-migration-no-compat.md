# ADR 0010 — One user, one migration, no compatibility layer

Applies when you consider a feature flag, a dual-write, a legacy reader, or
keeping ServiceBay's format "for now".

- **Status:** Accepted, 2026-10-08

## Context

Exactly one box runs ServiceBay, operated by the author. There is no fleet,
no other user, no upgrade path to protect.

## Decision

ServiceBay is migrated once, in the order in `docs/MIGRATION.md`. The kernel
knows nothing about ServiceBay: no `laib migrate` verb, no reader for its
stores, no flag. The one-time translation is `scripts/migrate-servicebay.py`,
a throw-away script run by the operator (or by Claude in a session) that
reads ServiceBay's `config.json` and templates a single time and writes draft
service folders for review, outside the state repo (#226). Secrets are moved
by hand with `laib secret set`; there are 48 of them and most are
re-generated. The ones the existing data was made with are carried by
`scripts/carry-servicebay-secrets.sh`, a second throw-away script that pipes
each from ServiceBay's store into `laib secret set` (#228). A
service belongs to exactly one control plane at any moment.

## Consequences

- The migration scripts live in `scripts/` until step 7 of the plan is done,
  then they are deleted. They never become part of the binary.
- Rollback during the migration is per service (re-enable the old unit); after
  it, a restore of the kept ServiceBay data dir for 30 days.
