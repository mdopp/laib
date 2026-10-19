# Migration day — the handoff for the session that runs it

## Für den Besitzer (kurz, auf Deutsch)

**Bevor irgendetwas passiert:**

1. **Eine frische Sicherung außer Haus, vom selben Tag.** In ServiceBay
   unter *Backup* die Sicherung auf die FritzBox (NAS) jetzt laufen lassen
   und warten, bis sie fertig ist. Das Skript prüft das und verweigert den
   Start ohne sie. Nur wenn du ausdrücklich ohne Sicherung weitermachen
   willst, gibst du `--no-fresh-backup` mit. **Wichtig:** Diese Sicherung
   enthält die Einstellungen der Dienste, nicht die Fotos, nicht Immichs
   Datenbank, nicht die Paperless-Dokumente und nicht die Musik. Davon gibt
   es heute keine Kopie außer Haus (der RAID-Spiegel hat nur eine Platte).
   Der Immich-Schritt benennt die Fotos auf derselben Platte um und schreibt
   Immichs Datenbank in einer Transaktion um, mit einem `pg_dump` daneben auf
   derselben Platte. Wenn dir das zu wenig ist: vorher die Fotos und
   `stacks/immich/pgdata` auf eine USB-Platte kopieren. Das ist deine
   Entscheidung, die Sitzung fragt dich danach.
2. **ServiceBays automatische Updates aus** (ServiceBay → Einstellungen:
   Auto-Update und Update-Fenster Sa/So 03:00), damit nichts mitten im
   Umzug einen Dienst neu startet. Das Skript warnt, solange sie an sind.
3. **Immichs Admin-Adresse** nennen (Immich → Administration → Benutzer).
   ServiceBay hat sie auf dem Kraftwerk nicht gespeichert, ohne sie bleibt
   der Lauf beim Schritt `drafts` stehen.
