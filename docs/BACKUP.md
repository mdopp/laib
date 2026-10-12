# Laib — backup and restore

## Today (ServiceBay, 2026-10-09, measured on the box)

Three backup modules with three concepts:

| Module | What | Status on the box |
|---|---|---|
| `lib/systemBackup.ts` | full-box tar, local or SSH | not used |
| `lib/backup/service.ts` | scheduled copy of `/mnt/data` to SMB, history, email | `enabled: false`, last run errored (`/mnt/backup` missing) |
| `lib/externalBackup/*` | per-service manifest (keep paths, strip rules) to the Fritz!Box NAS, auto-restore on reinstall | `partial`: 13 of 22 services ok, 9 undeclared, file-share incomplete |

The per-service manifest is the right idea and the only one that runs. Its
problems are structural: the manifests live in the ServiceBay codebase (a
duplicated fork in the backup worker), an undeclared service is skipped with a
warning that nobody reads, secrets had to be stripped from archives because
configs carry them in clear text, and restore is a special code path that runs
before a pod starts.

## Laib

One tool, one declaration per service, one key.

- **Tool: restic.** Encrypted, deduplicated, incremental, with snapshots and
  retention. Target from `box.yml backup.repo` (the Fritz!Box NAS over SFTP or
  SMB mount, a second disk, or both as two repos). The restic password is a
  secret in `secrets.age` (`RESTIC_PASSWORD`).
- **Declaration in the service, not in the kernel.** `laib.yml data[]` lists
  the service's paths; `backup: false` marks bulk data that is skipped. A
  service with no `data` entry fails `laib template lint` and `laib plan`:
  silence is not an opt-out. An empty list is written as `data: []` with a
  comment saying why.
- **What is in every snapshot:** the state repo (which includes
  `secrets.age`), the journal, and every declared path. Not the rendered
  files; they are reproduced by `laib apply`.
- **No strip rules.** The repo is encrypted; a config file that carries a
  secret on disk is backed up as it is. The `age` key is not in the backup
  and lives on paper (ADR 0006), so a stolen repo yields nothing.
- **Schedule:** a systemd user timer rendered by `laib apply` from
  `box.yml backup.schedule`; the run is `laib backup`, which journals result,
  size and duration. A failed or partial run is a red journal line and a red
  line in `laib status`, not an email.
- **Restore is two verbs.** `laib restore <when> <service>` replaces the
  declared paths of one service (`--delete`: what the snapshot does not have
  goes; a snapshot of what is there is taken first), then applies it.
  `laib restore --box <when>` on a fresh box (#40) restores the state repo
  and the journal first, then every service in dependency order with its
  units stopped and its paths replaced, then one apply — all under the one
  lock, each service paused for converge meanwhile.
- **The recovery sheet.** `laib recovery-sheet` prints one page: the age
  key, the restic repo, the restic password and the four steps. The key and
  the password both live on the data disk and inside the encrypted repo, so
  without that page an off-box repo is useless. `laib init` and the stick
  say so; `laib diag` stays yellow until `laib recovery-sheet --confirm`.
  Humans at the console only, never over MCP.
- **The password is never invented twice.** `backupPrep` generates
  `RESTIC_PASSWORD` only when the repo does not exist yet; an existing repo
  without the password in `secrets.age` asks for the sheet's
  (`RESTIC_PASSWORD=… laib restore --box latest`), which then lands in
  `secrets.age`.
- **Databases are not copied live.** A declared path with `quiesce: true`
  (immich and paperless `pgdata`, vaultwarden `data`, Home Assistant's
  config) has its service stopped for the snapshot: stop, snapshot, start,
  under the lock and paused for converge. A nightly minute of downtime is
  the price of a database that opens after a restore.
- **The restore test.** `laib backup check` runs monthly from a timer:
  `restic check --read-data-subset=5%`, then one service's path restored
  into a scratch dir and compared. A green journal line is the proof; diag
  is yellow after 35 days without one, and red a week after install while
  the only repo sits on the data disk.
- **Reinstall is the ordinary path.** Install Laib from the stick, put the
  `age` key in place, `RESTIC_PASSWORD=… laib restore --box latest`. There
  is no special auto-restore code path; apply reads the restored state like
  any other.
- **Bulk media** (ServiceBay ADR 0002's "large drive") is not a kernel
  concern: a `mirror` template with an rsync timer is a service like any other,
  with its own `data: []`.

## What changes for the operator

| Today | Laib |
|---|---|
| three settings pages, three statuses | one line in `laib status`, one journal entry per run |
| undeclared services skipped quietly | undeclared services do not pass plan |
| manifests in the control plane's code | `data[]` next to the service |
| strip rules for secrets in configs | encrypted repo, key on paper |
| restore before the pod starts, special path | `laib restore`, then the ordinary apply |
| SMB credentials in config.json | restic repo URL in `box.yml`, password in `secrets.age` |

Issue #4 implements this.
