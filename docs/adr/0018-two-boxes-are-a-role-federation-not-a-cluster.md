# ADR 0018 — Two boxes are a role federation, not a cluster

Applies when a second box appears, when a service should "move", when
someone proposes shared storage, an overlay network or a scheduler, and
when a box has no GPU.

- **Status:** Parked, 2026-10-10 — the operator asked the question and
  decided for one box (the Kraftwerk); the NUC is a trial only. This
  record keeps the design in case a second box ever becomes the plan.
  What *is* in use from it: `box.yml model.url` for a box without a GPU.

## Context

Kubernetes shares work across nodes by bringing etcd, an overlay network
and distributed storage. A household needs "which box is responsible for
what" and "say so when something is missing", not rescheduling.

## Decision

1. **One Laib and one state repo per box.** A service belongs to exactly
   one box. Moving is remove here, add there, restic restore.
2. **Roles may be remote.** `box.yml peers` names the other boxes; a
   role not played locally is referenced by peer (`roles.sso: alltag`),
   and the renderer uses the peer's address (forward_auth target, model
   URL). `box.yml model.url` covers a box without a GPU, peer or cloud.
3. **The only exchange is the route list.** Each Laib serves `GET /routes`
   (`host → ip:port`); the DNS box renders rewrites per host; the proxy
   box forwards public hosts to the peer. Nothing else crosses boxes.
4. **No failover, no scheduler, no shared storage.** A missing peer is a
   red in the journal and a sentence from the Bote, not a migration.
5. **Roles by nature:** the always-on, low-power box carries proxy, sso,
   dns and the household; the GPU box carries model, Werkstatt and media,
   and may sleep.

## Consequences

- `box.yml` gains `peers` and `model`; builtins `MODEL_URL`, `MODEL_KEY`,
  `MODEL_LEASE` (done); route exchange and remote roles are issue #22.
- Stimme templates choose CPU variants when `gpu.present` is false.
- The NUC trial is the first federation member; the GPU box becomes the
  second when ServiceBay is retired (`docs/ZIELBILD.md`).
