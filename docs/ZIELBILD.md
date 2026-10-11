# Laib — the target picture and the way there

2026-10-10. The concept is `docs/KONZEPT.md`; this is the end state in
one picture and the ordered path, with the NUC as the trial box.

## The end state

**One box: the Kraftwerk** (the GPU box that runs ServiceBay today),
running everything in `docs/KONZEPT.md`: Laib with all roles including
the model, the Operator, the Bote, Haus, Frage, Home Assistant with
Stimme, the household services, the Werkstatt. One state repo, one
restic repo on the NAS, one Laib.

The Intel NUC is a **proving ground only**: it has no GPU, so it uses the
Kraftwerk's model proxy over the LAN (`box.yml model.url`), and it is
wiped when the trial is over. Nothing stays on it. (Two boxes with fixed
roles would work — ADR 0018 keeps the design as a parked option — but it
is not the plan.)

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

### Phase 4 — the Kraftwerk gets Laib (docs/MIGRATION.md)

10. Laib installed on the GPU box beside ServiceBay with `--gpu`;
    ServiceBay's units ignored; Caddy beside NPM (step 2 of MIGRATION.md),
    then auth, adguard, llama as the model role (issue #18).
11. The household services adopted one by one from ServiceBay: Radicale,
    Paperless, Vaultwarden, file share, Home Assistant, media, Immich,
    foundry-chronicle; each `laib plan`, `laib apply`, verify, old unit
    off. Stimme with the GPU variants (Martin TTS).
12. Operator, Bote, Frage and Haus move from the NUC to the Kraftwerk:
    the same templates, `model.url` replaced by the local role, Haus's
    data restored from the NUC's restic repo.

Gate: ServiceBay disabled; `laib status` green; the Bote reports from the
Kraftwerk; the NUC is wiped.

### Phase 5 — voice and retire

13. Assist sentences for timers and media (check 1 of ADR 0015), Music
    Assistant with Jellyfin and radio, the wake word trained; Solaris the
    agent off; its memory library in Haus; solarisbay reduced to templates
    in the household registry; solaris-android renamed.
14. ServiceBay's data dir kept 30 days, then deleted. This page rewritten
    as "what we have".

## What the trial proves, and what it does not

Proves: the kernel on real hardware, DNS-01 and SSO, the Operator with a
remote model, the Bote on Signal, Haus against a real family — all
without touching the GPU box. Does not prove: the GPU role, the lease,
media transcoding — those are Phase 4 on the Kraftwerk itself, which is
why that box is migrated last and nothing on it changes before.

## Stand 2026-10-11

Built and CI-proven without a box: the stick (`laib-stick`), the first
install end to end in a KVM guest, the data disk as a one-member RAID1
with `laib disk add` and a reinstall that keeps the data, Caddy with its
own CA, backup and restore against a real restic repo, the Bote's config
with the `haus` and `automat` tools, Haus as its own repo with the Box
page, pairing and the companion API. Phase 1 (the NUC) is next and is the
first real run; nothing on the Kraftwerk has changed.
