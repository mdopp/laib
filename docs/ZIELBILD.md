# Laib — the target picture and the way there

2026-10-10, phases reshaped 2026-10-13 after ADR 0019 (calm before
autonomy), ADR 0020 (personas) and ADR 0021 (names). The concept is
`docs/KONZEPT.md`; this is the end state in one picture and the ordered
path, with the NUC as the trial box.

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
NUC runs the whole household side against the GPU box's model. The one
exception is the trial's certificates. The house's port 80 reaches the
Kraftwerk, so its NPM gets one HTTP-only proxy host per NUC name, removed
when the trial ends (ADR 0007, amendment 2026-10-13).

### Phase 1 — calm: Laib on the NUC (no GPU, remote model)

ADR 0019 §1: the first phase is only what "alles läuft" depends on.

1. Fedora CoreOS on the NUC from the stick: `laib-stick --domain nuc.dopp.cloud --lan-ip <nuc> --model-url http://<gpu-box>:11435/v1`.
   - Certificates come by HTTP-01 through the Kraftwerk (ADR 0007, amendment 2026-10-13): one Strato CNAME per NUC host and one HTTP-only proxy host per name on the Kraftwerk's NPM (#30). The certificates are real, so the phones in Phase 3 need no root CA. The fallback is the deSEC delegation with `--dns-provider desec`.
   - No `--model-lease`: nothing on the NUC takes the GPU lease.
   - No GitHub token: the repo, the releases and the images are public (#94).
2. `laib add caddy`, `auth` (Authelia + LLDAP), `adguard`, `whoami`; `laib status` green; `laib.<domain>` behind Authelia.
3. Backup on the box and off it (the NAS), `laib recovery-sheet` printed, `laib family-card` on the fridge, the outside watchdog pinged (#57).
4. `laib add haus`: the start page with the three guarantees ("Alles läuft · Letzte Sicherung · Letzte Änderung — Rückgängig", ADR 0019 §3), in the house persona (Joshua unless `box.yml persona:` says otherwise).
5. The Operator as a diagnostician only: `diag`, `inspect`, `logs`, `note`, `escalate`, `propose` (ADR 0019 §1).

Gate: one month of real use, with the VM's fault injection repeated on
the hardware (reboot, power cut, reinstall from the stick, restore,
update), and nothing red that a human had to touch.

### Phase 2 — the agents on the NUC

6. The Operator's `write`, `exec` and `restart`, granted per action type
   once the eval (`werkstatt/evals`, #82) shows no wrong mutating action.
7. The Bote: Hermes as stock (ADR 0016) on Signal, in the house persona,
   with the tools `laib`, `ha` (the GPU box's Home Assistant for now),
   `frage`, `automat`, `persona` (ADR 0021); approvals only through the
   relay on the admin's verified number (ADR 0019 §2e). `chatPersonas`
   gives a number its own persona.
8. Frage: SearXNG + summary; registered as Assist's conversation agent on
   the GPU box's HA (check 2 of ADR 0015).
9. The Werkstatt and its autoloop (ADR 0019: second phase, outside the
   first gate).

Gate: "was gibt's Neues" on Signal answers with the morning report; "gib
#n frei" from the admin's number applies a proposal, and from any other
number does not.

### Phase 3 — Haus on the NUC

10. The new Haus app, a widget client (ADR 0022), paired with the NUC's
    Haus; its widgets stay current through the live stream while the
    screen is on. Haus carries nothing of the Solaris app: no bridge, no
    old names.
11. Messages to people (ADR 0022): an automation made by voice with the
    Bote's `automat` tool sends "Waschmaschine fertig" to a person's chat
    channel, through the Bote.
12. The Haushalt MCP source (ADR 0017, haus#11): calendar and contacts
    from Radicale on the NUC, notes from the file share, documents from
    Paperless.
13. The pages one by one (haus#12): Start, Räume, Energie, Medien,
    Notizen, Kalender; the Sprechen field talks to the Bote. The memory
    library moves out of Solaris into Haus (haus#13).

Gate: the family uses Haus and the widgets for a week without opening the
old Solaris PWA.

### Phase 4 — the Kraftwerk gets Laib (docs/MIGRATION.md)

14. Laib installed on the GPU box beside ServiceBay with `--gpu`;
    ServiceBay's units ignored; Caddy beside NPM (step 2 of MIGRATION.md),
    then auth, adguard, llama as the model role (issue #18).
15. The household services adopted one by one from ServiceBay: Radicale,
    Paperless, Vaultwarden, file share, Home Assistant, media, Immich,
    foundry-chronicle; each `laib plan`, `laib apply`, verify, old unit
    off. Stimme with the GPU variants; its trainer trains the house
    persona's wake word on the GPU (ADR 0020, #128).
16. Operator, Bote, Frage and Haus move from the NUC to the Kraftwerk:
    the same templates, `model.url` replaced by the local role, Haus's
    data restored from the NUC's restic repo.

Gate: ServiceBay disabled; `laib status` green; the Bote reports from the
Kraftwerk; the NUC is wiped.

### Phase 5 — voice and retire

17. Assist sentences for timers and media (check 1 of ADR 0015), Music
    Assistant with Jellyfin and radio; the Voice PE satellites flashed
    once with the persona's microWakeWord model (by hand in ESPHome,
    `templates/stimme/README.md`); Solaris the agent off; solarisbay
    archived. The Solaris app is gone; the new Haus app replaced it in
    Phase 3 (ADR 0022).
18. ServiceBay's data dir kept 30 days, then deleted. This page rewritten
    as "what we have".

## What the trial proves, and what it does not

Proves: the kernel on real hardware, real certificates (HTTP-01) and SSO, the Operator with a
remote model, the Bote on Signal, Haus against a real family, all
without touching the GPU box. Does not prove: the GPU role, the lease,
media transcoding, the wake-word training. Those are Phase 4 on the
Kraftwerk itself, which is why that box is migrated last and nothing on
it changes before.

## Stand 2026-10-13

Built and proven in CI without a box (vm-install, a KVM guest on Fedora
CoreOS, green on every release since v0.4.2):
- the stick;
- the first install of the commit under test;
- the data disk as RAID1 chosen by identity;
- a reinstall that keeps the data, and a stick left in that does nothing;
- a power cut during the first boot;
- reboot with the firewall and registry login intact;
- hooks in the hooks image (auth, adguard, radicale);
- backup and restore, and disaster recovery from the sheet alone;
- the update path;
- remove.

Unit-tested and wired, not yet on hardware:
- the trust kernel (ADR 0019 §2);
- the Bote with persona, relay, `ha`, `automat` and `frage`;
- Haus with the Box page, the guarantees, pairing, the companion keys and the persona;
- Stimme's trainer.

Phase 1 on the NUC is next and is the first real run (#23, owner actions
in #30). Phases 4 and 5 are tracked in #160. Nothing on the Kraftwerk has
changed yet.
