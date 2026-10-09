# ADR 0011 — The model server is a Laib platform role, not part of Solaris

Applies when you touch the llama template, the GPU lease, the policy proxy,
or wonder which project a model-serving piece belongs to.

- **Status:** Accepted, 2026-10-09
- **Deciders:** operator (mdopp): "Hauptsache, dass es zu Laib wandert."

## Context

llama-server (router on 11434, policy proxy on 11435, embeddings on 11436)
and the GPU lease (`model-lease` on Solaris's 8787, the lease script under
`${DATA_DIR}/solarisbay/`) live in the solarisbay repo. The GPU already has
three consumers: Solaris for chat and voice, the Werkstatt for pi in the
`coding` mode, foundry-chronicle for evening runs. Keeping the server in
solarisbay makes Laib's Werkstatt depend on the household assistant for a
model, and splits the proxy (reads `allowed`) from the lease (writes it)
across two owners. solarisbay's own rule: generic capabilities belong in the
platform.

## Decision

1. `llama` is a built-in Laib template with the role `model` in `box.yml`
   (`roles.model`), beside proxy, sso, identity and dns. `box.yml gpu.present`
   must be true for the role.
2. The template carries llama-server, the router, the policy proxy, the
   embeddings server **and the lease endpoint** as one sidecar, with the
   same HTTP contract as today (`POST/GET/DELETE /api/model-lease`, 200/202/
   409, `holder`, `allowed`, `mode`), so foundry-chronicle and Solaris change
   an address, nothing else.
3. Solaris keeps the judgement about *when* to ask for a mode (calendar,
   voice priority) and becomes a pure client of the lease. Its
   `model_lease` module loses the server half.
4. GGUF files under `${DATA_DIR}/llama/models` are downloads: declared as
   `data:` with `backup: false` and a reason.

## Consequences

- One owner for GPU, server, proxy and lease. `laib plan` can refuse a
  second `gpu: true` service while the role holds the card.
- The move is independent of the Solaris migration step and happens right
  after the auth and dns roles, because Werkstatt and Solaris both wait for it.
- solarisbay shrinks by the template, the lease script and the proxy service.
