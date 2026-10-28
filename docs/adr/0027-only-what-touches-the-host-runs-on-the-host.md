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

## Implementation notes (#624: the Operator and notify)

How the first two moves were made, where the decision left room:

- **Pinned by the release, not by the template path.** The release builds
  both images from its tag and builds `laib` with their digests
  (`internal/render.OperatorImage`, `NotifyImage`). They speak the MCP of
  the laib that renders them and the Operator's skills come from it
  (ADR 0023), so they must not drift from it: the self-update brings the
  new images with the new laib, and `laib self rollback` converges with
  the previous laib, which renders its own images back. `laib updates`
  does not list them; a build from source renders `:latest`. The release
  carries no `laib-operator` or `laib-notify` binary any more; one an
  older release installed stays in `/var/opt/laib` for a rollback to it.
- **The host's network.** Laib, the model, Frage, usage-metrics and
  bote-notify answer on host ports, laib serve on 127.0.0.1 unless the
  box opens it, and laib posts to the Operator on 127.0.0.1:5901. The line
  this ADR draws is the disk and the identity, not the ports: with
  `Network=host` each program reaches what its host unit reached, and no
  more of the box's files.
- **The folders.** `/mnt/data/laib/operator` is mounted at `/operator`,
  `/mnt/data/laib/notify` at `/notify`; the unit makes the folder on the
  host before it starts. laib-notify's `mail.json` and `watchdog.last`
  lay in `/mnt/data/laib` itself: its first start moves them into
  `notify/`, and `/ready` reads the stamp there.
- **The proof.** e2e checks the rendered unit (one mount, no source above
  the age key, `secrets.age`, the state repo or the journal, no env file);
  the boot job runs the Operator from this commit's image and the unit
  laib renders and shows the key unreadable inside while its folder is
  there. On the box it is still to be shown, with the box's own paths.

## Implementation notes (#624, part 2: haushalt and lease)

How the last two moves were made:

- **laib-haushalt: a container on every box, the door over HTTP.** Laib
  renders `quadlet/laib-haushalt/laib-haushalt.container` like the
  Operator's (`render.HaushaltImage`, pinned by the release): the host's
  network, so `net geraete` reads the box's neighbour table; HTTP on
  127.0.0.1:5902. The door (`cmd/laib/door.go`) posts each call to
  `/call` with the door's key (`LAIB_HAUSHALT_KEY`, generated into
  `secrets.age`, a podman secret for the container), the caller's actor,
  the verb's ceiling and what the verbs read of the box: the LAN address,
  the scan service's name, its status file's content and its orders
  folder. The program reads none of laib's files. What it would journal
  comes back with the answer and the kernel writes it as the caller.
- **The scan status comes with the call, not as a mount.** The issue
  asked for the status file read-only; scan writes it by rename, and a
  file bind mount keeps the old inode, so the container would read a
  status that never changes. Mounting its folder would hand over the
  stacks' archive. So the door reads the file as `laib status` does and
  hands its content over; the one mount is `auftrag/`, at the same path,
  `:z` (shared with the scan container), only while scan is installed.
- **The way back is propose only, enforced.** `scan einrichten` calls
  `laib_propose` over MCP with `LAIB_HAUSHALT_TOKEN`, minted on apply for
  the new `haushalt` hand (`ceiling: propose, verbs: [propose]`). Laib
  holds that hand to ceiling propose whatever `X-Laib-Ceiling` says
  (`mcp.DoorHands`): its proposals are never the dial's, and the header
  can only lower the ceiling (net geraete sends read). The proposal's
  actor is `haushalt`; the journal line names the caller. A verb served
  behind the door takes no MCP lock — its call back takes it, which
  would otherwise wait for the call it is part of.
