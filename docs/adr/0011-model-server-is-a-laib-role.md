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

## Amendment 2026-10-11 (#42)

The lease serves on the proxy's port (11435), not a port of its own; its
`POST`/`DELETE` take the model key as bearer; modes in `presets.json` name
`role/container` refs the sidecar resolves through laib's state; a holder
renews every `renew_after` (`renew_s` until #157) or loses the card; the pause marker covers every
service whose units the lease stopped. See `templates/llama/README.md`.

## Amendment 2026-10-14 (#157): the lease contract

The owner asked for one standard contract. "Foundry ist nicht so wichtig,
dass sich Laib anpassen muss … treffe die Entscheidung für die Zukunft,
nicht für die Vergangenheit."

§2 promised "the same HTTP contract as today", meaning Solaris's, so that
consumers would only change an address. That promise is withdrawn: the
lease is Laib's own, and consumers adapt to it when they move to Laib (ADR
0010, one migration, no compatibility layer). The windows were named after
the consumers that asked for them (`foundry`, and `thinking`/`coding` as
aliases), which ADR 0021 rule 4 rules out. No old name stays.

From here on, this section is the contract. `internal/lease/contract_test.go`
pins it against the shipped `presets.json`.

**Ask for a model, not a window.** `model` is a preset alias, and the lease
takes the narrowest window that allows it. A consumer never needs to know
the window names. They are, named for what they do to the card:

| Window | What it allows | What it costs |
|---|---|---|
| `shared` | the household model and the 12B | nothing stops; the voice stack keeps the GPU |
| `full` | every preset | embeddings, Whisper and the trainer stop; the voice stack moves to the CPU |
| `exclusive` | nothing answers (asked for only by this name) | every window's units stop |

**`POST /api/model-lease`**
- Needs `Authorization: Bearer <MODEL_KEY>`.
- Body is exactly `{model, ttl_s, holder}`:
  - `holder` is the asking service's own name, `[a-z0-9-]{1,64}`, and is required. It is never a session, round or person.
  - `ttl_s` defaults to 14400 and is clamped to 300–86400.
- The same POST also renews.

| Answer | Body |
|---|---|
| 200 | `{ok: true, state: "ready", model: <window>, alias, expires_at, renew_after, allowed}` |
| 202 | `{ok: true, state: "preparing", model, alias, retry_after: 30, expires_at: null}`: the weights are loading. Poll `GET`; do not post again. |
| 409 | `{ok: false, reason: "held", holder, expires_at, mode, allowed}` |
| 400 | `{ok: false, reason}`, where `reason` is one of `invalid_json`, `invalid_payload`, `unexpected_field`, `invalid_model`, `invalid_ttl`, `invalid_holder` |
| 401 | `{ok: false, reason: "unauthorized"}` |

**`GET /api/model-lease`** needs no key and answers
`{state, model, alias, expires_at, holder, last_renewed_at, renew_after, retry_after, mode, allowed}`,
where `state` is one of `none`, `preparing`, `ready`.

**`DELETE /api/model-lease`**
- Needs the key. Body is `{holder}`; with no body it is the admin's way out of a window nobody renews.
- 200 `{ok: true, state: "released"}`. The card is free when the answer comes.
- 409 `{ok: false, reason: "held", holder, expires_at}` for someone else's window.

**Times and renewal**
- Times are Unix seconds.
- `renew_after` is a third of the ttl, and at most half of the grace (`LEASE_RENEW`, 10 min). A holder that misses two renewals loses the card.

**Consumer rules**
- Renew every `renew_after`.
- On start, `GET`; if your own holder holds a window, `DELETE` it first.
- A failed lease never stops the consumer's own work.

**Who adapts:** foundry-chronicle, pi-web and anything else speak this
when they move to Laib (Phase 4 of `docs/ZIELBILD.md`), not before.
