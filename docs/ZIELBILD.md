# Laib — the target picture and the way there

2026-10-10. The concept is `docs/KONZEPT.md`; this is the end state in
one picture and the ordered path, including the NUC trial and two boxes.

## The end state

Two boxes with fixed roles, one household (ADR 0018):

| | **Alltag** (Intel NUC, no GPU, always on, low power) | **Kraftwerk** (the GPU box, may sleep) |
|---|---|---|
| Laib | its own Laib, state repo, Operator | its own Laib, state repo, Operator |
| Roles | proxy (Caddy, public 443), sso (Authelia + LLDAP), dns (AdGuard), identity | model (llama router, proxy, lease, embeddings) |
| Household | Haus, Bote (Hermes), Frage, Radicale, Paperless, Vaultwarden, file share, Home Assistant, Stimme (CPU: Whisper small, Piper) | Werkstatt, media (Jellyfin with transcoding), Immich (ML), foundry-chronicle, Martin TTS (GPU) |
| Model | `box.yml model.url` → Kraftwerk's proxy; cloud fallback when it sleeps | the role itself |
| Backup | restic to the NAS, own repo | restic to the NAS, own repo |

What they exchange: the route list (`host → ip:port`) so the DNS box
renders rewrites per host and the proxy box forwards public hosts to the
peer, and the model URL. Nothing else. No scheduler, no shared storage,
no failover: a sleeping Kraftwerk means the Bote says so and uses the
cloud model; media waits.

Everything a person touches: voice at home (HA Assist + Stimme + Frage),
Haus pages and widgets, Signal with the Bote, the Box page and the
Werkstatt for the admin. Everything an agent touches: the Laib MCP of
each box, the Haushalt MCP of Haus, Home Assistant.

## The way there

The NUC is the proving ground: nothing on the GPU box changes until the
NUC runs the whole household side against the GPU box's model.

### Phase 1 — Laib on the NUC (no GPU, remote model)

1. Fedora CoreOS on the NUC, `scripts/install-box.sh --domain <trial domain> --lan-ip <nuc> --model-url http://<gpu-box>:11435/v1 --model-lease http://<gpu-box>:8787/api/model-lease`.
2. `laib propose --add caddy`, `--add authelia` (+ lldap), `--add adguard`, `--add whoami`; approve; `laib status` green; `laib.<domain>` behind Authelia.
3. The two ADR 0015 checks are not needed here yet; this phase proves the kernel, Caddy with DNS-01, SSO, converge, backup to the NAS.

Gate: a week of `laib converge` and `laib diag --journal` with nothing
red that a human had to touch.

### Phase 2 — the agents on the NUC

4. Operator (`cmd/laib-operator`, issue #20) with `MODEL_URL` from
   box.yml; break whoami on purpose, watch it inspect, restart, journal.
5. Bote: Hermes as stock (ADR 0016), Signal gateway, Laib MCP as `bote`,
   HA integration pointed at the GPU box's Home Assistant for now, Frage
   as a tool; approval by verified number.
6. Frage: SearXNG + summary; register as Assist's conversation agent on
   the GPU box's HA (check 2 of ADR 0015).

Gate: "was gibt's Neues" on Signal answers with the morning report; "gib
#n frei" applies a proposal.

### Phase 3 — Haus on the NUC

7. Lift and shift (DEKONSTRUKTION step 3): `/p/*`, `/napi`, pairing,
   push, favorites, energy, HA aggregation into `mdopp/haus`; the Android
   app pointed at the NUC's Haus; widgets work.
8. The Haushalt MCP source (ADR 0017): calendar and contacts from
   Radicale on the NUC, notes from the file share, documents from
   Paperless, memory from the library.
9. Pages one by one: Start, Räume, Energie, Medien, Notizen, Kalender, Box.

Gate: the family uses Haus and the widgets for a week without opening the
old Solaris PWA.

### Phase 4 — the household moves to the NUC

10. Radicale, Paperless, Vaultwarden, file share, Home Assistant, Stimme
    (CPU variants) move from the GPU box to the NUC: one service at a
    time, restic restore, `laib apply`, verify, old unit off.
11. Voice: Assist sentences for timers and media (check 1 of ADR 0015),
    Music Assistant with Jellyfin and radio; wake word trained.

Gate: voice and the five intents work from the NUC with the GPU box's TTS.

### Phase 5 — the GPU box becomes the Kraftwerk

12. Laib on the GPU box beside ServiceBay (MIGRATION.md): llama as the
    model role (issue #18), Werkstatt, media, Immich, foundry-chronicle
    adopted one by one; ServiceBay's units ignored, then removed.
13. Peers: `box.yml peers` on both, routes exchanged (issue #22); DNS on
    the NUC rewrites media.<domain> to the GPU box; public 443 stays on
    the NUC and forwards.

Gate: ServiceBay disabled; `laib status` green on both; the Bote reports
both boxes.

### Phase 6 — retire

14. Solaris the agent off; its memory library in Haus; solarisbay reduced
    to templates in the household registry; solaris-android renamed.
15. ServiceBay's data dir kept 30 days, then deleted. This page rewritten
    as "what we have".

## What the trial proves, and what it does not

Proves: the kernel on real hardware, DNS-01 and SSO, the Operator with a
remote model, the Bote on Signal, Haus against a real family. Does not
prove: the GPU role, the lease, media transcoding — those stay on the GPU
box until Phase 5 and are the reason the GPU box is migrated last.