- **Amendment (#883, owner 2026-10-28): the hand adds one kind of
  Box-Aufgabe.** The Bote's `laib wish` is a task for the admins, not a
  ticket, and only the kernel writes `aufgaben.yml`; so the `haushalt` hand
  also has `aufgabe` (fixed in code, `state.HaushaltHand`). The kernel lets
  it add, never tick off or defer, and records as `von` the caller
  laib-haushalt names (`--von bote:<login>`), the way Haus's `--von` is.
- **The oui list is in the image.** Distroless has none; the image takes
  Alpine's `hwdata-net` (`/usr/share/hwdata/oui.txt`), so makers stay
  named without a mount of the host's file.
- **laib-lease stays the llama template's unit, as a container.**
  `llama-lease.container` names `ghcr.io/mdopp/laib-lease:latest`; laib
  replaces that line with `render.LeaseImage`, pinned by the release, so
  the lease moves with laib. Mounts: the lease folder (`:z`, Stimme's
  units read `voice-device.env` from it) and `presets.json` read-only
  (`%h/.config/containers/systemd/llama/`; a changed presets.json
  restarts the llama units, so the file mount never goes stale). The
  model key and `LLAMA_LAIB_TOKEN` (the template's secret, `mint: lease`)
  are podman secrets.
- **Pausing through laib.** The lease finds a role's units in `laib
  status` (its rows carry `provides` now) and holds a service with `laib
  pause <service> --until … --stop <unit> --restart <unit> "<grund>"`
  over MCP as the `lease` hand (`ceiling: apply, verbs: [status, pause,
  resume, note]`). `pause` gained `--stop` and `--restart` for the
  service's own units; the marker keeps them and `resume` starts and
  restarts them again. A renewal names no unit and touches none.
  Of what looked host-bound: writing `voice-device.env` is the lease's own
  folder, no host needed; stopping Whisper is `--stop`; restarting the
  voice units on a new device is `--restart`, after the lease wrote the
  file. The long-window warning is `laib note --kind watch`.
- **Without laib serve.** The lease starts anyway, serves the household,
  writes "Laib antwortet nicht" into its status file and refuses a
  window that would stop units (the card is never taken from services
  that keep running); it asks laib again at the next window. A renewal
  that cannot reach laib leaves the marker to end at its renewal time.
- **The migration.** Every box gets laib-haushalt's container with the
  first apply after the self-update (the token is minted, the key
  generated). The llama template's change reaches a box as a template
  update: its apply stops and disables the host `llama-lease.service`
  before the reload (a template update now drops a `.service` or `.timer`
  the template no longer ships) and starts the container under the same
  unit name. The update mints a token for the `lease` hand (apply), so
  laib's nightly sync (the `laib` hand, propose) may not file it (#34):
  the owner runs `laib template update llama`, a human-class proposal,
  and approves it — one apply. Until then the host lease keeps running
  the binary an older release installed.
- **Release and rollback.** The release builds and attests
  `ghcr.io/mdopp/laib-haushalt` and `ghcr.io/mdopp/laib-lease` and pins
  their digests into laib; it carries no `laib-lease` or `laib-haushalt`
  binary. `laib self rollback` swaps laib (and laib-disk) and converges
  with the previous laib, which renders its own images. A laib from
  before this change renders the template's `:latest` for the lease and
  runs the old `laib-haushalt` binary behind its door; its policy has no
  `lease` hand, so a lease then cannot pause anything and says so.
- **The proof.** e2e checks laib-haushalt's rendered unit (no mount
  without scan) and the lease's (a scratch GPU box with the llama
  template, through `laib plan`): exactly their mounts, none above the age
  key, `secrets.age`, the state repo or the journal, the tokens as podman
  secrets; the door's checks run against the real program with the key
  and token the apply handed podman. The boot job runs laib-haushalt from
  this commit's image and the unit laib renders: `laib scan` reaches it, a
  call without the key gets 401, and the key, `secrets.age`, the state
  repo and the journal are not readable inside. The lease's container on
  the GPU box, and both on the box with its own paths, are still to be
  shown.

## Note (#677): the lease outside the kernel's tree

The lease's code moved from `internal/lease` and `cmd/laib-lease` to its
own program `lease/` (`lease/cmd/laib-lease`, `lease/Containerfile`), with
its own budget in `scripts/loc.sh` (ADR 0005, 2026-10-26). Nothing about
its container changes: the image is still `ghcr.io/mdopp/laib-lease`, the
release still pins its digest into laib (`render.LeaseImage`), and it still
reaches laib only over MCP as the `lease` hand — now with its own client,
since it imports nothing of the kernel. A rollback to a release from
before runs that release's pinned lease image, built from the old path.
The same train moved `zuhause` and `person_signal` behind the verb door:
laib-haushalt proposes their box.yml paths as the `haushalt` hand, whose
built-in grant now names `zuhause` and `people.*.signal`.

