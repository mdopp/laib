# Laib — the one migration from ServiceBay

Only one box runs ServiceBay. So: no compatibility layer, no dual control
plane for months, no feature flags. One weekend, one order, one script that
reads ServiceBay's config once.

## Running side by side

Both run at once: ServiceBay in its container on 5888, Laib as a user
service on 5900; separate state, separate nftables tables
(`servicebay_lanblock` / `laib`), separate tokens. ServiceBay's `<name>.kube`
files sit in the Quadlet root, Laib's units in `<name>/` folders. Three
things cannot be shared:

1. **A service.** Quadlet names units after the file, not the folder, and
   ServiceBay's `auth.kube` runs the same Authelia and LLDAP on the same
   data as Laib's `auth/` units, whatever those are called. `laib plan`
   reports `KONFLIKT` for every file in the Quadlet root that would run a
   service of the state beside Laib: the same name or unit name, a host port
   the service publishes (`PublishPort=`, `hostPort:`, a host-network pod's
   `containerPort:`), or a host path at, inside or above one of its
   backed-up data paths (`Volume=`, `Mount=`, a `hostPath`; a `.kube` is
   read with its `Yaml=`). What lies at or below a `backup: false` path is
   shared on purpose: the shared tree `file-share/data` with its folders,
   which Immich, Jellyfin, Beets and Solaris read and write. While one is
   reported, every `laib apply` refuses before it generates a secret or
   writes a file, so ServiceBay's unit is stopped and its files moved out
   **before** the service goes into the state (step 4). A `laib add` that
   refuses has already put the service into the state: move the file out,
   then `laib apply`. Not seen: a host-network `.container` declares no
   ports, so ServiceBay's `llama-embed.container` (11436, Laib's llama's
   embeddings port) is caught by nothing; it goes out with `llama` (step 4).
2. **Ports 80/443.** NPM holds them until the services have moved; then
   Caddy replaces it (step 4d).
3. **Whole-file configs.** Authelia, AdGuard rewrites and the SSH port 2222
   (claude-dev vs Werkstatt) move as a whole, never half.

## Principle

