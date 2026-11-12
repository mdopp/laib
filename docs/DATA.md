# Whose data lives where, and who can reach it

Read this when someone asks whether one person's photos, documents,
contacts, places or messages are kept apart from another's; when you add
or change a template or a Homes page that keeps the family's data, gives
one service a key to another's data, or shows one person's data to
someone else; and before you answer "who can see this?" from memory.

Verified 2026-11-10 against laib `73b7003`, homes `9cf6f69`, archiv
`368ccf2` and homes-android `5197f1b`; gap 6 (who owns a new document)
against laib, homes and dokumente as changed by the PRs that closed it. Every claim names the file it was
read from. What only the box can answer carries its read-only probe and
the date the box last answered it (`werkstatt-docprobe`, #1169); what only
a person can answer in an app's settings names its Box-Aufgabe (section
"What a person answers" at the end). The
design is what the code says; what runs on the box today is in its own
section at the end ("On the box, 2026-11-10"), from `laib status`.

## The general shape

**People.** A person is an LLDAP user in the group `family` or `admins`
(templates/auth). `laib person invite <user> --group family|admins` makes
the user without a password and Authelia mails the set-password link;
only a human invites an admin (cmd/laib/person.go `cmdPersonInvite`,
cmd/laib/person360_test.go `TestOnlyAHumanInvitesAnAdmin`). A person's
messenger numbers are box.yml `people`, changed only by a proposal
(docs/STATE.md, "people").

**How a person's account in a service comes to exist.** One of three
shapes, the order in werkstatt/guide/templates.md "Who logs in, and how":

1. *OIDC with auto-register*: the app makes the person's own account at
   their first login through Authelia (Immich, Paperless, Reitti,
   Audiobookshelf, Home Assistant, Vaultwarden). Until a person has
   logged in once, they have no account there, and a service's status
   line counts only those who have.
2. *LDAP bind*: the app checks the password against LLDAP with a
   read-only account and keys everything by the login name (Radicale,
   Jellyfin, Samba).
3. *Forward-auth*: Authelia logs in, Caddy passes `Remote-User` (and the
   route's proxy secret, ADR 0012 §3) and the app trusts it (Homes, File
   Browser, the archive).

**Who can see past the apps.** Three limits hold everywhere and are not
repeated per service:

- **Root on the box reads every file of every service that is not
  encrypted with a person's own secret** (ADR 0032, Consequences; the
  security plan's „Was unmöglich bleibt“). That is everything below
  except a closed archive vault and Vaultwarden's vault contents.
- **Agents** (the Operator, Claude, pi, the Bote) look into a service's
  containers and files only as ADR 0032 allows: never into `private:
  sealed`, into `private: approval` only for an hour after a human's
  Einblick in the Inbox, freely into a service without `private:`. The
  journal keeps only the size of a private service's output
  (internal/envelope/private.go). That is a guard against reading along,
  not against an admin who means to.
- **Backups** hold the same files (restic over the declared `data:`
  paths, docs/BACKUP.md); whoever holds the backup key reads what root
  reads.

**Machine keys.** Every key one service holds to another is a secret in
`secrets.age` on the box, handed to the unit as a podman secret or
environment value (ADR 0006, docs/STATE.md). Root reads them. Keys an
owner pastes on the box page („Schlüssel hinterlegen“, `owner:` in
laib.yml) belong to whichever account the owner made them in; the
`hilfe:` text says which.

## Overview

| Service | Accounts | One person sees another's? | Admin / root | `private:` |
|---|---|---|---|---|
| immich | per person, OIDC auto-register | no, unless shared in Immich | Immich admin: no photos; root: all files | approval |
| paperless | per person, OIDC auto-register | only the household's (no owner: what Dokumente's sorting or the person gave the household); a new document is its person's or Dokumente's first (gap 6) | superuser and `dokumente`: all | approval |
| radicale | per person, LDAP | household books shared; a person's own books no | `homes` service account writes into every person's `privat-*`/`handy-kontakte` | approval |
| reitti | per person, OIDC auto-register | no | Reitti admin role given to nobody; root: all | approval |
| file-share | per person (Samba, File Browser) | yes: one shared tree | root: all | approval |
| vaultwarden | per person, SSO | no (vaults are encrypted per person) | admin page: accounts, not contents | approval |
| archiv | one instance per person | no | root: nothing while the vault is closed (with listed exceptions), everything while open | sealed |
| homes | per person, forward-auth | by design for the household parts; Paperless as its read-only user `homes` sees it; see flows | root: all of `${DATA_DIR}/homes` | none |
| bote | per messenger number | **yes: one shared memory** | root: all | none |
| scan (Dokumente) | none of its own | files into Paperless as Dokumente's (tag Eingang) until its sorting gives it to a person or the household | root: all | none |
| home-assistant | per person, OIDC | yes: one house | admins group are HA admins | none |
| media, audiobookshelf, music-assistant | per person (LDAP / OIDC / via HA) | no, per-person progress | admin keys read every person's progress | none |
| usage-metrics | none | counts, no who; unanswered sentences 30 days | root: all | none |
| kalender | none of its own (service account) | mirrors into the person's own read-only books | root: the subscription addresses | none |

## Per service

### immich

`private: approval` (templates/immich/laib.yml).

- **Accounts:** one per person. Immich logs in through Authelia's OIDC
  with `autoRegister: True` (templates/immich/post-deploy, `WANT`), so a
  person's own account is made at their first „Mit Familienkonto
  anmelden“. The admin is a separate local account (`ADMIN_EMAIL`,
  `IMMICH_ADMIN_PASSWORD`, signed up by post-deploy; on the recovery
  sheet).
- **Separation:** Immich keeps each user's uploads, persons and faces
  apart; a person sees another's photos only through Immich's own
  sharing (shared albums, partner sharing), which the person sets up.
  The file share's `photos/` folder is mounted read-only into Immich
  (`PHOTOS_RO`, templates/immich/immich.container); which Immich user
  sees it depends on the external library an admin creates in Immich (no
  template code creates one): Box-Aufgabe Box-Aufgabe #27. The Immich admin manages users
  but does not see their photos in the app; root reads
  `${DATA_DIR}/immich/upload` directly.
- **Keys held to it:**
  - `IMMICH_API_KEY` — the admin's, minted by post-deploy with
    `systemConfig.read` and `systemConfig.update` only; Laib uses it to set
    the OIDC block on every apply. No photo rights.
  - `HOMES_IMMICH_KEY` — **the owner's own account**, pasted on the box
    page; rights `person.read/create/update/merge`, `user.read`,
    `asset.read`, `asset.view`, `asset.upload`, `face.read`,
    `asset.delete`, `album.create`, `album.read`, `albumAsset.create`
    (+ optional `person.statistics`) — templates/homes/laib.yml,
    homes internal/immich/immich.go. Homes therefore works on the owner's
    Immich only; see Homes and the flows below.
  - The archive: each person's own key, inside their vault (archiv
    apps/archiv/immich.go `umfeldRechte`: `asset.read`, `asset.view`,
    `person.read`; stored at `immich-schluessel` inside the vault,
    apps/archiv/umfeld.go). The code only reads (`/api/users/me`,
    `/api/api-keys/me`, `/api/search/metadata`);
    templates/archiv/archiv.container gives it `IMMICH_URL`.
  - Reitti: each person enters their own key in Reitti's settings
    (templates/reitti/README.md, "Immich").
