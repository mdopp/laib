# What runs today, and what Laib still needs for it

Inventory of the GPU box on 2026-10-10 (read-only through ServiceBay's
MCP: 33 units, 32 active, one dead), mapped onto the Laib end state in
`docs/KONZEPT.md`. "Port" means: a template folder in the household
registry (`mdopp/laib-templates`), written from the ServiceBay template's
Pod YAML and `variables.json`, with `podlet`/hand for the units, then
`laib template lint`.

## Platform roles

| Today (unit) | Laib | Status |
|---|---|---|
| nginx (NPM, 80/443/81, 24 hosts) | `caddy`, role proxy | built-in exists; DNS-01 unverified on a box |
| auth (Authelia 9091 + LLDAP 3890/17170) | `auth`, roles sso + identity | built-in: Authelia and LLDAP in one pod, configuration.yml, OIDC key and LLDAP seeding by the hook |
| adguard (53, 8083) | `adguard`, role dns | built-in: host network, first config by the hook, rewrites, the PTR upstream (the router, #551), NXDOMAIN for PTR in 172.16.0.0/12 and the query log retention (#901) synced via the API |
| llama, llama-embed (11434/11436, GPU) | `llama`, role model, + `laib-lease` | ported, GPU-unverified (#18) |
| servicebay (5888) | Laib itself (5900) + Operator | done; retired at the end |

## Household and apps

| Today | Laib | Status |
|---|---|---|
| home-assistant (8123, zwave 8091, 3 containers) | `home-assistant` | built-in: HA, Z-Wave JS UI, Matter on the host network; onboarding and the Wyoming/CalDAV/MQTT integrations are configuration in HA |
| solaris (4 containers: chat, gatekeeper, …) | retired (ADR 0015); Haus + Bote + Frage | `bote`, `frage` and `haus` built-ins exist; Haus is its own repo, `mdopp/haus` |
| solaris-whisper, solaris-tts, solaris-wakeword-trainer, solaris-whisper-batch (GPU) | `stimme` | built-in: Whisper, Piper, openWakeWord, and Laib's own trainer (`laib-wakeword-trainer`, the persona's phrase, #128) as Wyoming units; Martin (Kokoro, OpenAI API) is not in the Assist path; whisper-batch was foundry-chronicle's and moved with it: `chronik-whisper` in foundry-chronicle's template (Stimme's Wyoming Whisper returns no timestamps) |
| media (Jellyfin, GPU) | `media` + `audiobookshelf` + `music-assistant` | built-ins: Jellyfin keeps music and video; the family logs in with their LLDAP accounts through the LDAP-Authentication plugin, Quick Connect on (#256); ServiceBay's twin libraries ("Music2" on Music's folders) are removed by media's post-deploy (#646). Audiobookshelf takes Hörbücher, Hörspiele and Podcasts: metadata next to the files, a paced one-time match against audible.de, Hörspiel series from `<NNN> - <Titel>`, the feeds of `podcasts/sync_podcasts.py` subscribed with auto-download (#646); its family login is set from Laib's OIDC secret on every apply, the cached client rebuilt, so the drift that retired it on ServiceBay cannot stay (#255). Music Assistant drives the speakers for Assist, with the share's radio stations as favourites and Audiobookshelf as a source (#646) |
| immich (4 containers, ML) | `immich` | built-in |
| paperless (3 containers) | `paperless` | built-in with OIDC logins through Authelia |
| radicale (5232) | `radicale` | built-in: LDAP logins against LLDAP, owner-only rights |
| vaultwarden | `vaultwarden` | built-in with Authelia OIDC |
| file-share (Syncthing, Samba, filebrowser; 14 ports) | `file-share` | built-in on the host network; notes, documents, music, photos live here |
| mosquitto, esphome | `mosquitto`, `esphome` | built-ins |
| beets | `beets` | built-in |
| claude-dev, pi-web | `werkstatt` | built-in, v1 |
| solaris-import-google (8097) | one-shot importers into Radicale/Paperless (ADR 0017) | becomes scripts, no service: `scripts/import-google.py` brings a Takeout's calendars and contacts into a person's Radicale over DAV (#372) |
| usage-metrics (8095) | `usage-metrics` | built-in (kept, #238): no route, in-box callers on 8094 (8095 is Music Assistant's), the SQLite file at ServiceBay's path; Haus and the Bote count into it, unanswered sentences 30 days (#266) |
| daggerheart-chronik (8701), daggerheart-asteroids (dead), flutstunde (8092) | external registries (flutstunde, foundry-chronicle) | **their repos add a `laib.yml`**; `laib template sync` pulls them. foundry-chronicle's is `chronik` (`templates/chronik`, foundry-chronicle#362): bot and timestamped Whisper in their own namespaces, Piper from `stimme` for the announcement |
| foundry-chronicle (lease holder, not a unit here) | client of `laib-lease` | speaks Laib's lease contract from `${MODEL_LEASE}`/`${MODEL_KEY}`, preset `gemma-4-12b` (ADR 0011 amendment 2026-10-14, foundry-chronicle#362) |

## New, not there today

| Needed by the concept | Where |
|---|---|
| Haus (pages, cards, `/napi`, the Haushalt MCP source) | new repo `mdopp/haus` (DEKONSTRUKTION step 3) |
| searxng | inside the `frage` pod, built-in |
| signal-cli | inside the `bote` pod while the Bote has Signal, built-in |
| the household registry itself | new repo `mdopp/laib-templates`, `laib template lint` in CI |

## The count

- Built-ins done (23): caddy, auth (Authelia + LLDAP), adguard, whoami,
  werkstatt, bote, frage, llama, media, audiobookshelf, music-assistant,
  radicale, vaultwarden, mosquitto, esphome, beets, home-assistant,
  immich, file-share, stimme (whisper, piper, openwakeword, trainer),
  paperless, haus, usage-metrics. Every unit that runs on the box today has a Laib template.
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

The same, 2026-10-24 (#646): Symfonium's shuffle played Marc-Uwe Kling,
podcasts and radio between the music. Jellyfin held thirteen libraries:
five were ServiceBay's twins ("Music2", "Music (cdopp)2", "Audiobooks2",
"Movies2", "Shows2", each on its original's folders), and Podcasts and
Radio (.strm streams) were *music* libraries. `sync_podcasts.py` ran once
by hand on 2026-07-26 (five episodes each, names with `&amp;`) and never
again. The fix, in the order the family is never without their books:

1. media's post-deploy removes the twins (strict: the name plus "2", the
   same kind, the same folders; Jellyfin's entry only); users who saw a
   twin see the original.
2. `laib add audiobookshelf` (the owner approves the new service and its
   public route): „Hörbücher & Hörspiele" and „Podcasts", metadata stored
   with each item, the one-time Audible match paced by
   `audiobookshelf-pflege.timer`, the feeds of `sync_podcasts.py`
   subscribed. It hands Music Assistant an API key: the same apply adds
   Audiobookshelf as MA's source.
3. Music Assistant's post-deploy imports `radio/*.m3u` and `*.strm` as
   radio favourites.
4. Only then `SPOKEN_WORD=audiobookshelf` in media's values (a proposal):
   media's post-deploy removes Jellyfin's Audiobooks, Podcasts and Radio,
   each only when Audiobookshelf or Music Assistant answers with its
   content, red with the reason otherwise.

Afterwards Symfonium sees music (and video) only; Hörbücher and Podcasts
are in the Audiobookshelf app (hoerbuecher.<domain>), radio on the
speakers through Music Assistant and Home Assistant.
- Repos that need a `laib.yml`: flutstunde. foundry-chronicle has one (`chronik`).
- Decisions: `mdopp/laib-templates` to create (usage-metrics: kept, #238).

Each port is mechanical: the contract comes from `variables.json` and the
backup annotation, `podlet` or the existing Pod YAML gives the units, the ServiceBay `post-deploy.py`
becomes the hook (what it did for proxy/OIDC/credentials is rendered by
Laib and falls away). The Werkstatt's autoloop can work these as tickets
once the registry repo exists.