A service belongs to exactly one control plane. It is ServiceBay's while its
file is in the Quadlet root and Laib's state does not have it; it is Laib's
from the moment ServiceBay's unit is stopped, its file moved out and
`laib add <name>` puts it in the state. There is no marker and no in-between:
the `KONFLIKT` check above keeps the two apart (`migratedFrom` was dropped,
#227; ADR 0010: the kernel knows nothing about ServiceBay). A Quadlet unit
cannot be disabled while its file is there, so "disable" always means the
file moves out.

## Order

1. **Laib binary + state repo**, nothing applied. `install-box.sh` (as in
   `docs/INSTALL.md`; it installs the binaries and the unit and runs
   `laib init --domain …`; the Kraftwerk is the GPU box: `--gpu`, without
   which `laib add immich` refuses, its machine learning wants the GPU)
   also writes the box's own config, as on a stick
   install: journald persistent with at most 4G and four weeks
   (`/etc/systemd/journald.conf.d/50-laib.conf`, journald restarted) and
   podman's log driver pinned to journald
   (`~/.config/containers/containers.conf.d/50-laib.conf`, a drop-in:
   ServiceBay's own containers.conf stays; its containers keep logging to
   journald, Fedora CoreOS's default). The Kraftwerk is never reinstalled, so
   this is the only way these reach it; `laib diag` is `rot box` while either
   file is missing, and `install-box.sh --box-config` writes just these two (#281).
   Then the migration scripts and ServiceBay's templates as the box runs
   them — the built-ins in its image, the external registries it cloned into
   its container:
   ```sh
   git clone --depth 1 https://github.com/mdopp/laib ~/laib && cd ~/laib
   mkdir -p ~/servicebay-templates
   podman cp servicebay:/app/templates ~/servicebay-templates/servicebay
   podman cp servicebay:/app/.servicebay/registries ~/servicebay-templates/registries
   python3 scripts/migrate-servicebay.py --config /mnt/data/servicebay/config.json \
     --templates ~/servicebay-templates/servicebay,$(ls -d ~/servicebay-templates/registries/*/templates | paste -sd,) \
     --out /mnt/data/laib/migration | tee /mnt/data/laib/migration.txt
   ```
   (python3 is on the box: ServiceBay's agent needs it.) It writes a draft `<name>/` per installed
   template — laib.yml with the routes the box's proxy hosts have and the auth
   ServiceBay applied, data paths from the Pod YAML's host paths, secrets and
   ports from `variables.json`; values.yml from `installedVariables` — and
   prints what it could not map: proxy hosts and variables without an
   installed service, named volumes, data the Laib built-in would not mount.
   The drafts never go into the state repo (the script refuses): a
   `services/<name>/` without units makes laib refuse the whole state, and
   `laib add <name>` refuses one that exists (#226). Where Laib has a
   built-in, the draft is what to compare `laib add <name>` against; the
   others get their units from their own repo's `laib.yml` or by hand
   (ADR 0004: `.container` is the primary form). Secrets: `laib secret set`
   for the external ones (DNS token, SMTP, Fritz!Box, GitHub, Claude OAuth),
   and the data-bound ones carried over (below). The rest is generated on
   first apply and the post-deploy hook seeds the app. Nothing runs yet.
2. **NPM stays the proxy until step 4 is done.** Caddy cannot start beside
   it: both are host-network, and Caddy binds 80 and 443, which NPM holds;
   and Caddy's routes come from the state, which holds none of the
   household's services yet. Meanwhile NPM keeps serving every name: Laib's
   built-ins publish the ports ServiceBay's proxy hosts forward to (one
   that publishes on 127.0.0.1 only gets its proxy host repointed, 4c), and
   Laib's Authelia answers NPM's `auth-request` as ServiceBay's did
   (Authelia's default endpoints). Caddy replaces NPM in step 4d.
3. **auth** (Authelia + LLDAP), by the a–c of step 4: ServiceBay's
   `auth.kube` out first, then `laib add auth`. Only the services already
   in Laib's state have Laib clients at this point, so the apps still on
   ServiceBay keep theirs another way (#245): auth's pre-start finds
   ServiceBay's `authelia-config/configuration.yml`, the file it is about to
   overwrite, keeps its `clients:` once in `servicebay-clients.yml` and its
   signing key as `oidc.pem`, and registers each kept client, verbatim with
   ServiceBay's secret, until a Laib service declares the same client id.
   Then that service's client is Laib's: same id, Laib's secret, which the
   service's own unit or hook hands to the app (Vaultwarden's environment,
   Immich's settings, Home Assistant's `auth_oidc`). The apply prints which
   clients are still ServiceBay's. The signing key stays, so the JWKS and
   every token an app holds stay valid. From here on, a client ServiceBay
   registers itself is ignored (Authelia reads the clients from
   `oidc-clients.yml`): install no new SSO app through ServiceBay.
4. **adguard**, then **llama** as the `model` role (ADR 0011): the template
   moves from solarisbay into Laib with the lease sidecar; Its consumers
   (foundry-chronicle now, the Werkstatt's pi from step 5) speak Laib's lease contract (ADR 0011,
   amendment 2026-10-14): a preset instead of a window name, a holder, the
   model key. Then **vaultwarden, radicale, mosquitto, file-share, beets,
   immich, paperless, media, home-assistant, chronik** in that order: small and stateless first,
   the ones with migrations last. Each one, in this order (the service is
   down from a to c; that is the price of never having two writers on one
   database):
   a. Stop ServiceBay's unit and move its files out of the Quadlet root:
      ```sh
      systemctl --user stop <name>.service
      mkdir -p ~/servicebay-units
      mv ~/.config/containers/systemd/<name>.kube ~/.config/containers/systemd/<name>.yml ~/servicebay-units/   # or <name>.container
      systemctl --user daemon-reload
      ```
      For `llama`, `llama-embed.service` is stopped and `llama-embed.container`
      moved out with it.
   b. Carry its secrets over (#228) and, where "Data that moves into
      Laib's layout" lists it, `scripts/move-servicebay-data.sh <name>`
      (#229).
   c. The `laib add <name> …` line `migrate-servicebay.py` printed for it
      (the box's values, a database's image pinned; it applies). `laib plan`
      shows no `KONFLIKT`, the apply ends green, the app shows the old data.
      Its names still answer through NPM. Where one answers 502, the
      built-in publishes on 127.0.0.1 only (Vaultwarden) while ServiceBay's
      proxy host forwards to the LAN address: in NPM, set that proxy host's
      forward host to `127.0.0.1` (NPM runs on the host network).
   **chronik** (daggerheart-chronik) comes from foundry-chronicle's registry
   (`laib template sync`) and needs `llama` for the lease and `stimme` for
   Piper. Its data moves once, in step b with ServiceBay's unit stopped (one
   Discord token, one bot): `${DATA_DIR}/daggerheart` → `chronik/data`,
   `daggerheart-aufnahmen` → `chronik/aufnahmen`, copied, never moved; the
   commands are in foundry-chronicle's `templates/chronik/README.md`. The
   Discord token carries over with `laib secret set CHRONIK_DISCORD_TOKEN`.
   d. When every routed service is Laib's: **Caddy replaces NPM.** NPM's
      unit out by a. (`nginx`), then `laib add caddy`; its routes are the
      state's, now the full list. Verify each name through Caddy, then the
      AdGuard rewrites, if any, point at the box as before.
5. **Werkstatt** replaces `claude-dev` + `pi-web`: one template; their git
   checkouts are copied into the Werkstatt's workspace (below). Their
   sessions and SSH host keys are an open decision (below).
6. **Solaris does not move** (it retires, ADR 0015). Its units stay
   ignored (`laib ignore`) while Haus's bridge still needs them (haus#10);
   they go off in Phase 5 (#160).
7. Disable `servicebay.service`. Keep its data dir for 30 days, then delete.
   Delete `auth/authelia-config/servicebay-clients.yml` (ServiceBay's own
   `servicebay` client and any app that never moved), then `laib apply auth`.
   ServiceBay's assist catalog: there is no import tool yet; what happens to
   it is decided in #160.

## Data that moves into Laib's layout (#229)

The built-ins keep their own layout. They do not learn ServiceBay's paths;
the data moves into theirs once, per service, after ServiceBay's unit is out
(step 4) and before `laib add`:

```sh
scripts/move-servicebay-data.sh --dry-run <service>   # what it would do
scripts/move-servicebay-data.sh <service>             # on the box, as core
```

It **copies** and never moves. ServiceBay's paths stay as they were, so
handing a service back is re-enabling its unit, and they go with ServiceBay's
data dir after 30 days. Two files are edited in place, and their ServiceBay
version is kept beside them as `<file>.servicebay`; a rollback puts those
back. It refuses a service whose ServiceBay file is still in the Quadlet
root, whose ServiceBay unit still runs (the wrapper reads the running units
from `systemctl --user`; a live database copied is no copy), whose target
already holds data, or whose copy does not fit with 1 GiB to spare, before
writing anything of it. Each target is copied into `<target>.partial`, synced
and renamed, so a copy cut off by a power cut or an error is never where the
built-in starts on it; the next run clears the `.partial` and copies again. A
second run of a finished service is refused: its targets hold data.
Ownership is kept as the container uids have it (the copy runs as root in the
laib-hooks image); SELinux labels are set by the built-in's `:z` on its first
start; sockets and FIFOs are left out, hardlinks become copies.

| Service | ServiceBay on the box | Laib | Step |
|---|---|---|---|
| vaultwarden | `vaultwarden/` (db.sqlite3, rsa_key.pem in it) | `vaultwarden/data` | copied |
| home-assistant | `home-assistant/matter-server` | `home-assistant/matter` | copied: the Matter fabric survives |
| home-assistant | Z-Wave JS server on 3001 through `sb-external-settings.json` (3000 was NPM's) | zwave-js-ui's own `settings.json`, its default 3000 | edited; Home Assistant's Z-Wave JS entry (`.storage/core.config_entries`) follows to `:3000` |
| file-share | podman named volume `file-share-syncthing-config` | `file-share/syncthing-config` | copied: device ID and keys survive, nothing is re-paired |
| file-share | `file-share/filebrowser-db/filebrowser.db` | `file-share/filebrowser/filebrowser.db` | copied: users, scopes and share links survive; `filebrowser-config` stays behind (the unit configures File Browser) |
| file-share | Samba's passdb `file-share/samba-private/smbpasswd`, one account per LLDAP user | the same path | in place, said by the script with the account names: every family member's and admin's account keeps its password (#296); one whose LLDAP user is in neither group goes at the first start. The POSIX side is made again on every start, and from then on each person sets a new password in Haus (`/netzlaufwerk`), an admin resets one there |
| radicale | `radicale/data`, owned by the image's radicale user (2999) as ServiceBay's default user namespace maps it (a subuid) | the same path; Laib's unit maps the box's user onto 2999 | in place, handed to the box's user: otherwise Radicale cannot write its collections and restarts forever (#311). ServiceBay's image takes them back on its next start |
| werkstatt | `claude-dev/workspace`, `pi-web/workspace` | `werkstatt/workspace` | the git checkouts copied, owned by the Werkstatt's `dev` (1000); pi-web's as `pi-web-<name>` where claude-dev has the same one |
| immich | the libraries ServiceBay's disk import made: `file-share/data/<user>/photos` (`/mnt/photos/<user>/photos`), `file-share/data/photos` (Shared, `/mnt/photos/photos`) | `file-share/data/photos/<user>`, `file-share/data/photos/shared` (`/mnt/photos/<user>`, `/mnt/photos/shared`) | **moved**, and Immich's paths rewritten (below): albums, faces, favourites, descriptions and edits stay with their assets |
| file-share | Syncthing folders on those paths | the same folders on the new paths | `config.xml` rewritten with the photos, folder and device IDs kept: the devices keep syncing, nothing is re-paired |

Everything else the built-ins mount sits at ServiceBay's paths already (auth,
adguard, the Home Assistant config and the Z-Wave store, immich upload and
pgdata, paperless, media, mosquitto, beets, esphome, llama) and is
adopted in place.

Jellyfin's LDAP login (#256) is adopted too: the plugin is installed on the
box already; media's post-deploy rewrites its `LDAP-Auth.xml` to bind as the
read-only `jellyfin` account (ServiceBay bound as the LLDAP admin), lets
members of `family` and `admins` in, maps `admins` to administrators, and
keeps the libraries ServiceBay's configuration gave new users. The users
Jellyfin made from LDAP logins stay. `laib add media BASE_DN=…` comes with
the box's base DN from the printed line.

**Immich's external libraries (#263, the owner's decision on #229).**
ServiceBay mounted all of `file-share/data` at `/mnt/photos`; the built-in
mounts only `file-share/data/photos` there. The photos move into that
layout, one folder per library, so no library's import path holds another
library's photos (a Shared library on `/mnt/photos` would import every
user's photos a second time). It runs after file-share (its Syncthing config
is in Laib's folder by then) and before `laib add immich`, with Immich and
both Syncthings stopped:

```sh
systemctl --user stop file-share-syncthing.service      # Laib's, from the file-share step
scripts/move-servicebay-data.sh --dry-run immich        # per library: asset paths, checksums, asset files; what follows the IDs
scripts/move-servicebay-data.sh immich
systemctl --user start file-share-syncthing.service
```

- **The photos are renamed**, on the same disk, not copied: the library is
  the biggest data on the box. A folder on another filesystem is refused.
- **Immich's database is rewritten in place.** Immich has no API that
  moves an asset, and its own `immich-admin change-media-location` swaps one
  prefix in every table (it is meant for the upload folder), so the wrapper
  starts Immich's Postgres image on `immich/pgdata` (never beside a running
  unit or container that has it open), takes a `pg_dump` with Postgres's
  own `pg_dump` to `immich/before-library-move.dump`, and the mover rewrites
  in **one transaction**: the asset paths of each library, their files
  (XMP sidecars), the path checksums Immich computes for an external asset
  (`sha1("path:" + path)`, recomputed for the new path) and the libraries'
  import paths. Each UPDATE must hit exactly the rows the dry run counted,
  or the transaction is rolled back. Asset IDs do not change, so albums,
  people, favourites, descriptions and edits, which hang on the ID, stay.
  A path of those libraries in a table it does not rewrite (person
  thumbnails, profile images, the storage template's move history, another
  library's assets) or an asset already at a new path is refused.
- **Syncthing follows.** Folders at or below a moved path get the new path
  in `file-share/syncthing-config/config.xml` (ServiceBay's version kept as
  `config.xml.servicebay`); the folder marker moves with the folder, IDs
  stay. A folder that contains a moved path is refused (its devices would
  delete those photos), as is one that would contain a moved library
  afterwards (its devices would get photos that are not theirs).
- **Cut off anywhere**, the next run finishes it from the journal
  `immich/library-move.json`; a finished run does nothing. The dump is
  kept.
- **Proof:** boot.yml `library-move` runs it on Immich v3.3.1 and its
  Postgres (the Kraftwerk's images), with two Syncthings. One of them stands
  in for a phone. Immich's API shows the same asset IDs at the new paths
  with albums, favourites, sidecar descriptions, a named person and an
  edit. A rescan adds nothing and takes nothing offline. A photo the phone
  takes afterwards lands in `photos/<user>/` and in Immich, and the dump
  restores. A description typed into Immich for a photo of a read-only
  library does not survive Immich itself (v3.3.1 fails to write the
  sidecar, then clears the description on re-reading the metadata). That
  happens on ServiceBay as well, so the descriptions that last are the
  sidecars' and the files'.

**Values and images.** `scripts/migrate-servicebay.py` prints one
`laib add <name> …` line per built-in. It carries the box's values under
Laib's names: `BASE_DN` from `LLDAP_BASE_DN` (ServiceBay derived a blank one
from the domain), `MQTT_USER` from `MQTT_USERNAME` (the box's is `mqtt`), and
`SHARE_USER`. **The migration never downgrades a database.** If the box runs
another tag than the built-in's default for a container Laib does not update
itself (the ones that keep a database), the line pins the digest the box
runs: `LLDAP_IMAGE=$(podman image inspect --format '{{index .RepoDigests 0}}'
docker.io/lldap/lldap:latest) && laib add auth … LLDAP_IMAGE=$LLDAP_IMAGE`, so
an image the box no longer has, or one without a registry digest, stops the
line instead of adding the service with a blank image. This covers LLDAP `:latest` against
`:stable`, Paperless `:beta` against `:latest` and Authelia `:latest` against
`:4`. Moving to the built-in's tag later is a deliberate update, a values
change with its snapshot.

**Open, the operator's decision (#229).** Until it is made, the default
applies:

- **Home Assistant's Authelia login** (`auth_oidc`, client `homeassistant`):
  decided, kept (#229, #253). The built-in declares the same client;
  pre-start installs the same pinned component and moves ServiceBay's
  top-level `auth_oidc:` block out of `configuration.yaml` into
  `packages/laib_auth_oidc.yaml`, with Laib's secret. The users it linked
  stay linked. Nothing to carry.
- **The Werkstatt's sessions and SSH host keys.** ServiceBay kept them in
  claude-dev's `/workspace` (its home; host keys in `.ssh/host_keys`); the
  Werkstatt keeps its home in `werkstatt/home` and its host keys in
  `werkstatt/ssh-host-keys` (#241). Copying claude-dev's into them is not
  in `move-servicebay-data.py` yet. Until it is: sessions start fresh, and
  the host key changes once (the app asks to trust it again).

## What does not move

- The 28 JSON stores, the twin, the approval and audit stores. The journal
  starts empty on day one; the old audit log stays in the ServiceBay data dir
  for 30 days.
- ServiceBay API tokens. All agents get new hand tokens.
- The TUI, the portal, the companion app's `/napi` (Haus owns it now; until haus#10 the rest goes through Haus's Solaris bridge).

## Secrets that carry over (#228)

The data on the box was made with these values. Laib must start with the same
ones, or the app is locked out of its own database or config:

| Laib secret | ServiceBay name | What a new value breaks |
|---|---|---|
| `LLDAP_ADMIN_PASSWORD` | same | LLDAP reads it only when it creates the DB: Authelia's bind, the auth post-deploy, Radicale's seeding |
| `AUTHELIA_STORAGE_KEY` | `AUTHELIA_STORAGE_ENCRYPTION_KEY` | Authelia refuses its `db.sqlite3`; every TOTP/WebAuthn registration is lost |
| `IMMICH_DB_PASSWORD` | `DB_PASSWORD` | immich-server cannot reach its Postgres |
| `IMMICH_ADMIN_PASSWORD` | same | post-deploy logs in as Immich's admin once, to mint the key it sets the family login with (#254); ServiceBay kept the admin on this password (its #1928) |
| `PAPERLESS_DB_PASSWORD` | same | the same for Paperless |
| `MQTT_PASSWORD` | same | every device and Home Assistant lose the broker |
| `JELLYFIN_ADMIN_PASSWORD` | same | the media post-deploy cannot log in to Jellyfin |
| `ADGUARD_ADMIN_PASSWORD` | same | post-deploy cannot sync the rewrites into the kept `AdGuardHome.yaml` |
| `SAMBA_PASSWORD` | `SHARE_PASSWORD` | devices that mount the share with the old account |

They are set **before the service's first apply**, which is `laib add`:
`secrets.age` is one flat map, and every apply generates whatever secret a
service in the state is missing, so a value not carried by then is a new one.
Carrying all of them at step 1 is safe; the services are not in the state yet.

```sh
# on the box, as core, while ServiceBay's container still runs
scripts/carry-servicebay-secrets.sh    # all of them, or name services (… auth immich); LAIB=/var/opt/laib/laib if laib is not on PATH
```

The values live sealed in ServiceBay's `config.json` (`installedSecrets`,
AES-256-GCM with `secret.key` beside it). The host has no Python and openssl
has no GCM, so the script unseals each value inside ServiceBay's container
with its node and pipes it straight into `laib secret set <Laib name>`. No
value is passed on argv, kept in a variable or file, or shown, not even in
an error. A value the store does not have, one it cannot unseal (another
`secret.key`) and one of ServiceBay's redaction masks (`<redacted>`,
`__REDACTED__`, which once landed in its store as values) are reported by
name and the script exits non-zero; so does a service the table does not
list, a typo included. Read it before the apply; it does not set a
substitute. A rerun sets the same values again. The store holds what
ServiceBay deployed. A password changed later inside the app (in LLDAP's UI,
say) is not in it, and the first apply's verify shows that.

One value goes with them: the box's broker user is `mqtt` and the built-in's
default is `homeassistant`, so `laib add mosquitto MQTT_USER=mqtt`. And Immich's
admin: `laib add immich ADMIN_EMAIL=<the admin's address>` (Immich →
Administration → Users; ServiceBay seeded it from `IMMICH_ADMIN_EMAIL`, or
`OPERATOR_EMAIL` when that was blank).

**Not carried: OIDC client secrets.** Laib generates its own
(`OIDC_<CLIENT>_SECRET`) and every app that keeps a copy is handed it on each
apply: Immich's post-deploy writes it into Immich's settings (#254), so the
secret ServiceBay left in Immich's database is overwritten, not migrated.

## Rollback during the weekend

Every `laib apply` snapshots first. Handing a service back to ServiceBay is
`laib remove <name>`, then its files back from `~/servicebay-units/` into the
Quadlet root, `systemctl --user daemon-reload`, `systemctl --user start
<name>.service`. ServiceBay's data paths were only copied from, so they are as
it left them; for home-assistant, first put the two `<file>.servicebay` files
back (zwave-js `settings.json`, `.storage/core.config_entries`). For immich,
the photos moved: rename each `file-share/data/photos/<user>` back to
`file-share/data/<user>/photos` and `file-share/data/photos/shared` to
`file-share/data/photos` (via a temporary name), restore the dump into the
stopped database (`pg_restore --clean --if-exists -d immich
immich/before-library-move.dump` in Immich's Postgres image), put
`config.xml.servicebay` back, and delete `immich/library-move.json`. NPM comes back
the same way until step 7. After step 7,
rollback is a restore of the ServiceBay data dir, which is why it stays 30
days.
