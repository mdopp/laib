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
- **Restore is two verbs.** `laib restore <when> [service]` restores the
  declared paths of one service (after a snapshot of what is there), then
  runs apply for it. `laib restore --box <when>` on a fresh box restores the
  state repo first, then every service, in dependency order (roles first).
- **Reinstall is the ordinary path.** Install Laib, put the `age` key and the
  restic password in place, `laib restore --box latest`, `laib apply`. There
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
