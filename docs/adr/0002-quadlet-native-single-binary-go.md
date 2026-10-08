# ADR 0002 — systemd runs the containers via Quadlet; Laib is one static Go binary on the host

Applies when you are about to call `podman run`, add a supervisor, put Laib in
a container, or pick a language for anything in `cmd/` or `internal/`.

- **Status:** Accepted, 2026-10-08

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
