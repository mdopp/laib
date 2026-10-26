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

Phase 5's `scripts/solaris-off.py` and `scripts/solaris-links-off.py`
(#540), the carry-over of ServiceBay's OIDC clients in the auth pre-start
(#245), the removal of ServiceBay's `auth_oidc:` block in Home Assistant's
pre-start (#253) and the Werkstatt's `werkstatt-mcp-tidy` (#392) followed
on 2026-10-24 (owner: "Altlasten können weg"); they too live in git history.

What stays: the general disk import `scripts/laib-import.py`, and the code
comments that say which ServiceBay rule a Laib rule came from.

## Amendment 2026-10-26: the carried values are rotated, then the data dir goes (#665)

The passwords `carry-servicebay-secrets.sh` carried (LLDAP's and Immich's,
Jellyfin's, AdGuard's and Home Assistant's admins, the Immich and Paperless
databases, Authelia's storage key, MQTT and the share) still sit readable in
ServiceBay's data dir beside the data dir (`/mnt/data/servicebay`): in
plaintext in its unit history, 116 files, and sealed in `config.json` with
`secret.key` next to it. Owner, 2026-10-25: rotate them now and delete the
dir right after, not 30 days after migration day.

- `laib diag`, probe `servicebay`, names every secret whose current value is
  still in that dir, plain or unsealed with its key, by name and service, never
  the value. With none left it names the dir, with `laib servicebay remove` as
  the repair. This is the one place the kernel reads ServiceBay's store, and
  only to measure that nothing of it is still in use; probe and verb live in
  `internal/diag/servicebay.go` and go with the dir.
- A data-bound secret (#353) rotates when its template marks it `rotate: true`:
  `laib secret rotate` keeps the new value aside (`<NAME>.next` in secrets.age),
  hands it to the template's hook as `LAIB_ROTATE_<NAME>`, and takes it only
  when the hook hands it back (the app took it); a hand-back after a failed
  `laib-exec` is not taken. A red apply after the change rotates back, once.
  This is general, not ServiceBay's, and stays.
- `scripts/rotate-carried.sh` (throw-away, goes with the dir) orders the
  rotations, one service at a time, green before and after, and stops at the
  first red; what the owner's devices or a provider hold (MQTT, the share, a
  mail password) and Authelia's storage key are the owner's, with `--wie`.
- `laib servicebay remove` (tier destroy, humans only, so the Inbox only)
  refuses while a secret still has a value from the dir: the snapshot it takes
  first (restic, tag `pre-destroy`) must hold dead values only.
