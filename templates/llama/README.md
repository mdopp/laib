# llama — the model role (ADR 0011)

**Ported, not yet verified on the GPU box.** The policy proxy and the lease
are `cmd/laib-lease` (Go, tested with a fake router: household, mode
follows the request, a named holder, 409 with the holder named, the
catalogue marked, exclusive, release by holder). The post-deploy hook
fetches every preset's weights idempotently and writes the router's
`presets.ini` from `presets.json`. The broker and the expiry unit of the
old script are gone: the sidecar is a long-running process and expires
leases itself. What the GPU box must still confirm (issue #18): llama-server's
router flags (`--models-preset`), speculative decoding active, the voice
units reading `voice-device.env`, and the 1.3 s turn.

## What goes where

| solarisbay today | Laib |
|---|---|
| Pod YAML with `hostNetwork: true`, `nvidia.com/gpu` limit, GPU Quadlet fallback written by the hook | `llama.container` with `AddDevice=nvidia.com/gpu=all`; `hostNetwork: true` in laib.yml and the carve-out in `box.yml` |
| `post-deploy.py download_model / ensure_preset_weights` | `post-deploy` hook: idempotent download of the preset files into `${DATA_DIR}/llama/models` |
| `write_presets`, `render_presets` | `presets.yml` next to the units, rendered by the hook into the router's models directory |
| `proxy_run`, `make_proxy_server`, `allowed_presets`, `canonical_mode` | `laib-lease` run by `llama-lease.service`: the policy proxy on `PORT` |
| `lease_acquire / lease_release / schedule_expiry`, the broker units and `gpu_lease_request.json` | the same `laib-lease`: `POST/GET/DELETE /api/model-lease` with Laib's lease contract (ADR 0011, amendment 2026-10-14); no broker, consumers speak HTTP |
| `install_embed_unit` | `llama-embed.container` |
| `register_http_check` against ServiceBay's API | `health:` in laib.yml |
| `set_voice_device` (voice stack to CPU in heavy modes) | the lease itself: a window with `voice: cpu` writes `voice-device.env` and restarts the voice units |

Consumers: the Werkstatt's pi and
foundry-chronicle. All address `127.0.0.1:<PORT>` from host services and
`host.containers.internal:<PORT>` from isolated pods (ServiceBay ADR 0007).

## The lease (#42)

- **Modes name roles and containers, not unit names.** `presets.json`
  `modes.<name>.stops` and `voiceUnits` are `role/container` refs
  (`voice/whisper`, `model/embed`); the sidecar resolves them through
  laib's state to `stimme-whisper.service`, `llama-embed.service` at
  start and refuses to start on a ref nothing provides, naming the
  service and the units it has. A renamed template fails loudly, not by
  leaving a unit on the GPU.
- **The API wants the model key.** `POST`/`DELETE /api/model-lease` take
  `Authorization: Bearer <MODEL_KEY>` — the same key consumers already
  send the proxy; 401 without it. On this box the key is the secret
  `MODEL_API_KEY` the template declares (generated on the first apply,
  builtin `${MODEL_KEY}`); a peer box sets the same value by hand
  (`laib secret set MODEL_API_KEY`, `box.yml model.keySecret`). `GET` and
  `/health` stay open. The proxy's `/v1` stays reachable from the LAN
  (operator decision, solarisbay #1420).
- **Narrowest mode.** Acquiring by preset takes the narrowest mode that
  allows it (`gemma-4-12b` → `shared`, not `full`). Consumers ask by
  preset; the windows are named for what they do to the card (ADR 0011,
  amendment 2026-10-14).
- **Renew or lose the card.** The answer carries `renew_after` (a third
  of the ttl, at most half of `LEASE_RENEW`, 10 min) and `expires_at` (the
  ttl asked for). A holder that misses two renewals is released, not after
  four hours. The automatic window keeps its idle timeout and
  is renewed only by requests that need it — a household request never
  keeps a foreign window alive.
- **Starvation is visible.** Every service whose units the lease stops
  carries the pause marker (`laib status`, `/ready`, the Box page) with
  the reason in German: „Grafikkarte reserviert für bote („qwen3.6-35b-a3b“):
  Sprache läuft langsamer, bis 15:30“. A window older than
  `LEASE_WARN_AFTER` (20 min) gets one yellow journal line.
- **One port.** The lease answers on the proxy's port (`PORT`, 11435):
  `http://<gpu-box>:11435/api/model-lease`.
