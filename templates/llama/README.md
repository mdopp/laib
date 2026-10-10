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
| `proxy_run`, `make_proxy_server`, `allowed_presets`, `canonical_mode` | `lease.py` served by `llama-lease.container`: the policy proxy on `PORT` |
| `lease_acquire / lease_release / schedule_expiry`, the broker units and `gpu_lease_request.json` | the same `lease.py`: `POST/GET/DELETE /api/model-lease` with the foundry-chronicle #321 contract; no broker, consumers speak HTTP |
| `install_embed_unit` | `llama-embed.container` |
| `register_http_check` against ServiceBay's API | `health:` in laib.yml |
| `set_voice_device` (voice stack to CPU in heavy modes) | stays a Solaris client decision, triggered by the lease's `mode` field |

Consumers: Solaris (`model_lease` becomes a client), the Werkstatt's pi, and
foundry-chronicle. All address `127.0.0.1:<PORT>` from host services and
`host.containers.internal:<PORT>` from isolated pods (ServiceBay ADR 0007).
