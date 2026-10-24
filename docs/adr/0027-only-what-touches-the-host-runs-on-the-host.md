# ADR 0027 — Only what touches the host runs on the host; the rest of Laib's programs run in containers

Applies when you add or change one of Laib's own programs (not a template's
service), when you wonder whether something may run as `core` on the host,
or when an agent-facing program needs a new right.

- **Status:** Accepted, 2026-10-24
- **Deciders:** the owner (mdopp), on "ob wirklich alles was Haus und was
  Laib macht, nativ auf dem System laufen soll, oder ob einiges davon in
  einen Container gehört" and the answer: "ALLES wie empfohlen. Baue auch
  gleich die Container".

## Context

Every template's service is a Quadlet container, and so are the hooks.
Haus runs in a container too. On the host, as the user `core`, run Laib's
own programs: `laib` (serve, converge, apply, backup, updates),
`laib-disk`, `self-update.sh`, and also `laib-operator`, `laib-notify`,
`laib-haushalt` (ADR 0026) and `laib-lease`.

`core` can read everything Laib keeps: the age key, `secrets.age`, the
state repo, the journal. The last four programs need none of it. They talk
to Laib over MCP or HTTP, and `laib-lease` also writes pause markers. The
operator unit had no sandboxing, so the Operator could in principle read
the age key. The Operator is the program that asks a model and reads the
web (ADR 0025). ADR 0026's "a program behind the door may only read and
propose" held only by agreement.

## Decision

1. **Host only for what must touch the host.** These stay on the host:
   - `laib`: systemd, podman, Quadlet, snapshots;
   - `laib-disk`: block devices, mdadm, SMART;
   - `self-update.sh`: it replaces the binaries;
   - the backup, which stops services and reads their data.
2. **Everything else is a container with only its own token and folders.**
   Each one is a Quadlet unit rendered by Laib, from an image CI builds and
   publishes on ghcr, pinned by digest like the template images. None of
   them mounts `/mnt/data/laib` whole, the age key, `secrets.age` or the
   state repo.
   - **`laib-operator`** gets its MCP token (the `ops` hand), its own
     folder `operator/`, and the model and Frage over HTTP.
   - **`laib-notify`** gets its token, the bote-notify URL and the mail
     relay.
   - **`laib-haushalt`** becomes a small long-running service. The verb
     door hands a call over HTTP on 127.0.0.1 instead of running the
     program, with the caller's actor and a propose-only token. The scan
     orders folder is its only data mount. It uses host networking where
     a verb reads the LAN.
   - **`laib-lease`** pauses services through Laib's `pause` verb over MCP
     instead of calling `systemctl` itself. It keeps its lease folder and
     the presets file read-only.
3. **The order:** the Operator first, because it gains the most. Then
   notify, haushalt and lease. Each move proves that the program can no
   longer read the age key: a check in e2e, and on the box a
   `laib look`-style probe.
4. **Rights follow the move.** Once the Operator runs in its container,
   `restart` may be granted to `ops` (owner, 2026-10-24, #543). Before
   that, it may not.

## Consequences

- Four more images in CI, and four units that `laib self rollback` and the
  self-update treat like the other services, not like binaries.
- A compromised Operator, notify, haushalt or lease reaches no further than
  its token. "Propose only" is now enforced by the OS, not only agreed.
- `laib-lease` depends on `laib serve` for pausing. While `laib serve` is
  down, a lease cannot pause anything, and the lease says so.
- `scripts/loc.sh` keeps one budget per program. Moving to a container
  changes where a program runs, not what it counts toward.
