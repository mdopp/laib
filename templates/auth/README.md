# auth

Authelia (forward-auth, OIDC), LLDAP (users, groups) and Valkey (Authelia's
sessions) in one pod; `laib.yml` says what each part does. This page covers
what the family and the owner notice beyond logging in.

## Anmeldungen melden (#653)

**Für den Admin:** Schlägt eine Anmeldung immer wieder fehl, kommt eine
Nachricht über den Boten (Signal) an die Person für Mitteilungen im Haushalt
(`box.yml notify.person`). Einmal je Vorfall, nicht je Versuch:

- **Authelia sperrt einen Namen**: nach `AUTH_MAX_RETRIES` (5) Fehlversuchen
  in `AUTH_FIND_TIME` (10 Minuten) für `AUTH_BAN_TIME` (30 Minuten). Das wird
  immer gemeldet, auch aus dem Heimnetz.
- **Eine Adresse von außen** versucht es 10-mal in einer Stunde, oder mit 5
  verschiedenen Benutzernamen („sieht nach Brute Force aus“).
- **Vertippt im Heimnetz**, unterhalb einer Sperre: nur eine Zeile im Journal,
  keine Nachricht.

> 🔒 Anmeldung gesperrt: 6 Fehlversuche für „anna“ in 5 Minuten, die
> Anmeldung ist bis 18:40 gesperrt.
> Woher: 192.0.2.10 (von außen).
> Den Benutzernamen gibt es.
> Warst du das? Sonst nichts tun, die Sperre greift. Bei Häufung: sag es dem
> Operator, er kann eine längere Sperre vorschlagen (AUTH_BAN_TIME in
> services/auth/values.yml).

> ⚠️ 6 Fehlversuche von 192.0.2.30 (von außen) in der letzten Stunde, für 6
> Benutzernamen: „admin“, „root“, „test“, „guest“, „anna“ und 1 weitere.
> Sieht nach Brute Force aus. 4 davon gibt es nicht, das ist typisch für
> Brute Force.
> Warst du das? Sonst nichts tun: Authelia sperrt jeden Namen nach 5
> Fehlversuchen für 30 Minuten. Bei Häufung: …

Die Nachricht nennt Benutzername und Adresse, nie ein Passwort; Authelia
speichert keines. Jeder gemeldete Vorfall steht als Notiz (`incident`) im
Journal, so zeigen ihn der Morgenbericht und die Box-Seite.

**How it works:** `auth-anmeldungen.timer` starts `auth-anmeldungen.service`
every five minutes, which runs `anmeldungen.sh` twice:

- **`zaehlen`** runs `anmeldungen.py` in a stock Python image
  (`ANMELDUNGEN_IMAGE`, the script on stdin, as kalender does): no network,
  no capabilities, a read-only root; Authelia's data and LLDAP's folder
  mounted read-only, its own folder `auth/anmeldungen` writable. It copies
  `db.sqlite3` (and its WAL) and LLDAP's `users.db` into its tmpfs and reads
  the copies, so neither database is ever opened for writing and no
  credential is needed to tell a name that exists from one that does not
  (LLDAP's `user_id` and mail address; a login by mail counts).
  - **The cursor** (`stand.json`): the id and time of the last
    `authentication_logs` row read. A row at that id with another time is a
    new database (a restore, a reinstall): it reads from the start. A first
    run starts with the last hour, never with every old failure.
  - **Bans** follow Authelia's rule with the same values pre-start renders
    into `regulation:`: the name's last `AUTH_MAX_RETRIES` attempts within
    `AUTH_FIND_TIME` failed, so it is banned until the last of them plus
    `AUTH_BAN_TIME`. An attempt Authelia logged as `banned` starts one too.
  - **Inside or outside** is the host's own networks (`ip addr` on the host,
    `LAN_NETZE`), plus loopback and link-local. An address that cannot be
    read counts as outside, so it is never hushed.
  - **One incident** is a ban (open until it ends) or an address (open until
    an hour after its last failure). What comes while it is open is counted,
    not sent. A ban and the brute force from its address in one run are one
    message.
  - It writes bote-notify's payloads into `meldungen.jsonl` and the journal
    notes into `notizen.tsv` (`incident` per message, one `watch` per run for
    failures below every threshold). A name someone typed is cut to one short
    printable line before it goes into either.
- **`melden`** runs on the host, where bote-notify listens on loopback (as
  beets' `musik-meldungen.sh`): curl with the bearer as its config on stdin,
  never on argv; a send that fails stays for the next run, a day-old one is
  dropped. The notes go through `/var/opt/laib/laib note` as actor `laib`
  with `LAIB_CEILING=propose`, so this program can write notes and cannot
  apply anything. Without the Bote no message piles up; the notes still go.

Why here and not in laib-notify: notify runs in a container that mounts only
its own folder (ADR 0027) and stands at its line cap. The counter needs
Authelia's database, which this template owns; it runs with less than
notify has (no token at all) and moves with the template.

### Later: more login doors, the same pattern (#653 part 5)

Other services have logins outside Authelia: Home Assistant's app login
(`ip_ban`, `.ip_bans.yaml`), Vaultwarden ("Username or password is
incorrect" in its log), Samba (`smbd`: "Authentication for user [...]
FAILED"). Each fits the same three steps, in its own template:

1. a reader that turns its log or file into rows like
   `authentication_logs`' (`id`, `time`, `successful`, `banned`, `username`,
   `remote_ip`), with its own cursor;
2. the same thresholds and incident grouping (`bans()` and the per-address
   rule in `anmeldungen.py`, with that service's own ban rule);
3. `meldungen.jsonl` and `notizen.tsv` for a host-side `melden`, which can be
   this `anmeldungen.sh` unchanged once its folder is a parameter.

When the second reader comes, the grouping moves into one shared script
rather than being copied; not before (CLAUDE.md: no abstraction before it
recurs).
