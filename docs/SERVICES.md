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
| auth (Authelia 9091 + LLDAP 3890/17170) | `authelia` + lldap, roles sso/identity | built-in has Authelia only: **LLDAP unit missing**, Authelia's own configuration.yml and the LLDAP seeding hook must come from ServiceBay's auth template |
| adguard (53, 8083) | role dns | **port needed**: template + rewrites from `render/adguard/rewrites.json` |
| llama, llama-embed (11434/11436, GPU) | `llama`, role model, + `laib-lease` | ported, GPU-unverified (#18) |
| servicebay (5888) | Laib itself (5900) + Operator | done; retired at the end |

## Household and apps

| Today | Laib | Status |
|---|---|---|
| home-assistant (8123, zwave 8091, 3 containers) | `home-assistant`, also the voice brain (ADR 0015) | **port needed**; HA's config stays in its data dir; Assist, CalDAV and Music Assistant integrations are configuration on the box |
| solaris (4 containers: chat, gatekeeper, …) | retired (ADR 0015); Haus + Bote + Frage | `bote`, `frage` built-ins exist; **Haus missing** (new repo) |
| solaris-whisper, solaris-tts, solaris-wakeword-trainer, solaris-whisper-batch (GPU) | Stimme: Wyoming whisper, TTS, trainer | **port needed** (4 units); the gatekeeper is dropped |
| media (Jellyfin, GPU) | `media` | **port needed**; Music Assistant comes new (Einslive, playlists) |
| immich (4 containers, ML) | `immich` | **port needed** |
| paperless (3 containers) | `paperless` | **port needed**; registry template today |
| radicale (5232) | `radicale`, now actually used (ADR 0017) | **port needed**; LLDAP accounts + the Bote's service account |
| vaultwarden | `vaultwarden` | **port needed** |
| file-share (Syncthing, Samba, filebrowser; 14 ports) | `file-share`; notes live here (ADR 0017) | **port needed**; host-network carve-out |
| mosquitto, esphome | `mosquitto`, `esphome` | **port needed** (small) |
| beets | `beets` | **port needed** (small) |
| claude-dev, pi-web | `werkstatt` | built-in, v1 |
| solaris-import-google (8097) | one-shot importers into Radicale/Paperless (ADR 0017) | becomes scripts, no service |
| usage-metrics (8095) | ? | **decide**: keep as a registry template or drop |
| daggerheart-chronik (8701), daggerheart-asteroids (dead), flutstunde (8092) | external registries (flutstunde, foundry-chronicle) | **their repos add a `laib.yml`**; `laib template sync` pulls them |
| foundry-chronicle (lease holder, not a unit here) | client of `laib-lease` | address change only |

## New, not there today

| Needed by the concept | Where |
|---|---|
| Haus (pages, cards, `/napi`, the Haushalt MCP source) | new repo `mdopp/haus` (DEKONSTRUKTION step 3) |
| searxng | inside the `frage` pod, built-in |
| music-assistant | **port needed**, household registry |
| signal-cli | inside the `bote` pod, built-in |
| the household registry itself | new repo `mdopp/laib-templates`, `laib template lint` in CI |

## The count

- Built-ins done: caddy, authelia, whoami, werkstatt, bote, frage, llama (7).
- Ports needed for the box as it runs today: lldap (into auth), adguard,
  home-assistant, stimme ×4, media, immich, paperless, radicale,
  vaultwarden, file-share, mosquitto, esphome, beets (**16 templates**),
  plus music-assistant (new).
- Repos that need a `laib.yml`: flutstunde, foundry-chronicle (daggerheart).
- Decisions: usage-metrics keep or drop; `mdopp/haus` and
  `mdopp/laib-templates` to create.

Each port is mechanical: `scripts/migrate-servicebay.py` writes the
contract draft from `variables.json` and the backup annotation, `podlet`
or the existing Pod YAML gives the units, the ServiceBay `post-deploy.py`
becomes the hook (what it did for proxy/OIDC/credentials is rendered by
Laib and falls away). The Werkstatt's autoloop can work these as tickets
once the registry repo exists.
