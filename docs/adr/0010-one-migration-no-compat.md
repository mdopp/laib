# ADR 0010 — One user, one migration, no compatibility layer

Applies when you consider a feature flag, a dual-write, a legacy reader, or
keeping ServiceBay's format "for now".

- **Status:** Accepted, 2026-10-08; carried out 2026-10-19; its tooling removed
  2026-10-22 (amendment below)

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

## Amendment 2026-10-22: the migration ran, its tooling is gone

The migration ran on the Kraftwerk on migration day, 2026-10-19 (#160); the
box runs Laib and no ServiceBay. At the owner's word ("wir haben auf der box
doch kein servicebay mehr ... können wir da nicht ausmisten?", 2026-10-22)
its scripts, their tests and fixtures, the runbooks `docs/MIGRATION.md` and
`docs/MIGRATION-DAY.md`, the Generalprobe that rehearsed it in a VM and the
`library-move` job that proved the Immich mover were deleted, as the
consequences above said they would be. They live in git history: the last
commit that had them is `0203ec07bcdadaa01305cee8d02eb00512d88f80`
(`git show 0203ec07:docs/MIGRATION.md`; its "Rollback during the weekend"
section is the way back while ServiceBay's data dir is kept).

What stays: Phase 5's `scripts/solaris-off.py` and
`scripts/solaris-links-off.py` (Solaris is still to go off, #540), the
general disk import `scripts/laib-import.py`, and the code comments that say
which ServiceBay rule a Laib rule came from.
