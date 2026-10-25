# llama — the model role (ADR 0011)

**Ported, not yet verified on the GPU box.** The policy proxy and the lease
are `cmd/laib-lease`, in its container (ADR 0027; Go, tested with a fake router: household only,
a foreign preset refused without a lease, a named holder, 409 with the holder named, the
catalogue marked, exclusive, release by holder). The pre-start hook
writes the router's `presets.ini` from `presets.json` before every start
(the router reads it only then, #512); the post-deploy hook fetches every
preset's weights idempotently. The broker and the expiry unit of the
old script are gone: the sidecar is a long-running process and expires
leases itself. What the GPU box must still confirm (issue #18): llama-server's
router flags (`--models-preset`), speculative decoding active, the voice
units reading `voice-device.env`, and the 1.3 s turn.

## What goes where

| solarisbay today | Laib |
|---|---|
| Pod YAML with `hostNetwork: true`, `nvidia.com/gpu` limit, GPU Quadlet fallback written by the hook | `llama.container` with `AddDevice=nvidia.com/gpu=all`; `hostNetwork: true` in laib.yml and the carve-out in `box.yml` |
| `post-deploy.py download_model / ensure_preset_weights` | `post-deploy` hook: idempotent download of the preset files into `${DATA_DIR}/llama/models` |
| `write_presets`, `render_presets` | `presets.json` next to the units, rendered by the `pre-start` hook into the router's models directory before the router starts (#512) |
| `proxy_run`, `make_proxy_server`, `allowed_presets`, `canonical_mode` | `laib-lease` in `llama-lease.container` (ADR 0027): the policy proxy on `PORT` |
| `lease_acquire / lease_release / schedule_expiry`, the broker units and `gpu_lease_request.json` | the same `laib-lease`: `POST/GET/DELETE /api/model-lease` with Laib's lease contract (ADR 0011, amendment 2026-10-14); no broker, consumers speak HTTP |
| `install_embed_unit` | `llama-embed.container` |
| `register_http_check` against ServiceBay's API | `health:` in laib.yml |
| `set_voice_device` (voice stack to CPU in heavy modes) | the lease itself: a window with `voice: cpu` writes `voice-device.env` in its folder, and laib's `pause --restart` restarts the voice units |

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
  four hours.
- **No window by request (#522).** Without a lease the proxy answers the
  household model only; a request for another preset is a 409 that names
  the household model and the lease API. Before, such a request took
  `full` itself as holder `router`: one call from Solaris for Gemma
  evicted Qwen (`--models-max 1`), stopped Whisper and put the voice on
  the CPU for 45 minutes, and nothing said so. A preset outside the
  household is a named holder's lease, which it renews and gives back. A
  `router` window left in `lease.json` is given back when laib-lease starts.
  Redirecting to the household model was the alternative; it was not
  taken because the caller would get an answer from a model it did not
  ask for and never learn that its configuration is wrong.
- **A standing lease is yellow.** While any window other than the
  household's stands, `lease/laib-status.json` carries a yellow finding
  with the holder, the window, what it stopped and when it ends, so
  `laib status` and `laib diag` say why Qwen has to reload.
- **Starvation is visible.** Every service whose units the lease stops
  carries the pause marker (`laib status`, `/ready`, the Box page) with
  the reason in German: „Grafikkarte reserviert für bote („qwen3.6-35b-a3b“):
  Sprache läuft langsamer, bis 15:30“. A window older than
  `LEASE_WARN_AFTER` (20 min) gets one line in laib's journal (`laib note`).
- **In its container, through laib (ADR 0027).** `llama-lease.container`
  runs `ghcr.io/mdopp/laib-lease` — the template names `:latest`, laib puts
  in the digest its release built — with the lease folder and
  `presets.json` read-only as its only mounts and the model key and
  `LLAMA_LAIB_TOKEN` (minted for the `lease` hand: status, pause, resume,
  note) as podman secrets. It stops no unit: it finds a role's units in
  `laib status` and asks `laib pause <service> --until … --stop <unit>
  --restart <unit> "<grund>"` over MCP; laib writes the marker and acts,
  `laib resume` brings back what the pause held. While laib serve does not
  answer the proxy serves the household, the status file says the lease
  cannot pause anything, and a window that would stop units is refused.
- **The presets, nothing else.** `/v1/models` through the proxy lists the
  presets, the household's first, each marked `allowed_in_mode`; the router
  also lists every GGUF file in its models dir, and those are left out. A
  request for a raw file id is a 404 (`model_not_found`) naming the
  presets (#395).
- **A pinned llama.cpp (#423).** `IMAGE` is a build tag with its digest
  (`server-cuda-bNNNNN@sha256:…`), and the units have no `AutoUpdate`:
  llama.cpp renames flags between builds (`--draft-max` became
  `--spec-draft-n-max`), and a floating `server-cuda` took every drafter
  preset down after an image update. A newer build is a proposal: set the
  tag and digest, apply, and check that every preset loads.
- **No thinking by default (#501).** Every preset renders `reasoning = off`
  into `presets.ini` (llama.cpp's `--reasoning off`: the server's template
  default becomes `enable_thinking=false`), so Frage, the Operator, pi and
  Haus answer without a thinking pass none of them asked for. A preset
  opts in with `"thinking": true` in `presets.json`. A request that wants
  thinking says so: `chat_template_kwargs: {enable_thinking: true}`
  overrides the server default per request (the Bote's `/model denken`).
- **One port.** The lease answers on the proxy's port (`PORT`, 11435):
  `http://<gpu-box>:11435/api/model-lease`.
- **A missing role degrades, it does not stop the lease (#374).** A mode
  that stops a role nobody provides (`voice/whisper` before Stimme is on
  the box) leaves it out; laib-lease names each one in its status file
  (`lease/laib-status.json`, gelb in `laib status` and diag). A container
  the providing service does not have is still refused at start.

## Prompt eval: the knobs and how to measure them (#525)

Qwen 3.6 35B-A3B read prompts at ~270–410 t/s on the Kraftwerk's 16 GB
card; the Bote's first turn is ~14,500 tokens. Each preset in
`presets.json` may set, rendered into `presets.ini` by pre-start (an
unset key keeps llama.cpp's default):

| Key | presets.ini | What it changes |
|---|---|---|
| `batch` | `batch-size` | logical batch per prompt step |
| `ubatch` | `ubatch-size` | physical batch: larger is faster prompt eval, costs compute buffer on the card |
| `flashAttn` | `flash-attn` | `true`/`false`/`"auto"`; a quantized V cache (`cacheV`) needs it on |
| `ngl` | `n-gpu-layers` | layers on the GPU; unset lets llama.cpp fit them to free memory |
| `nCpuMoe` | `n-cpu-moe` | MoE expert layers kept on the CPU; `0` asks for all on the GPU |
| `parallel` | `parallel` | slots; `ctx` is shared among them |

The household preset ships `batch 4096, ubatch 2048, flashAttn true`.
That is the starting point to measure, **not a measured result**: whether
it is faster, and whether the larger compute buffer still fits next to
the weights without pushing layers to the CPU, only the box can say. The
target is ≥ 1,500 t/s (~10 s for the Bote's cold start).

Measure on the box, same prompt before and after (`measure.py` is
installed next to `presets.json`):

```
python3 ~/.config/containers/systemd/llama/measure.py --label before
# change presets.json, laib apply llama (pre-start rewrites presets.ini, the router restarts)
python3 ~/.config/containers/systemd/llama/measure.py --label after
```

It sends a ~14k-token prompt three times with `cache_prompt: false` and
`max_tokens: 1` through the proxy, prints the router's own
`prompt_per_second` per run, and the last llama log lines that say where
the weights sit: `offloaded N/M layers to GPU`, the `CPU_Mapped` and
`CUDA0` buffer sizes, `n_batch`, `n_ubatch`, `flash_attn`, `n_seq_max`.
`offloaded` below M, or a large `CPU_Mapped` buffer, means layers or
experts run on the CPU; try a smaller `ubatch`, fewer `parallel` slots, a
smaller `ctx`, or `nCpuMoe` explicitly. The last line is JSON, so two
runs compare side by side. The first run after a start includes the cold
load in its wall time; `prompt_per_second` does not.
