# ADR 0003 — No SSH remote agent: Laib runs on the host and reads podman/journald directly; one box

Applies when you want to observe the host from somewhere else, add a second
node, or stream state.

- **Status:** Accepted, 2026-10-08

## Context

ServiceBay pushed `agent.py` over SSH to the host, streamed compressed
container/unit/file state back and built a digital twin from it. The reason
was that ServiceBay lived in a container. Considerable work went into
robustness, redaction, summarisation and timeouts for that channel.

## Decision

1. Laib runs on the host (ADR 0002). Its "agent" is `podman ps`/`podman
   events` over the socket, `systemctl --user list-units`, and
   `journalctl --user -o json`. Streaming is `laib status --watch` and the
   journal.
2. Laib manages one box. A second box runs its own Laib; the two talk over
   MCP if they ever need to.
3. The lessons of the SSH channel move into `laib logs` and `laib status`:
   secret redaction, long-log summarisation, hard timeouts, robustness tests
   with dropped connections.

## Consequences

- No SSH pool, no twin, no multi-node code.
- The investment in the SSH agent is sunk; what it solved is solved by where
  Laib runs.
