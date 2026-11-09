# ADR 0033 — The copy off the box is append-only: a bucket with Object Lock, no forget or prune from the box

Applies when you change where or how `backup.offsite` stores its copy, when
you are about to add a `forget`, `prune` or delete against the off-box repo,
or when you weigh a backup target against a break-in rather than a dead disk.

- **Status:** Accepted, 2026-11-09
- **Deciders:** the owner (mdopp), on the security plan („Daten sicher,
  ohne Sperren“), decision K: „Cloud mit Löschsperre“ over a restic
  append-only server on the Fritz!Box NAS.

## Context

`laib status` showed one backup, on the disk it protects. `backup.offsite`
(#391) copies every green backup to a second restic repo, but the box holds
everything that repo needs: `RESTIC_PASSWORD` in `secrets.age`, the NAS's
login, and the right to `forget --prune`. Root on the box — an admin who
means it, or a burglar — can delete or encrypt both copies. That is the
ransom case, and the 3-2-1-1-0 rule's "1": one copy nobody on the box can
delete.

Two ways: a restic REST server in append-only mode on a second device at
home, or a cloud bucket with Object Lock. The first needs a device to keep
alive and burns with the house.

## Decision

1. The off-box repo may be an S3 bucket (restic's `s3:` backend, no rclone)
   with versioning and Object Lock in compliance mode. `box.yml
   backup.offsite.objectLock: true` says so.
2. Against a locked repo the box only adds: `restic copy` after every green
   backup, `restic check` and the restore test. It never runs `forget` or
   `prune` there. The retention is the bucket's lock, which the box cannot
   change; pruning old data is the owner's, from another computer, with a
   key the box never had.
3. The bucket's keys are box secrets (`AWS_ACCESS_KEY_ID`,
   `AWS_SECRET_ACCESS_KEY` in `secrets.age`, named in
   `backup.offsite.secrets`), entered by the owner; they reach restic as
   environment, never argv, box.yml or the repo. No agent creates an
   account or a key.
4. Any S3 provider with Object Lock will do (Backblaze B2, Hetzner Object
   Storage, …); the provider is the owner's. The key must not be allowed to
   delete object versions or bypass the lock; that is checked once when it
   is set up (docs/BACKUP.md), because Laib cannot see a key's rights.

## Consequences

- A break-in can no longer take the last copy: what was written stays for
  the lock period, and with a key that cannot delete versions, beyond.
  The content stays restic ciphertext; the provider reads nothing.
- The bucket only grows. restic deduplicates, so it grows with what
  changes; a yearly prune by the owner keeps it in check.
- Restoring after an attack means restoring the bucket's versions before
  the date (the provider's console, or `rclone serve restic
  --s3-version-at`), then `laib restore` as usual.
- The key and the bucket's address go onto the recovery sheet and into
  Bitwarden by hand: `secrets.age` is inside the backup it would open.
- Not measured here: how restic behaves against a given provider's lock
  (its lock files are deleted, which hides them in a versioned bucket).
  The first `laib backup` after the setup is the measurement; its journal
  line with reason `offsite` says green or why not.
