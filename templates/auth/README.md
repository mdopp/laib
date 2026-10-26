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

**Die anderen Türen (#703):** Vaultwarden (die Passwörter), das
Netzlaufwerk (Samba) und Home Assistant haben eine eigene Anmeldung. Für
jede gilt: kommen von einer Adresse 10 Fehlversuche in einer Stunde, oder 5
verschiedene Benutzernamen, kommt eine Nachricht, einmal je Vorfall. Von
außen immer; das Netzlaufwerk gibt es nur im Heimnetz, dort zählt auch das
Heimnetz. Sperrt Home Assistant selbst eine Adresse (`HA_LOGIN_SPERRE`),
wird das sofort gemeldet.

> ⚠️ Vaultwarden (Passwörter): 10 Fehlversuche für „anna@example.home“ in
> der letzten Stunde.
> Woher: 192.0.2.60 (von außen).
> Warst du das? Ein Gerät mit einem alten Master-Passwort versucht es auch
> immer wieder. Sonst nichts tun: Vaultwarden bremst wiederholte
> Anmeldungen, sperrt aber niemanden. Bei Häufung: sag es dem Operator.

> ⚠️ Netzlaufwerk (Samba): 10 Fehlversuche für „anna“ in der letzten Stunde.
> Woher: 10.0.0.9 (aus dem Heimnetz).
> Warst du das? Oft ist es ein Gerät, das sich noch mit einem alten
> Passwort am Netzlaufwerk anmeldet: dort das Passwort neu eingeben. Sonst
> versucht ein Gerät im Heimnetz Passwörter: sag es dem Operator.

> 🔒 Home Assistant hat 192.0.2.70 gesperrt: zu viele Fehlversuche bei der
> Anmeldung (10 Fehlversuche in der letzten Stunde).
> Woher: 192.0.2.70 (von außen).
> Warst du das? Dann die Adresse aus ip_bans.yaml im Ordner von Home
> Assistant löschen und Home Assistant neu starten, der Operator kann das
> vorschlagen. Sonst nichts tun, die Sperre greift.

**Ist der Bote nicht erreichbar** (oder gibt es keinen), kommt die Nachricht
nach 15 Minuten per Mail, einmal, sofern die Box einen Mailserver hat
(`SMTP_HOST`, derselbe wie für „Passwort vergessen“), an `box.yml
notify.mail` oder die Adresse des Admins. Ohne Mailserver wartet sie auf den
Boten, höchstens einen Tag.

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
  dropped, unless the mail takes it (below). The notes go through `/var/opt/laib/laib note` as actor `laib`
  with `LAIB_CEILING=propose`, so this program can write notes and cannot
  apply anything. Without the Bote and without a mail server no message
  piles up; the notes still go.
- **The mail fallback** (#703): `melden` hands a message to laib-notify
  when the Bote has not taken it for 15 minutes (unreachable, 429, 5xx),
  refused it (a person it does not know), or there is no Bote or no
  `box.yml notify.person` at all, and the box has a mail server
  (`SMTP_HOST`): one file per message, written whole and then renamed, in
  laib-notify's folder `eingang/` (`/mnt/data/laib/notify/eingang`). laib-notify
  tells it as it tells its own (notify.go, #441/#659): the Bote once more,
  else one mail, to `MAIL_TO`, with the message's `betreff`; it removes the
  file once told, or after a day. No second SMTP client, no second
  recipient rule: the mail is laib-notify's, as everything else the owner
  gets mailed.

Why here and not in laib-notify: notify runs in a container that mounts only
its own folder (ADR 0027) and stands at its line cap. The counter needs
Authelia's database, which this template owns; it runs with less than
notify has (no token at all) and moves with the template.

### The other login doors (#703)

Vaultwarden, Samba and Home Assistant log a failed login to their unit's
journal. Before the counter runs, `zaehlen` exports each unit's journal
after its cursor (`journalctl --user -u <unit> -o json --output-fields=MESSAGE
--after-cursor`, the first time the last hour) into
`anmeldungen/journal/<door>.json`; the counter reads it (no extra mount,
the folder is its own) and writes the last entry's cursor to
`journal/<door>.cursor`, after its state, so a run that died reads the
export again and drops what it knows by id. The doors (`DOORS` in
`anmeldungen.py`, `TUEREN` in `anmeldungen.sh`):

| door | unit | the line | what the template does for it |
|---|---|---|---|
| Vaultwarden | `vaultwarden.service` | `Username or password is incorrect. Try again. IP: <ip>. Username: <name>.`, `Invalid admin token. IP: <ip>` (the admin page) | `IP_HEADER=X-Forwarded-For`: Caddy sends no X-Real-IP, so Vaultwarden logged Caddy's address |
| Samba | `file-share-samba.service` | `Auth: [SMB2,…] user […]\[<name>] … status [NT_STATUS_WRONG_PASSWORD] … remote host [ipv4:<ip>:<port>]` | `SAMBA_CONF_LOG_LEVEL=1 auth_audit:2`: Samba logs failed logins (level 2) and no successes (level 3) |
| Home Assistant | `home-assistant.service` | `Login attempt or request with invalid authentication from <host> (<ip>).`, `Banned IP <ip> for too many login attempts` | nothing; its own ban is `HA_LOGIN_SPERRE` (templates/home-assistant/README.md) |

A door has no ban of its own (Vaultwarden only slows logins down), so the
rule is #653's per-address rule: 10 failures or 5 names from one address
within an hour, from outside; Samba's in the LAN too, as the share has no
door to the outside. Home Assistant names no user. A ban Home Assistant
logs is a message at once, and it stays one incident for a day. What stays
below is the same `watch` note, with the door's name.

Why in this counter and not one reader per template, as #653 sketched:
templates cannot share a script, and the grouping, the cursor folder and
`melden` would otherwise be copied three times (CLAUDE.md: the second
reader is when it moves into one place). The other templates change only
what makes their log say who and from where. A door whose unit is not on
the box exports nothing.
