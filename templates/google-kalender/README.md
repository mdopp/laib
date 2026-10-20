# google-kalender

The family's Google calendars, mirrored one way into Radicale (#495). The
owner decided on 2026-10-20 that appointments stay in Google; the box keeps
a copy so Home Assistant (CalDAV), Haus and the Bote (`haushalt`) see them.

- **One direction.** `google-kalender.py` only reads from Google (a CalDAV
  `REPORT`). Nothing on the box can delete or change anything in Google.
  New appointments are still made in Google.
- **As the person, over CalDAV.** Each person's calendars land in their own
  Radicale collection `google-<kalender>` (e.g. `anna/google-familie`), PUT
  with their own login, never into Radicale's files (#371). The collection
  is Google's mirror: what Google drops, the next run removes. An empty
  answer from Google removes nothing and is a finding instead.
- **The Takeout import's paths.** Every item's path is uuid5 of its UID, as
  `scripts/import-google.py` writes it (#414), and the import also writes
  into `google-<kalender>`. A calendar imported from a Takeout first is
  replaced by the mirror, never doubled. vdirsyncer names items itself,
  which is why this is a small script (`tests/` holds the two to the same
  paths).
- **A timer, no daemon.** `google-kalender-sync.timer` runs it every 15
  minutes in a stock Python image (stdlib only). Each run writes
  `laib-status.json`; `laib diag` shows its findings: red for a Google
  token that expired or was revoked, a Radicale password that no longer
  works, or a calendar not mirrored for `STALE_HOURS` (6); yellow for a
  single failed run. A status older than 30 minutes means the timer
  stopped: looking at diag starts a run.
- **One secret.** `GOOGLE_KALENDER_KONTEN` holds the OAuth client and, per
  person, the Google refresh token, the Radicale password and the chosen
  calendars. `konten` writes it; it goes into `secrets.age` on stdin and
  reaches the unit through the environment, never on argv. Adding a person
  means running `konten` again for everybody (the secret is one value).

What was there before: ServiceBay had no ongoing sync, only
`solaris-import-google`'s one-shot upload, which became
`scripts/import-google.py` (#372). That import stays for the history a
Takeout has and Google's CalDAV may not.

## Einrichtung (einmal, durch den Besitzer)

Du brauchst etwa 20 Minuten und für Schritt 3 jede Person kurz am Browser.
Vorher ist das Template installiert: `laib add google-kalender` (oder den
Vorschlag in der Inbox freigeben). Bis Schritt 4 zeigt `laib diag` „noch
kein Google-Konto eingerichtet“.

1. **Google-Projekt anlegen.** Auf <https://console.cloud.google.com> ein
   neues Projekt anlegen, z. B. „Laib Kalender“. Unter „APIs und Dienste →
   Bibliothek“ die **CalDAV API** und die **Google Calendar API**
   aktivieren.
2. **Anmeldung erlauben.** Unter „Google Auth Platform“ (früher
   „OAuth-Zustimmungsbildschirm“) als Zielgruppe „Extern“ wählen, einen
   App-Namen und deine Mailadresse eintragen. Bei „Datenzugriff“ den
   Bereich `https://www.googleapis.com/auth/calendar` hinzufügen. Dann
   unter „Zielgruppe“ auf **„App veröffentlichen“** (Status „In
   Produktion“) klicken. Im Status „Test“ laufen die Zugänge nach sieben
   Tagen ab. Unter „Clients“ einen neuen Client vom Typ **„Desktop-App“**
   anlegen. Client-ID und Clientschlüssel bleiben im Browser offen, nicht
   in einen Chat und in keine Datei kopieren.
3. **Konten erfassen.** Auf der Box als `core`:
   ```sh
   mkdir -m 700 ~/google-konten
   podman run --rm -it \
     -v ~/.config/containers/systemd/google-kalender/google-kalender.py:/app/google-kalender.py:ro,z \
     -v ~/google-konten:/data:z \
     docker.io/library/python:3.13-alpine python3 /app/google-kalender.py konten
   ```
   Das Skript fragt nach Client-ID und Clientschlüssel (verdeckt), dann pro
   Person nach dem Laib-Benutzernamen und ihrem Passwort (verdeckt). Es
   zeigt eine Google-Adresse. Die Person öffnet sie, meldet sich an und
   erlaubt den Zugriff. Google warnt „App nicht überprüft“: über
   „Erweitert“ weiter, es ist deine eigene App. Danach zeigt der Browser
   „Seite nicht erreichbar“. Die ganze Adresse aus der Adresszeile ins
   Skript einfügen, dann die Kalender auswählen.
4. **Ablegen und löschen.**
   ```sh
   laib secret set GOOGLE_KALENDER_KONTEN < ~/google-konten/konten.neu && rm -r ~/google-konten
   laib apply google-kalender
   systemctl --user start google-kalender-sync.service
   laib diag
   ```
   Ohne Befund zu `google-kalender` läuft der Spiegel. Die Kalender
   erscheinen auf dem Handy (DAVx⁵) als „Familie“ usw. unter dem eigenen
   Konto.

**Wenn `laib diag` „Zugang abgelaufen“ oder „Radicale lehnt … ab“ meldet:**
Schritte 3 und 4 wiederholen, für alle Personen. Das gilt auch nach einer
Passwortänderung und für eine neue Person.

Ein Termin, den jemand auf dem Handy in einen `google-…`-Kalender
einträgt, verschwindet beim nächsten Abgleich wieder. Termine weiter in
Google anlegen.