4. **Passwörter, die du in einer App geändert hast.** Laib übernimmt die
   Admin-Passwörter, die ServiceBay gesetzt hat. Hast du das
   Administrator-Passwort in Home Assistant (oder AdGuard) später in der App
   geändert, kennt ServiceBay es nicht. Der Probelauf meldet sich damit bei
   der laufenden App an, ohne etwas zu ändern, und zeigt `RED … meldet sich
   nicht an`. Dann den Lauf bis `secrets` laufen lassen, das jetzige
   Passwort mit `laib secret set HA_ADMIN_PASSWORD` (bzw.
   `ADGUARD_ADMIN_PASSWORD`) setzen und mit `--resume` weitermachen (#373).
5. **Ein DNS-Eintrag für jeden neuen Namen.** Caddy holt seine Zertifikate
   per HTTP-01, dafür braucht jeder Name einen öffentlichen Eintrag (bei
   Strato: ein CNAME auf `dopp.cloud`). ServiceBay hatte nur Einträge für
   seine eigenen Adressen; neue wie `beets` und `esphome` fehlen. Der
   Probelauf zeigt sie als `RED öffentlicher DNS-Eintrag fehlt: …`. Vor
   Phase 4 anlegen (#377).
6. **Für die Werkstatt** (sie ersetzt claude-dev und pi-web): ein
   Claude-OAuth-Token und zwei GitHub-Tokens bereithalten. Ohne sie bleibt
   der Lauf vor der Werkstatt stehen, ohne etwas zu ändern.

**Ein Zeitfenster ohne neue Anmeldung:** Vom Schritt `auth` bis Caddy
übernimmt (Phase 2 bis 4), antwortet `auth.dopp.cloud` mit 502. Wer in der
Zeit neu angemeldet werden müsste (Paperless, Immich, Home Assistant über
Authelia, Vaultwarden), kommt nicht hinein. Deshalb laufen Phase 2 bis 4 an
einem Stück, das sind einige Stunden. Die Alternative ist, ServiceBays
NPM-Einträge für `auth`, `vault` und `llama` für das Wochenende auf
127.0.0.1 umzustellen: kein Ausfall, aber ein Eingriff in NPM. Die Sitzung
fragt dich, welche Variante du willst.

**Wo du dabei sein musst:** beim Start (die Sicherung, Punkt 1), nach jeder
Phase für die Stichproben in der Checkliste unten (anmelden, ein Foto, ein
Dokument, ein Licht), beim Wechsel von NPM zu Caddy (danach laufen alle
Adressen über Caddy; `chat`, `import`, `asteroids` und `flut` sind dann
weg, `dopp.cloud` und `www` leiten auf `haus` um, siehe "Known risks"), für die Werkstatt-Tokens und
ganz am Ende, wenn ServiceBay ausgeht. Danach setzt jedes Familienmitglied
in Haus (`/netzlaufwerk`) sein eigenes Samba-Passwort: auf dem Kraftwerk gibt
es heute nur das gemeinsame Konto.

**Wenn etwas rot wird,** hält das Skript an und gibt den betroffenen Dienst an
ServiceBay zurück. Er läuft dann wie vorher. Die Sitzung sagt dir, was los
war, und macht erst nach der Ursache weiter.

---

This is for a fresh Claude Code session on the owner's own computer. It
SSHes into the Kraftwerk as `core` and runs `scripts/migration-day.sh`
(#350). It does **not** run inside the Kraftwerk's `claude-dev` container,
because the `werkstatt` step replaces that container. The script refuses to
run inside a container.

## Read first

1. [`CLAUDE.md`](../CLAUDE.md): the three rules. Scripts do the mechanics,
   proof is on the box, KISS.
2. [`docs/MIGRATION.md`](MIGRATION.md): the order. The script runs it step
   by step and adds nothing it does not say.
3. This file.
4. The ADRs that bind the migration:
   [0010](adr/0010-one-migration-no-compat.md) (one migration, no
   compatibility layer),
   [0007](adr/0007-caddy-replaces-npm.md) (Caddy replaces NPM),
   [0008](adr/0008-unmanaged-is-drift.md) (an unmanaged unit is adopted or
   ignored),
   [0011](adr/0011-model-server-is-a-laib-role.md) (llama is Laib's `model`
   role),
   [0015](adr/0015-voice-is-ha-assist-solaris-retired.md) (Solaris retires,
   it does not move),
   [0006](adr/0006-identities-hands-services-age-key.md) (hand tokens,
   service secrets, the `age` key),
   [0013](adr/0013-the-agent-is-the-operator.md) and
   [0019](adr/0019-calm-before-autonomy-and-the-trust-kernel.md) (what an
   agent may do on its own),
   [0021](adr/0021-names.md) (names).

## The owner's decisions already taken

Link, don't re-ask. Each one was decided; asking again is noise.

| Decision | Where |
|---|---|
| A change is judged against what worked before: rethink freely, but it must be easier to maintain or more pleasant for the family, and neither noticeably worse | [#246](https://github.com/mdopp/laib/issues/246), `CLAUDE.md` |
| Immich's photos **move** into Laib's layout and Immich's paths are rewritten in its database; the Syncthing folders move with them; albums, faces and favourites must survive | [#229 (1)](https://github.com/mdopp/laib/issues/229#issuecomment-6214051479) |
| Home Assistant keeps the Authelia login (the built-in declares the OIDC client) | [#229 (3)](https://github.com/mdopp/laib/issues/229#issuecomment-6214051479) |
| The Werkstatt gets a persistent home; claude-dev's sessions are copied over once that exists | [#229 (4)](https://github.com/mdopp/laib/issues/229#issuecomment-6214051479), [#241](https://github.com/mdopp/laib/issues/241) |
| Samba: one account per family member, synced from LLDAP, password set in Haus (supersedes #229 (2)'s single family login) | [#296](https://github.com/mdopp/laib/issues/296) |
| flutstunde (asteroids) and the inactive daggerheart-asteroids do not move, they go with ServiceBay; daggerheart-chronik and usage-metrics get Laib templates | [#160](https://github.com/mdopp/laib/issues/160#issuecomment-6213514058) |
| Qwen 3.x is the household model and the default preset for everyone | [#285](https://github.com/mdopp/laib/issues/285#issuecomment-6233774062) |
| A capable model may run `apply` itself, within the budget (envelope, snapshot first, automatic rollback) | [#286](https://github.com/mdopp/laib/issues/286#issuecomment-6233774202) |
| The kernel's cap is 15,000 production lines, a warning at 95 %, raised only on purpose with the owner's approval; the lease and the relay stay in the kernel | [#309](https://github.com/mdopp/laib/issues/309), [#315](https://github.com/mdopp/laib/pull/315) |
| Tests are not capped: "Tests may have more lines than code" | [ADR 0005, amendment 2026-10-18](adr/0005-test-pyramid-verify-in-apply.md) |
| The first precondition of migration day is a fresh off-box backup, refused without one unless `--no-fresh-backup` | [#350](https://github.com/mdopp/laib/issues/350) |

Decided 2026-10-18 (owner): `dopp.cloud`, `www.` and `import.` are not
needed. `import.` goes dark with NPM and gets no Laib route; `dopp.cloud`
and `www.` redirect to `haus.` from Caddy's switch on (#360: the haus
route's `redirectFrom`, no service), so the family's old entry point leads
to Haus. ServiceBay's portal and its request form behind them are gone;
residents are invited (`laib person invite`, Haus's Box card). `solaris-import-google`
goes off with radicale in phase 3: it writes into radicale's data, which
`laib add radicale` refuses as a `KONFLIKT` (#371); its importers become
scripts (SERVICES.md). The dry run lists every unit that stays and would be
a `KONFLIKT` for a step, as `RED`. Listed on
[#160](https://github.com/mdopp/laib/issues/160).

## Preconditions

On the owner's computer:

- `ssh core@192.168.178.100` works (the host's sshd on 22; 2222 is
  claude-dev's and goes away).
- `gh auth status` is logged in to github.com: you report on #160 and file
  what turns up as issues.

On the box (the dry run checks each one and says which is red):

- A fresh off-box backup: ServiceBay's NAS backup at most 24 hours old
  (`BACKUP_MAX_AGE_H`), with status `ok`, `success` or `partial` (its normal
  state on the Kraftwerk: five services without a backup declaration, and
  Samba's sockets). A restic snapshot counts too, when the owner names one
  (`RESTIC_REPOSITORY`, `RESTIC_PASSWORD_FILE`).
- Running as `core`, on the host, linger on.
- ServiceBay's container runs ServiceBay 5.44.3, the version the
  generalprobe proved, with its templates at `/app/templates`.
- At least 50 GiB free on the data disk.
- The checkout and the Laib binary are one release: the checkout's
  `version.txt` equals `laib version` once installed. Use the release that
  carries this script; v0.6.0 itself does not. Until the next release, a
  checkout of `main` after #351 installs the v0.6.0 binary and matches.
- python3 on the host (ServiceBay's agent needs it; it is there).
- The carried administrator logins of Home Assistant and AdGuard work
  against the apps ServiceBay runs (read-only: HA's login flow up to its
  code, no token; AdGuard's API with basic auth; #373). A password changed in
  the app after ServiceBay set it is red here: `--until secrets`, then
  `laib secret set HA_ADMIN_PASSWORD` (or `ADGUARD_ADMIN_PASSWORD`) with the
  app's password, then `--resume`. Immich and Jellyfin are not tried: their
  login opens a session.
- Every route name of the state-to-be has a public DNS record, when the box
  takes Let's Encrypt by HTTP-01 (no `--dns-provider`, not `--tls internal`):
  resolved through DNS over HTTPS (`PUBLIC_DNS`, dns.google), not the box's
  AdGuard, whose rewrites answer every name (#377). A new name needs a CNAME
  to the apex before phase 4, or a `dns.provider` with a wildcard via DNS-01.
  The caddy step checks the same before NPM goes, and stops with nothing
  changed.

## The commands

All on the box, as core, from `~/laib`.

```sh
ssh core@192.168.178.100
git clone https://github.com/mdopp/laib ~/laib || git -C ~/laib pull --ff-only
cd ~/laib
export IMMICH_ADMIN_EMAIL=<the owner's answer>      # in every SSH session that starts a run

scripts/migration-day.sh --dry-run                  # every step and precondition; changes nothing
```

Read the dry run with the owner. Every `RED` line must be fixed first. The
`note` lines say what goes on the add lines (MQTT's user `mqtt`) and which
Quadlet files no step claims. The retire step stays red while one of those
is left.

Then the phases. Each call starts the transient unit `laib-migration` and
returns. The unit goes on when your SSH session ends.

```sh
scripts/migration-day.sh --until secrets            # phase 1: Laib v0.6.x beside ServiceBay, the drafts, the secrets
# phases 2 to 4 in one sitting: from auth until Caddy, auth.<domain> answers 502 through NPM
scripts/migration-day.sh --resume --until auth      # phase 2: Authelia + LLDAP
scripts/migration-day.sh --resume --until vaultwarden   # phase 3: adguard, llama, the household services
scripts/migration-day.sh --resume --until caddy     # phase 4: Caddy replaces NPM
#   the Werkstatt's tokens, now, with the owner (templates/werkstatt/README.md):
#   laib secret set CLAUDE_CODE_OAUTH_TOKEN, WERKSTATT_GIT_TOKEN, WERKSTATT_PI_GIT_TOKEN (each value on stdin);
#   laib token mint claude --note werkstatt, then laib secret set LAIB_CLAUDE_TOKEN; the same for pi (LAIB_PI_TOKEN)
scripts/migration-day.sh --resume                   # phase 5: werkstatt, what stays ignored, ServiceBay off
```

Watch:

```sh
scripts/migration-day.sh status                     # each step: green, red, skip, rolledback, pending; the log's tail
scripts/migration-day.sh wait 2400                  # until the unit ends, at most 40 minutes; then status
journalctl --user -u laib-migration -f              # live
less ~/laib-migration/log                           # all of it, also after the unit is gone
```

Never wait longer than 40 minutes in one call: `wait 2400`, read `status`,
wait again.

**Red.** The run stops at the first red step. The service in flight goes
back to ServiceBay: out of Laib's state, ServiceBay's files back in the
Quadlet root, its unit started. The run then prints `RED at <step>`. Read
the lines above it (the apply's verify, the containers' own words). Fix the
cause, with an issue if it is code, then:

```sh
scripts/migration-day.sh --resume [--until <step>]  # every step not green yet, in order
```

The copies the red attempt made are stale (ServiceBay wrote on) and are
removed before the move runs again. A run cut off mid-step (power, `systemctl
--user stop laib-migration`) is resumed the same way: a service its add had
already put in the state gets `laib apply`, as MIGRATION.md says.

**Roll back one service** (MIGRATION.md, "Rollback during the weekend"):

```sh
scripts/migration-day.sh rollback home-assistant    # Laib's out, ServiceBay's back and running
scripts/migration-day.sh --resume --until vaultwarden   # takes it over again later
```

**Immich is the exception.** Once its library move ran, its photos are in
Laib's layout, so the script neither hands it back on red nor rolls it back:
ServiceBay's Immich would find its libraries empty. Fix forward with
`--resume` (the move finishes from its journal). Or roll it back by hand as
MIGRATION.md describes (photos renamed back, `pg_restore` of
`immich/before-library-move.dump`, `config.xml.servicebay` back), with the
owner.

**Abort entirely:**

```sh
systemctl --user stop laib-migration                # if it runs
scripts/migration-day.sh rollback all               # every service back, Caddy first (NPM again), in reverse order
```

Laib stays installed beside ServiceBay with an empty state: it runs
nothing. After the `retire` step, rollback is a restore of ServiceBay's data
dir, which is why it is kept 30 days.

## Check after each phase

The generalprobe's household checks (`scripts/generalprobe/household.py`),
by hand with the owner. Each one is what the family would notice.

After **phase 1**: `laib status` lists no service; `laib secret list` has the
nine carried secrets (`LLDAP_ADMIN_PASSWORD`, `AUTHELIA_STORAGE_KEY`,
`IMMICH_DB_PASSWORD`, `IMMICH_ADMIN_PASSWORD`, `PAPERLESS_DB_PASSWORD`,
`MQTT_PASSWORD`, `JELLYFIN_ADMIN_PASSWORD`, `ADGUARD_ADMIN_PASSWORD`,
`SAMBA_PASSWORD`); `laib diag` is not `rot box`; every service still answers
under its name.

After **phase 2** (auth):
- a family member logs in at `https://auth.dopp.cloud` with the old password
  and the old second factor;
- a wrong password is refused;
- the apply printed which OIDC clients are still ServiceBay's.

After **phase 3** (the household services):
- **Photos:** the albums are whole, a photo opens, the library photos are
  not offline; a photo taken on a phone afterwards arrives.
- **Documents:** Paperless finds a known document by a word in it.
- **Vault:** an item is still in the vault (`vault.` answers 502 until
  phase 4, by design).
- **Calendar:** Radicale answers with a family member's LLDAP login.
- **Home:** Home Assistant shows the automations and integrations; the
  Z-Wave JS entry points at `:3000`; a light switches; the Matter devices
  are still paired.
- **MQTT:** the devices and Home Assistant are on the broker with the old
  credentials.
- **Files:** Syncthing's device ID is unchanged (nothing re-paired); File
  Browser shows the same users and shares.
- **Media:** Jellyfin lets a family member in with the LLDAP login.

After **phase 4** (Caddy): every name of the state answers through Caddy
(the script checks it; none answers 502); the family's login works at
`https://auth.dopp.cloud` through Caddy; Let's Encrypt certificates, not
Caddy's own; AdGuard's rewrites point at the box.

After **phase 5**: `laib status` first line `0 gelb · 0 rot`; ServiceBay's
container is gone and nothing answers on 5888; the Quadlet root has no
ServiceBay file except the ignored ones (Solaris, chronik);
the Werkstatt answers on SSH 2222 (its host key is new, the app asks once).

## Known risks

- **No off-box copy of the data.** See the owner's section: the NAS backup
  holds configs. The content backup is off and has failed since 2026-09-07.
  The data disk is a one-disk RAID1.
- **The two leaked passwords** (mdopp/servicebay#3054): Paperless's database
  password and the LLDAP admin password were in an agent transcript. They
  are carried into Laib unchanged, because the data was made with them.
  Rotate both with Laib after the move, but **not with `laib secret rotate`
  yet.** That verb writes a new value and re-applies, and LLDAP and Postgres
  read these only when they create their database, so auth and Paperless
  would be locked out ([#353](https://github.com/mdopp/laib/issues/353)). A
  data-bound secret needs a hook that changes it inside the app. Until #353
  is done, rotating them means changing the value in the app by hand (LLDAP
  UI, `ALTER ROLE` in Paperless's Postgres) and then `laib secret set` with
  the same value. Do that with the owner.
- **Samba** (mdopp/servicebay#3055): ServiceBay's LLDAP→Samba sync calls
  `useradd`, which the Samba image no longer has. On the Kraftwerk the
  passdb holds a single entry (105 bytes: one smbpasswd line, by its size
  the shared account), so no family member has a per-person login today, and
  none is lost. Laib's file-share keeps the shared account and makes the
  per-person ones; each person sets a first password in Haus.
- **No new login from phase 2 until phase 4.** Laib's Authelia,
  Vaultwarden and llama listen on 127.0.0.1 only, while ServiceBay's NPM
  proxy hosts for `auth.`, `vault.` and `llama.` forward to the LAN address,
  so those three answer 502 through NPM until Caddy serves them. NPM's
  forward-auth itself asks 127.0.0.1 and keeps working, but its login page is
  `auth.`. So run phases 2 to 4 in one sitting. The owner may instead want
  NPM's three proxy hosts pointed at 127.0.0.1 for the weekend. That is not
  built: ask, with both options, before phase 2. The generalprobe found this
  (#350).
- **Names that go dark with NPM** in phase 4, because no Laib service routes
  them: `chat.` (Solaris), `import.` (the Google import), `asteroids.` and
  `flut.` (go with ServiceBay, #160). `ws-test-2205.` is stale.
  `dopp.cloud` and `www.` (ServiceBay's portal; also the token mint URL
  Solaris uses) do not go dark: Caddy redirects them to `haus.` (#360). The
  owner does not need `import.` (2026-10-18); ask before phase 4 only
  whether `chat.` may go dark for the weekend.
- **chronik** stays on its ServiceBay unit (ignored) until
  foundry-chronicle#362 brings its Laib template. It keeps running.
- **Untested on the VM:** the generalprobe installs auth, file-share,
  mosquitto, radicale, vaultwarden, home-assistant, immich, paperless and
  NPM. The steps for adguard, llama, beets, esphome, usage-metrics, media and
  werkstatt run the same code, but only the Kraftwerk proves them. Expect
  the first red there, and read it.
- **ServiceBay's own updates** can restart a service mid-move. Switch them
  off (owner's section).
- `/boot` is 91 % full. Nothing in the migration writes to it. Do not run
  an OS update this weekend.