- **Public:** immich-public-proxy serves only what a person shared as a
  link, with no key and no login of its own
  (templates/immich-public-proxy/laib.yml).

### paperless

`private: approval` (templates/paperless/laib.yml).

- **Accounts:** one per person, made at the first OIDC login
  (`PAPERLESS_SOCIAL_AUTO_SIGNUP=true`), its LLDAP groups synced to
  Paperless' groups `admins` and `family` with their permissions
  (templates/paperless/paperless.container, paperless-mail.py `GROUPS`).
  The local admin (`PAPERLESS_ADMIN_USER`) is break-glass and a
  superuser. The service user `dokumente` (no administrator) belongs to
  the scan service (ADR 0028); the service user `homes` (read only, no
  group) to Homes.
- **Separation:** Paperless separates by a document's *owner*. A
  document without an owner is seen by everyone with `view_document`,
  which the `family` group has: that is the household's. **No way in
  gives no owner any more** (gap 6, closed; owner 2026-11-10: „Was du
  scannst oder hochlädst, gehört von Anfang an dir. Geteilt wird nur, was
  die Sortierung als Haushaltsdokument erkennt oder was du teilst.“):
  - the consume folder (the share's `paperless-consume`: scans, the app's
    and Homes' page uploads, the Bote's „ablegen“, the share): Paperless'
    workflow „Eingang“ (Consumption Started) makes it Dokumente's with the
    tag `Eingang` before it is stored (paperless-mail.py `eingang`). Homes
    (`von-<login>/`, homes internal/napi/upload.go) and the Bote
    (`Bote/von-<login>/`, templates/bote/notify.py `folder_of`) file a
    known sender's under that folder, whose tag tells Dokumente whose it is;
  - the household's mail address: the rule's owner is `dokumente`,
    `assign_owner_from_rule`, tag `Eingang`;
  - each person's own address `<login>+doc@<domain>` (and
    `<login>+doc+<anything>@`): a rule per person with a Paperless account,
    owned by them, `assign_owner_from_rule` (paperless-mail.py
    `personen_regeln`).
  Dokumente's sorting (mdopp/dokumente `ordnen/zuteilen.py`) then gives
  each `Eingang` document once: a `von-<login>` one to that person at
  once; what `ordnen.haushaltsdokument` finds is the household's
  (conservative: only the Bereiche Haus, Auto, Versicherung, Steuer, the
  model sure, nothing medical) gets the tag `Haushalt`, and Paperless'
  workflow „Für den Haushalt“ removes its owner and every extra right;
  else the addressee the model read; nobody found: it stays Dokumente's and
  Homes asks the admins (flow 21). A person shares their own document with
  the family by the tag `Haushalt` (or Paperless' own sharing). A document a
  person owns is seen by its owner, the superuser and the clients below,
  not by the rest of the family. `EINGANG_SORTIERT: "false"` (a box without
  the scan service) turns the „Eingang“ part off and files without an
  owner, as before.
- **Keys held to it:**
  - `PAPERLESS_TOKEN` — user `dokumente`, held by the scan service. Its
    rights (`DOKUMENTE_RECHTE`) include view and change of documents;
    `besitz()` in paperless-mail.py grants it view and change on **every
    document a person owns**, and the workflow „Dokument hinzugefügt“
    grants it the same on every new document. So the scan service reads
    private documents too, by design (it reads contracts and invoices).
    Since gap 6 it also *owns* every new document from the consume folder
    and the household's address until its sorting gives it away, and it
    decides whose: a household document goes to everybody by its decision
    (a conservative rule, ordnen.py `haushaltsdokument`).
  - `HOMES_PAPERLESS_TOKEN` — user `homes`, minted by post-deploy
    (paperless-mail.py `HAUSHALT`): view rights only, no group, not in
    the workflow, so it sees the documents without an owner and those
    shared with it, never a person's „nur für mich“, never a new document
    before Dokumente's sorting gave it to the household (gap 6). Homes mirrors what
    it sees into `${DATA_DIR}/homes-dokumente` for its memory library
    (templates/homes/homes-documents-export.service). See Homes.
  - `PAPERLESS_MAIL_PASSWORD` — the household's document mailbox, not a
    person's. The same login may also be given to a person's archive
    (`DOKUMENTE_MAIL_PASSWORD_<PERSON>`, set by the owner per person) to
    read that person's `+doc` mail (flow 24).
- **The mail behind a document (archiv#34):** a document filed from a
  person's own address gets one Paperless note „Die Mail dazu im Archiv:
  https://archiv.<domain>/tresor/mail?id=<Message-ID>“
  (paperless-quittung.py `notiz`, as the admin; `ARCHIV_URL`, empty: no
  note). The link carries the Message-ID only; it opens the mail only in
  the person's own open archive.

### radicale

`private: approval` (templates/radicale/laib.yml). Rights are rendered by
templates/radicale/pre-start into `/config/rights`; the first matching
section decides.

- **Accounts:** every LLDAP user logs in with their own password (LDAP
  bind as the read-only `radicale` account). Service accounts, seeded by
  templates/auth: `homes` (the household's), `kalender`, `homeassistant`,
  `archiv-<person>`.
- **Books and who reaches them:**

| Collection | Owner | Read | Write |
|---|---|---|---|
| `homes/haushalt`, `homes/haushalt-kontakte`, `homes/familie-*` | `homes` | every user | every user (`[haushalt]`: entries, not the collection) |
| `homes/abo-*` | `homes` | every user (`[abo-family]`) | `kalender` |
| `<person>/…` (own calendars, books, lists) | the person (`[owner]`) | the person | the person |
| `<person>/privat-*` („nur für mich“) | the person | the person, `homes`, `archiv-<person>` | the person, `homes` (`[homes-privat]`: `Rrw`) |
| `<person>/handy-kontakte` (the phone's contacts) | the person | the person, `homes`, `archiv-<person>` | the person, `homes` (Homes writes it from the app) |
| `<person>/google-*`, `<person>/abo-*` | the person | the person, `homes` | `kalender` only |
| `<person>/google-kontakte` (a Takeout) | the person | the person | the person |
| `homeassistant/…` | `homeassistant` | `homeassistant`, `kalender` | `kalender` (copies of the household's calendars) |

- **Separation:** another family member cannot read a person's own
  collections; every user can *list* the root (`[root]: R`). The Homes
  service account reads and writes each person's `privat-*` and
  `handy-kontakte` and reads their mirrors — whoever runs Homes's code
  or holds `HOMES_RADICALE_PASSWORD` reaches them.
- **Keys held to it:** `HOMES_RADICALE_PASSWORD` (`homes`),
  `KALENDER_RADICALE_PASSWORD` (`kalender`), `HA_RADICALE_PASSWORD`
  (`homeassistant`), `ARCHIV_RADICALE_PASSWORD` (`archiv-<person>`, one
  per instance, read-only). Bind accounts: `RADICALE_LDAP_PASSWORD`.
- The archive cannot read `homes/haushalt-kontakte` today
  (`[archiv-sonst-nichts]`); archiv's README notes the 403.

### kalender

No `private:` (templates/kalender/laib.yml). No accounts of its own: a
timer mirrors outside calendars (a Google calendar's secret iCal address,
a public ICS, entered in Homes) into `<person>/google-<name>` or
`<person>/abo-<name>` as Radicale's `kalender` account, and copies the
household's calendars into Home Assistant's own Radicale home. The
addresses are secrets (`KALENDER_ABOS`, one secret for every person's
subscriptions). Its data folder holds only the status and the last run.

### reitti

`private: approval` (templates/reitti/laib.yml).

- **Accounts:** one per person, OIDC with sign-up on, local logins off;
  Reitti's seeded `admin` has no password and its admin role is given to
  nobody (templates/reitti/README.md, "Logins";
  templates/reitti/reitti.container `OIDC_SIGN_UP_ENABLED`,
  `DISABLE_LOCAL_LOGIN`).
- **Separation:** each person sees only their own timeline (README).
  Upstream Reitti lets a person share their live position, and with a
  magic link optionally their recent track, with other users of the
  instance or with anyone holding the link — by that person's own choice
  (github.com/dedicatedcode/reitti README, read 2026-11-11); it names no
  way to share the past timeline.
- **Keys held to it:** Homes forwards the app's location points to the
  person's Reitti — see Homes. Each person's Immich key lives in their
  Reitti settings, in Reitti's database.

### file-share

`private: approval` (templates/file-share/laib.yml).

- **Accounts:** Samba has one account per family or admins member
  (`samba-members.py`, from LLDAP), its password set by the person in
  Homes; a shared fallback account (`SAMBA_PASSWORD`) is on devices.
  File Browser takes the user from Authelia's `Remote-User` and makes
  the account at the first visit; `FILEBROWSER_ADMIN_USER` is its admin
  (templates/file-share/file-share-filebrowser.container). Syncthing's
  GUI is for `admins` only.
- **Separation: none inside the share.** The Samba share `data` is one
  tree, `read only=no; force user=root` for every member
  (file-share-samba.container); File Browser is given no per-user scope
  by the template, so every account sees the whole tree (upstream's
  default scope; a family member's scope on the box: Box-Aufgabe
  Box-Aufgabe #26). Notes, documents, photos,
  music and the Paperless inbox all live there. Only the `werkstatt`
  share is limited, to `@admins`.
- **Shares out:** a File Browser share link works from anywhere
  (`publicPaths`), with its own password and expiry, made by whoever
  shares.
- **A person's task (#1168):** `file-share-check.py` asks, per member
  whose row in samba-accounts' status has no time, for that person's
  Box-Aufgabe „Netzlaufwerk-Passwort festlegen“ (`fuer: person:<login>`,
  `weg: /netzlaufwerk`, `fertig_wenn: status netzlaufwerk.<login>`); the
  Operator's look adds it and diag closes it once the row has a time.
  **Who sees what:** the person sees one password task in Homes (Homes'
  own, from the same status file, shown at once; Laib's stands behind it
  for the Bote and closes itself, homes internal/pages/einrichtung.go
  `mitLaibs`); the admins
  see in diag and the journal that a person's task exists („Aufgabe #N
  für die Person“, „für eine Person“), no name beyond the status line's
  count; Laib's whole hands (the owner at the shell, claude, the
  Operator) read the login in the finding's topic and in aufgaben.yml.

### vaultwarden

`private: approval` (templates/vaultwarden/laib.yml).

- **Accounts:** one per person, SSO through Authelia
  (`SSO_ENABLED=true`, `SSO_ONLY=false`), open sign-up off by default
  (`SIGNUPS: "false"`, values.example.yml).
- **Separation:** Bitwarden vaults are encrypted on the client with the
  person's master password; the server, root and the admin page
  (`VAULTWARDEN_ADMIN_TOKEN`) see accounts and metadata, not the
  passwords. Sharing goes through Vaultwarden organisations, which a
  person sets up.
- **Keys held to it:** none.
- **A person's task (#1168):** `vaultwarden-check.py` reads, besides the
  admin page's accounts, LLDAP's logins and mail addresses with the
  read-only account `vaultwarden` (`VAULTWARDEN_LDAP_PASSWORD`, seeded by
  templates/auth; no password, no group). An enabled account without a
  second factor whose address is a login's asks for that person's
  Box-Aufgabe „Zweiten Faktor für deinen Passwort-Tresor einrichten“
  (`fuer: person:<login>`, `weg: /einrichten/passwoerter`, `fertig_wenn:
  status zweiter-faktor.<login>`); closed once the account has one. The
  status file keeps the login, never the address. **Who sees what:** as
  for file-share's: the person their own task; the admins that one exists;
  Laib's whole hands the login.

### archiv

`private: sealed` (templates/archiv/laib.yml), one instance per person
(`perPerson: true`, ADR 0029).

- **Accounts:** the instance is the person's (`ARCHIV_PERSON`,
  templates/archiv/archiv.container); the route lets in the group
  `family` with two factors, and the instance serves only a request whose
  `Remote-User` is its person, with the proxy secret; another user gets
  403 (archiv apps/archiv/seite.go). The vault is made by the person on
  the page, with their Archiv-Passwort (apps/archiv/seite.go
  `/tresor/anlegen`); a passkey may unlock it (apps/archiv/passkey.go).
- **Separation:** no other person, no admin, no agent through Laib
  (`sealed`). Inside the vault (gocryptfs, apps/archiv/tresor.go): the
  msgvault database, the Briefkasten's private key, the Signal and
  WhatsApp keys, the contacts and calendar copies, the person's Immich
  key, and the settings for deleting old mail at the provider
  (`mail-aufraeumen.json`). **While the vault is open, root reads it**
  (ADR 0029; the page says so, apps/archiv/seite.go). **While closed,
  outside the vault in plaintext:** the IMAP hosts, users and app
  passwords of each mailbox the person entered (root can read the
  person's mailboxes with them), the person's own number, the Briefkasten's
  public key, its HMAC dedupe sets, the status files, the last counts for
  Homes (`aufgaben.json`, below) (ADR 0029 amendment 2026-11-01;
  apps/archiv/briefkasten.go). Briefkasten packets
  are age-encrypted to the person's key.
- **Umfeld (archiv#27):** the contact net, the answers to „Wie steht X
  zu Y?“ and who is hidden live only in the vault
  (`umfeld/netz.json`, `umfeld/antworten.json`, `umfeld/verborgen.json`;
  apps/archiv/umfeld.go, fragen.go). An answer leaves only by the
  person's tap on it in their own open archive (owner 2026-11-10: the tap
  is their consent): the archive posts an HMAC-signed packet to Homes'
  own port on the box (`ARCHIV_HOMES_INTERN`,
  `http://host.containers.internal:8650/intern/uebernahme`), server to
  server, never through a browser or Caddy, and Homes writes it at once;
  Rückgängig takes it back the same way (apps/archiv/uebernahme.go,
  fragen_seite.go; flow 20).
  The net also reads the person's own msgvault in the same open vault
  (`POST /api/v1/query`, read only): group chats and mail threads become
  ties between participants matched to the phone books' cards;
  `netz.json` keeps only those card keys, sizes, counts, the last time
  and a chat's title, no number, address or subject (apps/archiv/
  gespraeche.go, archiv `6ae89f3`). Derived relations (abgeleitet.go)
  are questions only, never written.
  The person's categories of group chats (Familie, Arbeit, …) stay in the
  vault (`umfeld/gruppen.json`) and never reach an address book
  (apps/archiv/gruppen.go).
  A Facebook or LinkedIn export the person shares to the Homes app
  („Ins Archiv“, owner 2026-11-11) reaches the inbox only as its friends
  list or `Connections.csv`, age-encrypted on the phone to the person's
  key (`<person>/sozial/*.age`; Homes stores ciphertext, never reads it);
  opened, the archive keeps the names (LinkedIn: e-mail if shared,
  company, position, date) in the vault (`umfeld/sozial.json`) as reasons
  and names for „+ Beziehung“, never as a contact or an address-book line
  (archiv apps/archiv/sozial.go, archiv#36, homes#341,
  homes-android#272). Mail cannot bring them: both platforms mail only a
  link that needs their login.
- **Message excerpts to the box's model (archiv#27, owner 2026-11-11;
  ADR 0029 amendment 2026-11-11):** only while the vault is open, a
  message with a relation word whose sender and recipients the phone
  books know goes, with their names, at most 600 characters of its text
  and what the archive knows of them, to the household model through
  laib-lease's proxy (`MODEL_URL`, `MODEL_KEY`, `MODEL_LEASE` in
  templates/archiv/archiv.container), at most ten a round, none while a
  lease holds the card. The answer is kept per message in the vault
  (`umfeld/hinweise.json`), so nothing is sent twice. The archive logs
  only counts (apps/archiv/hinweise.go). laib-lease does not log bodies,
  and llama-server runs without `--verbose` (templates/llama), so the
  prompt is not kept. On the box the running llama-server has no flag
  that writes prompts (`--verbose`, `--log-file`, `--slot-save-path`,
  `--log-verbosity`)
  <!-- box: laib look llama --container systemd-llama -- ps -eo args ~ /app/llama-server --host !~ \s(--verbose|-v|--log-file|--slot-save-path|--log-verbosity|-lv)(\s|$) --> *(box 2026-11-12: yes)*;
  what laib-lease keeps is the code's claim above. The model process is the household's, so root reads its
  memory while it answers, as it reads an open vault (flow 22).
- **Inbox `archiv-eingang/<person>/`:** Homes writes it as the device's
  person; each instance mounts only its own folder. Signal and WhatsApp
  databases arrive encrypted by the messenger, SMS age-encrypted on the
  phone; **WhatsApp `Media/` arrives in plaintext** and stays so until the
  Briefkasten encrypts it — indefinitely if the person has no vault yet
  (apps/archiv/briefkasten.go). Not in the backup (`backup: false`).
- **Mail (archiv#34, owner 2026-11-11):** the person's own mailboxes,
  several, fetched hourly with `EXAMINE`/`BODY.PEEK`. Where the owner set
  it for this person, also the household's document mailbox (the one
  Paperless reads), but only mail addressed to `<person>+doc@<domain>` or
  `+doc+…@`, checked by the archive on the full address (IMAP's TO search
  is a substring: `hanna+doc` holds `anna+doc`), read-only, never deleted
  there (flow 24). **Deleting at the provider** is the person's opt-in per
  own mailbox (off by default): only mail older than their limit, not
  flagged, not a draft, and proven in their msgvault by Message-ID plus the
  SHA-256 of its raw bytes; first a count the person confirms, then
  monthly, only while the vault is open (it needs msgvault for the proof);
  this alone uses `SELECT`, `\Deleted` + `UID EXPUNGE`, on Gmail a `MOVE`
  to its Trash (archiv apps/archiv/aufraeumen.go). Irreversible at the
  provider; the archive keeps the mail.
- **Counts for Homes (homes#362, owner 2026-11-12: „nur eine Zahl“):**
  how many things wait for the person in their archive, never what they
  are. Homes asks `GET /intern/aufgaben` with that person's
  `ARCHIV_HOMES_SECRET` and gets `{offen, am, fragen, aufraeumen,
  schluessel_fehlt}`: the number of open questions and suggestions on the
  workplace („Wie steht … zu …?“, „Kennen sich …?“, a group's name, what
  was recognised), the number of the Datenpfleger's findings (archiv#58),
  whether a key the archive waits for is missing (Signal, WhatsApp,
  Immich), and when this was counted. No name, no kind, no text. Counted
  only while the vault is open; the last count stays outside it in
  `/data/aufgaben.json` (mode 0600, root can read it, as the status
  files), so Homes can show it while the vault is closed (`offen: false`).
  Not in `laib-status.json`, which reaches `laib_status` and the admins.
  Homes shows it only to the archive's own person, on „Deine Aufgaben“
  and Start (archiv apps/archiv/aufgaben.go, intern.go; flow 27).
- **Keys it holds:** the person's Immich key (in the vault, read only),
  the box's model key (`MODEL_KEY`, the household's, for the model's
  proxy), `archiv-<person>` in Radicale (read only), the person's IMAP app
  passwords (outside the vault; `EXAMINE` for the fetch, `SELECT` only for
  the deletion the person allowed), and, where the owner set
  `DOKUMENTE_MAIL_PASSWORD_<PERSON>`, the household's document mailbox's
  login (a file in the container, `/run/secrets/dokumente-mail-password`;
  it reads the whole mailbox, every person's document mail: the archive's
  address check keeps the others out, not the provider; a read-only login
  of the provider, where one exists, is the narrower key). **Keys others hold to
  it:** `ARCHIV_HOMES_SECRET` (Homes, per instance: `/intern/` for a new
  phone's restore, WhatsApp media only while open, the counts above; the archive also signs
  with it the answers it posts to Homes' `/intern/uebernahme`, which Homes
  checks with the same secret); the msgvault key on
  `/p/<person>/` only where the person set `AGENT_PATH` (off by default,
  templates/archiv/values.example.yml).

### homes

No `private:` (templates/homes/laib.yml). Homes is the family's pages
and the app's API (`/napi/`); references below are to mdopp/homes.

- **Accounts:** none of its own. A page request is a person through
  Authelia's `Remote-User`, `Remote-Name`, `Remote-Email`,
  `Remote-Groups`, trusted only with the proxy secret
  (internal/pages/proxy.go). Admin is `Remote-Groups` containing `admins`
  (internal/pages/start.go). A phone is a device token minted when a
  signed-in person pairs it; Homes keeps only its sha256 with that
  person's login (internal/devices/devices.go), and every person-scoped
  `/napi/` route takes the person from the token, never from the request
  (internal/napi/kontakte.go, sicherung.go, standort.go,
  korrekturen.go). The app stores the token in Android's encrypted
  preferences (homes-android data/TokenStore.kt).
- **The owner, in Homes, is whoever owns the Immich key:** the login
  whose `Remote-Email` is the Immich account of `HOMES_IMMICH_KEY`
  (internal/gesichter/gesichter.go `Merke`). Owner-only pages are gated by
  `privatEigen` (internal/pages/kontakte_privat.go): „Fotos“ on Meine
  Kontakte, „Dieselbe Person?“ (kontakte_dieselbe.go), kontakt_offen.go,
  a contact's „Beziehungen“ (kontakte_beziehung.go). Who stands close to
  whom is no longer computed in Homes (homes ADR 0002, 2026-11-11): Homes
  does not read the photos for it; Kontakte and a contact's page link to
  the person's own archive („Dein Netzwerk im Archiv ›“), shown only to
  whom has an archive.
- **Self-only:** Meine Kontakte (the viewer's own `handy-kontakte`),
  personal lists, Standort (internal/pages/standort.go), Gesehen
  (internal/pages/gesehen.go: the viewer's own Jellyfin and ABS history
  only).
- **A person's archive status:** Homes reads each person's archive
  status (`laib status archiv-<person>`: times and the archive's own
  failure sentence only, never content) and shows it on „Meine Daten“
  only to that person (homes#350).
- **`${DATA_DIR}/homes` (`HOMES_DATA`, cmd/homes/main.go):**

| File | What | Whose |
|---|---|---|
| `devices.json` | paired phones: token hash, the pairing person | per person |
| `favoriten.json`, `ansicht.json`, `einrichtung.json` | favourites, calendar view, setup ticks, keyed by login | per person |
| `reitti-konten.json` (0600) | each person's own Reitti token | per person, a secret |
| `discogs-konten.json` (0600) | each person's Discogs OAuth token | per person, a secret |
| `sicherung/<person>/…`, `standort-import/<person>/…` | the ledger of a person's phone backups; a Takeout location file while it imports | per person |
| `immich-gesichter.json`, `immich-netz.json`, `immich-netz-antworten.json` | the owner's Immich persons, face JPEGs, the key's Immich e-mail and login, contact↔person links; the relations the phone books (household and the owner's `handy-kontakte`) write, and the owner's answers about them | the owner's Immich and phone, about household contacts |
| `kontakt-antworten.json` (0600) | „Kontakt prüfen lassen“: link hashes, outside contacts' answers, pending phone corrections | the asking person; outside contacts |
| `musikwuensche.json`, `podcasts.json` | wishes and subscriptions, each naming who | household, tagged by person |
| `papierkorb/` | household items deleted, 30 days | household |
| `kontakte-quelle/` | one-time copies of household firm cards | household |
| `zugang.json` | public access requests: name, contact, message, salted address hash | outsiders |

  Beside it: `homes-dokumente` (the Paperless mirror, below),
  `homes-qmd` (its index), the share's `notes` (the household's notes,
  one folder), `paperless-consume` (the app's uploads),
  `archiv-eingang/<person>/` (phone backups, written as the device's
  person, internal/napi/sicherung.go).
- **Keys Homes holds** (templates/homes/laib.yml):
  - `HOMES_IMMICH_KEY` — the owner's Immich account (internal/immich/
    immich.go). Reads people, faces, thumbnails, metadata search; writes
    `POST /api/people`, `PUT /api/people/{id}`, `/api/people/merge`,
    `POST /api/assets` (the owner's own contacts' pictures and the
    household contacts' photos, archived), `DELETE /api/assets` (only
    those), `POST /api/albums` and `PUT /api/albums/{id}/assets` (one
    album, *Homes · Kontaktfotos*, of those photos; row 25).
  - `HOMES_RADICALE_PASSWORD` — Radicale's `homes`: owns the household's
    books; reads and writes every person's `privat-*` and
    `handy-kontakte`, reads their mirrors (radicale above).
  - `HOMES_PAPERLESS_TOKEN` — Paperless' read-only user `homes` (minted
    by paperless' post-deploy); GET only plus Paperless' document chat
    (internal/paperless/paperless.go, vertraege.go). Every family member
    sees what the household shares in Paperless, nothing a person owns and
    nothing still in the Eingang (gap 6).
  - `HOMES_HA_TOKEN` — HA user `homes`, an administrator: states,
    history, cameras, service calls, Assist debug runs (internal/ha/).
  - `HOMES_JELLYFIN_KEY` (an administrator's: every person's watched
    state, read only, internal/jellyfin/gesehen.go),
    `HOMES_JELLYFIN_TOKEN` (hidden user `homes`, music only),
    `HOMES_ABS_TOKEN` (ABS user `homes`, an administrator: every member's
    progress, podcast subscriptions, internal/abs/).
  - `BOTE_API_KEY` (as `HOMES_BOTE_KEY`) — speaks to the Bote as the
    signed-in person (internal/bote/bote.go).
  - `HOMES_ARCHIV_SECRET_<PERSON>` — each archive's `/intern/`
    (internal/napi/sicherung_letzte.go); the same secret checks the
    answers that archive posts to Homes' `POST /intern/uebernahme`
    (internal/uebernahme): only for that person, within two minutes, each
    nonce once, and refused when the request came through Caddy
    (`X-Homes-Proxy`, `X-Forwarded-*`, `Remote-User`).
  - `HOMES_LAIB_TOKEN` — the hand `homes` (status, inbox, restart,
    person_invite …). Any paired phone may ask it to restart a service;
    approving is refused (internal/napi/napi.go).
  - No key to the file share: a Samba password goes through a FIFO.
    `USAGE_METRICS_TOKEN`: counts, never who (internal/usage/usage.go).
  - **Held to Homes:** `HOMES_HAUSHALT_TOKEN`, one bearer for the
    Haushalt MCP (`/mcp/haushalt`), held by the Bote and the scan service
    (internal/haushalt/haushalt.go). It is not per person: whoever talks
    to the Bote reaches every Haushalt tool. Dokumente also posts with it to
    Homes' `POST /intern/zuordnen` (homes cmd/homes/zuordnen.go; Homes' own
    port, refused through Caddy) the scans it could not give anyone: sender,
    date, pages, when, and a `zeichen` over them; the admins' answers come
    back and count only while the `zeichen` holds, so a list posted by
    another holder of the bearer cannot make an admin give away what they
    did not see (flow 21).

### bote

No `private:` (templates/bote/laib.yml).

- **Accounts:** none in an app; who may write is box.yml `people`'s
  messenger ids plus the platform allow-lists (templates/bote/
  values.example.yml). Homes's Sprechen field speaks to the Bote's API
  with `BOTE_API_KEY` and names the person, with
  `X-Hermes-Session-Key: homes:<login>` (templates/bote/README.md).
- **Separation: none in its memory.** Hermes' `memory` and
  `session_search` are on; the Bote runs one profile, so one
  `MEMORY.md`/`USER.md` reaches every later conversation and
  `session_search` finds every person's past sessions in one `state.db`
  (templates/bote/README.md, "Memory (#884)"). Only the SOUL asks the
  model not to retell one person's things to another — a prompt, not a
  guarantee. A profile per person is open on #884. ADR 0017 §3's „per
  number“ memory is therefore not what runs.
- **Data folders:** `${DATA_DIR}/bote/hermes` (config, memory, sessions),
  `bote/signal` (the linked Signal device: it can read every message to
  the Bote's number), `bote/gesendet/freigaben.json` (0600, bote-notify:
  the owner's chat target, the message id and the text of each proposal
  notice, up to 30 days, to mark it once decided, #1183).
- **Keys it holds:** `HOMES_HAUSHALT_TOKEN` (the Haushalt MCP — what that
  exposes is in the Homes section), `BOTE_HA_TOKEN` (HA user `bote`, no
  administrator), `BOTE_AUTOMAT_HA_TOKEN` (an HA administrator, the
  Automat only), `BOTE_LAIB_TOKEN` (the hand `bote`: propose, note,
  escalate), `USAGE_METRICS_TOKEN`.
- **Crossings:** „ablegen“ files a sent document into Paperless as its
  sender's (`Bote/von-<login>/`, Dokumente hands it to them; a stranger's
  under their chat name is Dokumente's until its sorting decides, gap 6); a message for a person (`rest_command.mitteilung`, „sag Mama
  Bescheid“) reaches that person.

### scan

No `private:` (templates/scan/laib.yml). Dokumente (ADR 0028) has no
accounts of its own. A stack from the share's `scan-eingang` or a SANE
scanner goes into Paperless' consume folder, where it is Dokumente's
(tag `Eingang`) until its sorting (`ordnen/zuteilen.py`) gives it to the
household, to its addressee, or, nobody found, leaves it Dokumente's and
asks the admins in Homes (gap 6); a „nur für mich“ stack Dokumente uploads
as `dokumente` and hands to the person (mdopp/dokumente scan/scan.py
`Paperless.geben`, found by checksum). It keeps each
stack's original pages for `SCAN_KEEP_DAYS` in `${DATA_DIR}/scan`
(`backup: false`) and the contract record in `${DATA_DIR}/dokumente`.
Keys: `PAPERLESS_TOKEN` (`dokumente`, sees every document — see
paperless), `HOMES_HAUSHALT_TOKEN` (notice reminders and the address
book), `BOTE_NOTIFY_TOKEN`.

### home-assistant

No `private:` (templates/home-assistant/laib.yml).

- **Accounts:** per person through OIDC (`auth_oidc`,
  templates/home-assistant/pre-start: `automatic_user_linking`,
  `automatic_person_creation`; `admins` → HA admin, `family` → user).
  Service users, each its own (templates/home-assistant/laib.yml
  `secrets:`): `homes` (administrator), `bote` (no administrator),
  `bote-automat` (administrator), `laib-pruefung`, `stimme`,
  `laib-namen` (administrator), `music-assistant` (administrator).
- **Separation:** one house; every user sees the devices and the
  household's calendars (copied by `kalender`). Who switched what goes
  into HA's logbook under the consumer's user, never a person's
  (post-deploy, #591). Location of the family's phones in HA through the
  companion app is per person's choice in that app; which persons'
  locations other users see: Box-Aufgabe Box-Aufgabe #25.

### media

No `private:`. Jellyfin: each person logs in with LLDAP (the
LDAP-Authentication plugin, templates/media/laib.yml); their watched
state is theirs. `HOMES_JELLYFIN_KEY` is an administrator's key: Homes
reads every person's watched films and series with it (templates/media/
post-deploy, haus#152); `HOMES_JELLYFIN_TOKEN` is the hidden user
`homes`, music only. Home Assistant has its own account `homeassistant`.

### audiobookshelf

No `private:`, `oidc:` with auto-register (templates/audiobookshelf/
post-deploy `authOpenIDAutoRegister`). Each person's progress is theirs;
the box's admins are ABS admins. `HOMES_ABS_TOKEN` is ABS user `homes`,
an administrator (podcast subscriptions); `ABS_MA_TOKEN` is
`musicassistant`'s.

### music-assistant

No `private:`. The family signs in through Home Assistant; MA reads HA's
users with an administrator token (`MUSIC_ASSISTANT_HA_TOKEN`). Plays
what Jellyfin and ABS hold; keeps no per-person data the template
declares beyond its own `data/`; whether MA keeps a history per user:
Box-Aufgabe Box-Aufgabe #25.

### usage-metrics

No `private:`. Counts (app, event, day, count) with no who, and
unanswered sentences for 30 days, also without who
(templates/usage-metrics/laib.yml). A sentence can itself carry what a
person asked. Reached only from inside the box with
`USAGE_METRICS_TOKEN` (Homes, the Bote).

### whoami

No `private:`. A test page behind the family login; keeps no data.

## Where data crosses from one person to others

Each line: what moves, from whom to whom, where in the code. Household
data (the Haushalt calendar and address book, `familie-*`) is shared by
design and not listed again.

| # | Flow | From → to | Where |
|---|---|---|---|
| 1 | A face crop from the owner's Immich goes onto a household contact card without a photo; every family phone syncs it. **Owner-approved, 2026-11-10: „Gesichtsausschnitt kann bleiben“.** | owner's Immich → every phone | homes internal/pages/kontakt_sprung.go `KontaktKarte`, `gesichtZeile`; served by `/napi/gemeinsam/kontakte` (internal/napi/gemeinsam.go) |
| 2 | Every household contact becomes a person (name, birthday) in the owner's Immich | household book → owner's Immich | homes internal/gesichter/abgleich.go |
| 3 | A name the owner gives a person in Immich renames the household contact („Automatisch zurück“) | owner's Immich → household book → every phone | homes internal/gesichter/abgleich.go `folgen` (CorrectShared) |
| 4 | The firms in the owner's phone contacts, notes stripped, are the Haushalt tool `handy_firmen` | owner's `handy-kontakte` → the Bote (anyone it talks to), Dokumente | homes internal/haushalt/firmenkontakte.go |
| 5 | Paperless as its read-only user `homes` sees it (the household's documents: no owner; none a person owns, none still in the Eingang): Homes' Dokumente pages, `/napi/portal/documents`, the Bote's `dokumente_suchen` and `notizen_finden`, the qmd mirror — no per-person filter | the household's documents → every family member and the Bote | homes internal/paperless/, internal/dokumente/dokumente.go, internal/haushalt/haushalt.go, internal/napi/tools.go |
| 6 | **Closed (gap 6).** A new document is someone's from the start: the app's, Homes' page's and the Bote's are the sender's (`von-<login>`), a person's own address's theirs, the scanner's and the household address's Dokumente's; the family sees it only when Dokumente's sorting finds it is the household's (`haushaltsdokument`: Haus, Auto, Versicherung, Steuer, the model sure, nothing medical) or the person tags it `Haushalt` | the sender → the household, by the sorting's rule or the person's tag | paperless-mail.py `eingang`, `personen_regeln`; templates/bote/notify.py `folder_of`; homes internal/napi/upload.go; dokumente ordnen/zuteilen.py, ordnen.py `haushaltsdokument` |
| 7 | Paperless' user `dokumente` (the scan service) may view and change every document a person owns | each person's private documents → the scan service | templates/paperless/paperless-mail.py `besitz`, workflow „Dokument hinzugefügt“ |
| 8 | The Bote's memory and `session_search` span every person's conversations | each person → every later conversation | templates/bote/README.md, "Memory (#884)" |
| 9 | Homes' Radicale account writes each person's `handy-kontakte` and `privat-*`, reads their mirrors | each person's books → Homes (service) | templates/radicale/pre-start `[homes-privat]`, `[mirrors-homes]` |
| 10 | The household's calendars are copied into Home Assistant's own account | household → HA's users | templates/kalender, `[kalender-ha]` |
| 11 | Notes, and links saved from the app („merken“), are one household folder | the person → the family | homes internal/napi/merken.go; the share's `notes` |
| 12 | Music and podcast wishes are shared lists naming who wished | the person → the family | homes internal/einkauf/musik.go, internal/podcasts/ |
| 13 | The file share is one tree for every member; a File Browser share link reaches anyone with it | the person → the family; → outside by a link | templates/file-share |
| 14 | A shared Immich album or link, through immich-public-proxy | the person → outside, by their choice | templates/immich-public-proxy |
| 15 | „Kontakt prüfen lassen“: the outside contact sees the card a person keeps of them, on a single-use link (14 days) | the person's card → that contact | homes internal/kontaktantwort/ |
| 16 | Cameras and house controls reach every paired phone; who switched is noted | the house → every phone | homes internal/napi/home.go |
| 17 | A public access request reaches `HOMES_TEASER_PERSON` | outsider → one person | homes internal/pages/teaser.go |
| 18 | Admin keys read every person's Jellyfin and ABS history; Homes shows each person only their own | each person → Homes (service) | homes internal/jellyfin/gesehen.go, internal/abs/gehoert.go, internal/pages/gesehen.go |
| 19 | Planned, refused today: the archive's Umfeld reads the household address book | household → the person's archive | archiv apps/archiv/umfeld.go; `[archiv-sonst-nichts]` |
| 20 | A relation the person answered in their archive („Wie steht X zu Y?“), at their one tap there (owner 2026-11-10: the tap in their own open archive is their consent; no second confirmation): both ends household contacts → both household cards by UID (the family sees it); otherwise → only the person's own phone, by name; a person only on photos → stays in the vault. A household card never gets a name from the person's phone or photos. The archive posts it server to server to Homes' own port on the box (`/intern/uebernahme`, not through Caddy), signed with that person's archive secret, valid two minutes, each nonce once; Rückgängig takes it back. | the person's archive → household book, or → the person's phone | archiv apps/archiv/uebernahme.go, fragen_seite.go; homes internal/uebernahme |
| 21 | A scan Dokumente's sorting could not give anyone: its sender, date, page count and arrival go to Homes and are shown to the admins („Wem gehört dieser Scan?“, Deine Aufgaben), who give it to a person or the household; never its title or text | the scan's metadata → the admins | dokumente ordnen/zuteilen.py `fragen`; homes cmd/homes/zuordnen.go, internal/pages/dokumente_zuordnen.go |
| 22 | Excerpts of a person's messages that carry a relation word („Mama“, „Schatz“), with the names of sender and recipients and what the archive knows of them, go to the household model so it can suggest how two people relate. Only while that person's vault is open; at most 600 characters a message; never to a cloud model. The answer stays in the vault, and nothing is logged. Owner, 2026-11-11: „dann gehen Absender, Empfänger und Text zum LLM“. | the person's archive → the box's model process (shared by the household; root can read its memory while it answers) | archiv apps/archiv/hinweise.go; templates/archiv/archiv.container; ADR 0029 amendment 2026-11-11 |
| 23 | Which person has no second factor in Vaultwarden, or no network-drive password, becomes their login in a template's status file and in aufgaben.yml (`fuer: person:<login>`, #1168). The person sees their own task; Homes and the Bote, acting for one person, see only that person's (`aufgaben.Sees`); the admins in diag, the journal and Homes' Befunde see that a task exists, not whose | each person's account state → Laib's whole hands (owner at the shell, claude, the Operator) | templates/vaultwarden/vaultwarden-check.py, templates/file-share/file-share-check.py; internal/diag/aufgaben.go `cover`; operator/findings.go `aufgaben` |
| 24 | The mail a person sends to their own document address (`<login>+doc@`) also goes into their archive, read from the household's document mailbox with its login, only mail whose recipient is exactly that address (owner 2026-11-11); Paperless keeps the link to it as a note on the document | the household's document mailbox → the person's archive; the Message-ID → a note on the person's document | archiv apps/archiv/dokumentenpost.go `Passt`, mail.go; templates/archiv (`DOKUMENTE_MAIL_*`); templates/paperless/paperless-quittung.py `notiz` |
| 25 | The photo on each household contact card (Homes' own address books only; never a person's `handy-kontakte` or archive; firms and faces Homes wrote aside) goes into the owner's Immich, one upload per photo hash, archived (out of the timeline), in one album Homes keeps; Immich's own face detection runs on it, Homes runs no ML. When its one face lands in an unnamed Immich person, Homes' „Wer ist das?“ offers that contact's name, and only the owner's tap names the person. A changed photo replaces the upload, a photo on no card any more deletes it. **Owner-decided 2026-11-11**, accepting that faces of people outside the family then live in the owner's Immich. | household book → owner's Immich (and its face recognition) | homes internal/gesichter/kontaktfotos.go, internal/immich/immich.go `Album`, `InsAlbum`; internal/pages/kontakte_wer.go (homes#352) |
| 26 | Which owner's keys are missing that something on the box waits for: the key's name, its service, the template's label, why and help text, and its form's link — never a value, never whether an optional key is set. Every hand with `aufgabe` reads it in `laib_aufgabe list` (homes#362); Homes shows it only to admins in „Deine Aufgaben“ | secrets.age's key names (set or not) → the admins' Homes view | laib cmd/laib/aufgabe.go `missingKeys`, internal/diag/keys.go `MissingKeys`; homes „Deine Aufgaben“ |
| 27 | How many questions, suggestions and cleanup findings wait in a person's archive, and whether a key it waits for is missing: counts, a flag and the time they were counted, nothing else (owner 2026-11-12). Counted only while the vault is open, the last count kept outside it; Homes reads it with that person's archive secret and shows it only to that person | the person's archive → Homes (service) → that person; root on the box can read the file | archiv apps/archiv/aufgaben.go, intern.go `/intern/aufgaben`; homes „Deine Aufgaben“ (homes#362) |

Not crossings, checked: location goes to the person's own Reitti with
their own token (homes internal/napi/standort.go); phone backups go to
`archiv-eingang/<person>/` as the device's person and each archive mounts
only its own (ADR 0029, templates/archiv/archiv.container); the app's
shared calendars (`/napi/gemeinsam/`) are Homes' own collections, never a
person's `privat-*` (homes internal/caldav/gemeinsam.go); a phone
correction from Homes waits only for its person's phone
(homes internal/napi/korrekturen.go).

## Separation gaps

Where the design does not keep people apart, or keeps them apart only by
a prompt. Deliberate household sharing (rows 11–13 above) is not
listed again.

1. **The Bote's memory is shared** (row 8). One Hermes profile, one
   `MEMORY.md`, one `state.db`: what one person told the Bote can surface
   in another's conversation; only the SOUL's prompt asks it not to.
   Open on #884 (a profile per person).
2. **Closed: Homes read Paperless with a person's token.** Its key page
   asked for „deinen Namen → Mein Profil“, so the owner's „nur für mich“
   documents reached every family member, the Bote and the mirror.
   Paperless' post-deploy now mints the token of its own read-only user
   `homes` (no group, not in the workflow) into `HOMES_PAPERLESS_TOKEN`,
   replacing the pasted one; the owner no longer sets it. A person's own
   documents in Homes would need a per-person view; not built.
3. **„Nur für mich“ promises the admins see every document**
   (homes internal/pages/scannen.go `saysScanFuer`). In Paperless an
   owned document is seen by its owner, the users it is shared with and
   superusers. Whether the OIDC `admins` group's members are superusers:
   Box-Aufgabe Box-Aufgabe #24; the
   break-glass admin and `dokumente` see them either way.
4. **The file share has no per-person space.** Samba's `data` and File
   Browser show every member the whole tree; a person's files there are
   the family's. This is ADR 0017's household share, stated here so it is
   not mistaken for separation.
5. **The archive's stated limits** (ADR 0029 and its 2026-11-01
   amendment): root reads an open vault, the IMAP password and messenger
   keys sit outside the vault, and WhatsApp `Media/` waits in plaintext
   in `archiv-eingang/<person>/` until the Briefkasten encrypts it —
   indefinitely for a person without a vault.
6. **Closed: new documents entered Paperless without an owner** (row 6).
   Scans, the app's uploads, the Bote's „ablegen“ and mail were seen by
   every family member (and Homes' `homes`, the Bote) until someone sorted
   them; a scanned doctor's letter reached the whole family first. Owner
   2026-11-10: „Was du scannst oder hochlädst, gehört von Anfang an dir.
   Geteilt wird nur, was die Sortierung als Haushaltsdokument erkennt oder
   was du teilst.“ Now Paperless' workflow „Eingang“ and the mail rules
   give each new document to its sender or to Dokumente, and Dokumente's
   sorting gives it on (paperless section; flows 6, 21). What remains:
   Dokumente decides what the household is, by a conservative rule; the
   admins see a waiting scan's sender, date and pages; a person who never
   logged in to Paperless gets their uploads only after their first login
   (until then Dokumente's, seen by no one); documents from before stay as
   they were (no migration).

Nothing found lets one family member read another's Immich, Reitti,
Vaultwarden, archive, own Radicale books or Jellyfin/ABS history through
the apps.

## On the box, 2026-11-10

From `laib status` at 2026-11-10 09:52 UTC (read-only), all 27 services
green. This is state, not design: it changes as people log in.

| Service | What the status line said |
|---|---|
| immich | 1 Konto — one Immich user exists; every other family member gets their own at their first login. Whether that one user is also the admin account (Immich links an OIDC login to an existing user by e-mail): Box-Aufgabe Box-Aufgabe #27 |
| vaultwarden | 1 Konto, 0 mit zweitem Faktor |
| media | 3 Konten der Familie |
| radicale | 2 Kalender, 2 Listen, 2 Adressbücher |
| homes | 1 Gerät angemeldet, von 1 Person |
| file-share | Netzlaufwerk: 0 von 3 Personen mit Passwort; Syncthing: 0 von 1 Gerät verbunden |
| archiv | one instance (the owner's), „Abgeschlossen“ (vault closed), 3 Pakete im Briefkasten |
| bote | Signal verbunden |
| paperless, reitti | no account count in their status line |

The last change before this was proposal 453, „Claude in der Werkstatt
darf eine Stunde lang in Immich hineinsehen“ — an ADR 0032 Einblick.

## What a person answers

What the box cannot read — a setting inside an app — is a Box-Aufgabe for
the admins; the answer comes back into this document and the number goes.
`werkstatt-docprobe` adds each task once and writes its number here.

- Paperless: whether the `admins` members are superusers (gap 3), and
  whether the workflow „Für den Haushalt“ (Document Updated) fires on
  Dokumente's API change on the box's Paperless version, so a document
  tagged Haushalt by Dokumente has no owner afterwards:
  Box-Aufgabe #24
- Immich: whether its one user is the admin account, and which user owns
  the file share's `photos/` as an external library:
  Box-Aufgabe #27
- File Browser: a family member's scope:
  Box-Aufgabe #26
- Home Assistant and Music Assistant: which persons' phone locations
  other HA users see, and whether MA keeps a history per user:
  Box-Aufgabe #25

Who is in LLDAP's `admins` (they get „Wem gehört dieser Scan?“, gap 6) is
state, not design: Homes → Box → Personen, or LLDAP. Whether Reitti can
share a timeline is answered in its section from upstream.

## Keeping this true

A change to a template, Homes, the archive or the app that adds an
account model, gives a service a key to another's data, or adds a flow
from one person's data to others updates this document in the same PR
(CLAUDE.md, werkstatt/guide). `TestEveryTemplateWithFamilyDataIsInDataDoc`
(templates/data_doc_test.go) holds the structural part: every template
with `private:`, `oidc:` or `perPerson: true` has a section `### <name>`
here that names its `private:` class (or says no `private:`). What a
section says beyond that, the PR keeps true.
