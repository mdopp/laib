# ADR 0002 — systemd runs the containers via Quadlet; Laib is one static Go binary on the host

Applies when you are about to call `podman run`, add a supervisor, put Laib in
a container, or pick a language for anything in `cmd/` or `internal/`.

- **Status:** Accepted, 2026-10-08; amended 2026-10-21 (#528: images update through laib)

## Context

Podman Quadlet already turns `.container` and `.pod` files under
`~/.config/containers/systemd/` into systemd units with restart policies,
dependencies, timers and `podman auto-update`. ServiceBay used `.kube` units
but ran itself in a container and therefore needed an SSH agent to see the
host (ADR 0003). Fedora CoreOS ships no Node runtime.

## Decision

1. Laib never runs a container. It renders Quadlet files and runs
   `systemctl --user daemon-reload` and `restart`. `.container` + `.pod` are
   the primary format; `.kube` only for imported Kubernetes manifests.
2. Laib is one static Go binary (`CGO_ENABLED=0`) at `/var/opt/laib/laib`,
   installed via Ignition, run as `laib.service` in the `core` user's systemd.
   It reaches Podman through the user socket and systemd through D-Bus.
3. TypeScript stays allowed for the Werkstatt image and for scripts. Not for
   the kernel.

## Consequences

- The runtime layer in the kernel is a renderer of a few hundred lines.
- No container-in-container, no socket mounts for the control plane, no
  loopback-to-itself trips.
- The operator's team knows TypeScript better than Go; the kernel must stay
  small enough that this does not matter, and `docs/` must explain the
  structure rather than assume it.

## Amendment 2026-10-21 (#528): images update through laib, podman auto-update stays masked

**Context.** On the Kraftwerk nobody kept the box current:
`podman-auto-update.timer` had been masked on the migration evening, so no
`AutoUpdate=registry` container ever pulled again, `laib updates` saw version
tags only, and Home Assistant's extensions were four months old. Unmasking
podman's timer would bring back a second actor that restarts units outside
laib's lock, judges a new image only by whether its unit starts (a red
health stays red), and knows nothing of data. ServiceBay kept the same timer
masked "until an update window is configured" and moved images by a manual
force-update with a digest anchor; it had no view of Home Assistant's
extensions at all.

**Decision.** Laib owns image updates; `podman-auto-update.timer` stays
masked (`install-box.sh --box-config` masks it on every box; `laib diag` is
yellow while it runs, and while laib's replacement does not).

1. **Without data, nightly by laib.** `laib-images.timer` runs `laib updates
   --run` every night 90 minutes after `backup.schedule` (box.yml `updates:
   days, time` narrow it), skipped while the backup runs, under the one
   lock, paused services left alone. It pulls the images the nightly run
   owns — a container the template marks `AutoUpdate=registry` (the mark
   templates already used for "no data a new image would migrate",
   docs/INTERFACE.md) whose image floats in #523's sense (`apply.Floats`) —
   restarts and verifies a service whose image moved, and on red tags the
   previous image back, restarts again and journals it red.
2. **With data, by proposal.** For every other image, `laib updates` compares
   the registry's digest of the tag with the pin or the local image (a tag
   such as `stable` or `latest` too, not only semver). The Operator's weekly
   `aktualisierung` runner proposes a newer one itself, pinned by digest
   (`image:tag@sha256:…`). The review marks a new image in a container
   without the mark, and any values change of a service with a database
   (`quiesce: true`), as one a person decides (a warning, so the dial never
   approves it alone, and the chat may: it is no `human` class); approving
   it first takes a `pre-update` restic snapshot of that service (stopped
   while its database is copied), fail closed. `laib rollback` brings the
   image back, `laib restore` the data from before.
3. **Extensions and firmware are the service's to report.** Home Assistant's
   check lists its `update.*` entities: extensions as `Update: …` with their
   release notes and, the installable ones, as one `HA_INSTALL` value
   proposal the runner files as it stands (post-deploy installs it after the
   approval); device firmware as `Firmware: …`, reported only.
4. **What is behind, weekly and in diag.** laib-notify's weekly mail is "Was
   ist veraltet": images, the host image, extensions, firmware and every
   image diag sees behind. `laib diag` is yellow for an image more than 30
   days behind its channel, counted from when `laib updates` first saw it
   behind (`/mnt/data/laib/updates.json`).

**Consequences.** One update path with laib's lock, verify and rollback; the
classification stays in the templates, where the author knows the data, and
`templates46_test.go` holds it (no `AutoUpdate=registry` beside a database;
Laib's own images carry it). The kernel grew about 275 lines for it. A
data service's update needs a person's yes every time, which is the point.
The registry digest of a multi-arch tag is its index digest; that podman
keeps it among a pulled image's RepoDigests is what the box confirms.
