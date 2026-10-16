# What runs today, and what Laib still needs for it

Inventory of the GPU box on 2026-10-10 (read-only through ServiceBay's
MCP: 33 units, 32 active, one dead), mapped onto the Laib end state in
`docs/KONZEPT.md`. "Port" means: a template folder in the household
registry (`mdopp/laib-templates`), written from the ServiceBay template's
Pod YAML and `variables.json` with `scripts/migrate-servicebay.py` as the
draft and `podlet`/hand for the units, then `laib template lint`.

## Platform roles

| Today (unit) | Laib | Status |
|---|---|---|
| nginx (NPM, 80/443/81, 24 hosts) | `caddy`, role proxy | built-in exists; DNS-01 unverified on a box |
| auth (Authelia 9091 + LLDAP 3890/17170) | `auth`, roles sso + identity | built-in: Authelia and LLDAP in one pod, configuration.yml, OIDC key and LLDAP seeding by the hook |
| adguard (53, 8083) | `adguard`, role dns | built-in: host network, first config by the hook, rewrites synced via the API |
| llama, llama-embed (11434/11436, GPU) | `llama`, role model, + `laib-lease` | ported, GPU-unverified (#18) |
| servicebay (5888) | Laib itself (5900) + Operator | done; retired at the end |

## Household and apps

| Today | Laib | Status |
|---|---|---|
| home-assistant (8123, zwave 8091, 3 containers) | `home-assistant` | built-in: HA, Z-Wave JS UI, Matter on the host network; onboarding and the Wyoming/CalDAV/MQTT integrations are configuration in HA |
| solaris (4 containers: chat, gatekeeper, …) | retired (ADR 0015); Haus + Bote + Frage | `bote`, `frage` and `haus` built-ins exist; Haus is its own repo, `mdopp/haus` |
| solaris-whisper, solaris-tts, solaris-wakeword-trainer, solaris-whisper-batch (GPU) | `stimme` | built-in: Whisper, Piper, openWakeWord, and Laib's own trainer (`laib-wakeword-trainer`, the persona's phrase, #128) as Wyoming units; Martin (Kokoro, OpenAI API) is not in the Assist path; whisper-batch was foundry-chronicle's and moves with it |
| media (Jellyfin, GPU) | `media` + `audiobookshelf` + `music-assistant` | built-ins: Jellyfin keeps music and video and gets a phone user limited to Musik (the Symfonium finding); Audiobookshelf takes Hörbücher and Podcasts with auto-download (replaces `podcasts/sync_podcasts.py`, which nothing ran); Music Assistant drives the speakers for Assist |
| immich (4 containers, ML) | `immich` | built-in |
| paperless (3 containers) | `paperless` | built-in with its own logins (no Remote-User trust, #231) |
| radicale (5232) | `radicale` | built-in: LDAP logins against LLDAP, owner-only rights |
| vaultwarden | `vaultwarden` | built-in with Authelia OIDC |
| file-share (Syncthing, Samba, filebrowser; 14 ports) | `file-share` | built-in on the host network; notes, documents, music, photos live here |
| mosquitto, esphome | `mosquitto`, `esphome` | built-ins |
| beets | `beets` | built-in |
| claude-dev, pi-web | `werkstatt` | built-in, v1 |
| solaris-import-google (8097) | one-shot importers into Radicale/Paperless (ADR 0017) | becomes scripts, no service |
| usage-metrics (8095) | ? | **decide**: keep as a registry template or drop |
| daggerheart-chronik (8701), daggerheart-asteroids (dead), flutstunde (8092) | external registries (flutstunde, foundry-chronicle) | **their repos add a `laib.yml`**; `laib template sync` pulls them |
| foundry-chronicle (lease holder, not a unit here) | client of `laib-lease` | moves to Laib's lease contract when it moves (ADR 0011 amendment 2026-10-14, foundry-chronicle#362) |

## New, not there today

| Needed by the concept | Where |
|---|---|
| Haus (pages, cards, `/napi`, the Haushalt MCP source) | new repo `mdopp/haus` (DEKONSTRUKTION step 3) |
| searxng | inside the `frage` pod, built-in |
| music-assistant | **port needed**, household registry |
| signal-cli | inside the `bote` pod while the Bote has Signal, built-in |
| the household registry itself | new repo `mdopp/laib-templates`, `laib template lint` in CI |

## The count

- Built-ins done (21): caddy, auth (Authelia + LLDAP), adguard, whoami,
  werkstatt, bote, frage, llama, media, audiobookshelf, music-assistant,
  radicale, vaultwarden, mosquitto, esphome, beets, home-assistant,
  immich, file-share, stimme (whisper, piper, openwakeword, trainer),
  paperless. Every unit that runs on the box today has a Laib template.
- Not yet run on any box: all of them. The NUC trial (Phase 1 to 3) and
  the Kraftwerk migration (Phase 4) are where they prove themselves.

## The Jellyfin finding (2026-10-10)

Audiobooks played in the middle of music on the phone, and podcasts never
appeared. Read-only look at the box: the music folder is clean (1,960
artist folders, no audiobooks), audiobooks sit in `audiobooks/`, and
Jellyfin registers them as a Books library. The player (Symfonium) syncs
every audio library it is allowed to see, so AudioBook items land in the
music queue. Podcasts: a hand-written `podcasts/sync_podcasts.py` exists
with two feeds but nothing runs it, and the Jellyfin provisioning skips
the `podcasts/` folder on purpose. On the box today, without changing
anything: in Symfonium untick the Audiobooks library. In Laib: a `phone`
Jellyfin user that sees Musik only, audiobooks and podcasts in
Audiobookshelf, radio and speakers through Music Assistant.
- Repos that need a `laib.yml`: flutstunde, foundry-chronicle (daggerheart).
- Decisions: usage-metrics keep or drop; `mdopp/haus` and
  `mdopp/laib-templates` to create.

Each port is mechanical: `scripts/migrate-servicebay.py` writes the
contract draft from `variables.json` and the backup annotation, `podlet`
or the existing Pod YAML gives the units, the ServiceBay `post-deploy.py`
becomes the hook (what it did for proxy/OIDC/credentials is rendered by
Laib and falls away). The Werkstatt's autoloop can work these as tickets
once the registry repo exists.
