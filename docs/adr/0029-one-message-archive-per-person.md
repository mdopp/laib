# ADR 0029 — One message archive per person, locked in a vault only that person opens

Applies when you build or change how a person's WhatsApp, Signal or mail
history reaches the box, where it is kept, who can read it, or when you
need one template installed once per person.

- **Status:** Accepted, 2026-11-01
- **Deciders:** the owner (mdopp), in one conversation: msgvault as the
  archive, one instance per person, one name for all („A“), deletion and
  backups as below („Das sagen wir und ist OK“), mail over IMAP, the
  Haus app first („um die Daten zum Testen zu haben“), and the vault
  („B“).
- **Issues:** laib#985 (import), laib#986 (template, instances, route),
  haus#207 (upload, keys, page), haus-android#194 (fetch the backups).

## Context

The owner wants their mail, Signal and WhatsApp history archived on the box
and searchable, for themselves first and for the family later, each person
seeing only their own.

- **No messenger backs up to our server.** WhatsApp's cloud target is
  Google Drive only. Signal keeps backups on the device, or on Signal's
  own servers. Both write a daily encrypted backup on the phone: WhatsApp
  `msgstore.db.crypt15` (readable with the 64-digit end-to-end key), Signal
  `signal-backup-*.backup` (readable with the 30-digit passphrase).
- **Android sandboxes app data.** The Haus app cannot read either
  messenger's database. It can read the backup folders the person grants it
  through the system folder picker.
- **msgvault** (Go, MIT, SQLite, full-text search, web UI, HTTP API, MCP)
  holds mail, WhatsApp and SMS in one searchable archive. It has no notion
  of users: one static API key opens everything, and there is no OIDC. It
  does not read Signal. It is alpha, and its telemetry is on by default.
- **The box has no wildcard certificate.** `box.yml` has no `dns:` block,
  so the deSEC alias of ADR 0007 (#378) is built but not enabled. Every
  hostname costs a Strato record and is published in the CT logs.

## Decision

1. **msgvault is the archive, one instance per person.** It is a template,
   `msgvault`, installed once per person. Each instance has its own
   database, its own key and its own data folder. A second purpose-alike
   service is normally a `HINWEIS`; instances of one per-person template
   are not. Laib gains the general ability to install a template for a
   person (laib#986). msgvault does not get it.
2. **One name for all: `archiv.<domain>`, LAN only.**
   - **Browser:** Caddy checks Authelia and then hands the request to the
     instance of the `Remote-User` (Laib already drops client `Remote-*`
     headers on every route). It sets that instance's API key itself, so a
     person never sees a key. A signed-in person without an instance gets
     the denied page.
   - **Agents:** an agent reaches an instance at `archiv.<domain>/p/<person>/`,
     past Authelia, with that instance's key.
   - **Strato:** one record for the name, once, not one per person.
3. **The vault.** Each person's archive data, their WhatsApp and Signal
   keys and their IMAP password live in one encrypted folder (gocryptfs).
   - Its password is known only to that person and is kept in their own
     Vaultwarden.
   - **Locked:** the instance is stopped. Disk, backups (restic stores the
     ciphertext) and the admin see nothing readable.
   - **Opening:** the person opens it in Haus. The instance starts,
     imports what waits in its inbox and fetches new mail.
   - **Closing:** it locks again after two idle hours, or on "Abschließen".
   - **The limit, stated in the guide:** while a vault is open, root on
     the box can read it.
   - If gocryptfs does not run rootless on the box (the first spike), the
     fallback is an open archive the admin may read, said in those words.
     No silent downgrade.
4. **One inbox root, one folder per person.**
   - `${DATA_DIR}/archiv-eingang/<person>/{whatsapp,signal}/` holds phone
     backups, still encrypted by the messenger, until the vault opens.
   - **Haus** mounts the whole root and writes as the person of the
     device token, never as the request says.
   - **Each instance** mounts only its own person's folder. An instance
     with a bug still cannot read another person's backups.
   - **A new person** is a new folder, not a change to Haus.
5. **The person enters their own keys.** The WhatsApp key, the Signal
   passphrase and the IMAP app password go from Haus into the open vault.
   No admin approval is involved, and they never reach the state repo, the
   Inbox or a log.
6. **Mail over IMAP** with an app password, not the Gmail API. A person
   needs no Google Cloud project.
7. **Deleting a person deletes their vault key.** The archive is then
   unreadable at once, also in every backup snapshot. Without the vault
   (the fallback), snapshots keep it until they expire. The guide says so.
8. **Order.**
   1. The Haus app and Haus's upload (haus-android#194, haus#207), so that
      real backups are on the box for testing.
   2. The decryption spike with those backups (laib#985), together with
      the gocryptfs spike.
   3. The template, instances and route (laib#986).
   4. The import (laib#985).

## Consequences

- **The kernel gains two things,** both general and neither specific to
  msgvault: a template installed per person, and a route that bundles a
  template's instances under one name.
- **Haus gains three things:**
  - a resumable upload for multi-GB files (Signal backups) beside the 25 MB
    camera upload;
  - a way to hand a key into an open vault;
  - an "Archiv öffnen / abschließen" control with the archive's state.
- **No Bote.** The Bote speaks for the whole family, so it never gets a
  key to an archive.
- **What a person must know** is in the template's README and on the Haus
  page:
  - how to switch on the two phone backups;
  - that the vault password is theirs alone, and losing it loses the
    archive;
  - that an open vault is readable by the box's admin;
  - what never reaches the archive: disappearing messages, view-once media
    and, for Signal, whatever the spike finds missing.
- **Alternatives not taken:**
  - one instance with several accounts behind a filter (msgvault's search,
    SQL and MCP see everything);
  - a hostname per person (a Strato record each, names in the CT logs);
  - live capture through a linked device (Beeper Desktop is not headless,
    Matrix bridges are heavy and risk the WhatsApp account);
  - true end-to-end encryption (the box could not search; msgvault cannot).
