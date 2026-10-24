# kalender

Calendars from outside into Radicale, one way (#495). The owner decided on
2026-10-21: Radicale is the one place. Haus, the Bote (`haushalt`:
`termine`) and Home Assistant read there; appointments are still made where
they were (Google), and a mirror only reads.

- **An address, not a login.** A Google calendar comes in as its „Geheime
  Adresse im iCal-Format“: read only, no Google account on the box, no OAuth
  app, nothing that expires after seven days. A public calendar (Feiertage,
  Schulferien from openholidaysapi.org, the municipality's Müllabfuhr) is
  any ICS address.
- **Entered in Haus, kept by laib.** Haus → Kalender → „Kalender hinzufügen“
  (haus#66) hands the address to `laib calendar subscribe <person> <name>
  google|abo [<title>]` on stdin. laib keeps every subscription in
  `secrets.age` as `KALENDER_ABOS` and applies this template again. The
  address is never answered, journalled or put on argv; Haus shows only
  „verbunden seit …, zuletzt abgeglichen vor …“ and **Trennen**
  (`laib calendar unsubscribe`). For an admin without Haus the same verb
  works on the console, the address piped in.
- **Read only, as a service account.** `kalender.py` writes as Radicale's
  account `kalender` (seeded by `auth`), whose rights
  (`templates/radicale/pre-start`) allow exactly `*/google-*`, `*/abo-*` and
  Home Assistant's home: nobody's password. The person reads
  `<person>/google-<name>` (or `abo-<name>`); `haus/abo-*` is the family's.
  What the source drops, the next run removes, only in that collection; an
  empty answer removes nothing and is a finding. Trennen removes the copy.
- **The Takeout import's paths.** Every item's path is uuid5 of its UID, as
  `scripts/import-google.py` writes it (#414). A calendar imported from a
  Takeout into `google-<name>` is replaced by the mirror, never doubled.
- **Home Assistant.** A CalDAV client finds only its own account's
  calendars, so the run copies every mirror and the family's calendars
  (`haus/haushalt`, `haus/familie-*`) into the home of HA's own account
  `homeassistant` (`HA_USER`), read only for HA. The home-assistant
  template's post-deploy adds that CalDAV entry: nobody types a password into
  HA. This replaces the hand-made `~/ha-caldav.sh`.
- **Each calendar its own interval (#619).** A subscription's `every` (30m,
  12h, 30d) says how often it is fetched: `laib calendar subscribe … --every
  30d`, or later `laib calendar every <person> <name> --every 1d`. Without
  one, a Google calendar is fetched every run and a public abo (Müllabfuhr,
  Feiertage) every `ABO_EVERY_DAYS` (30). A failed or new one is asked again
  on the next run.
- **A timer, no daemon.** `kalender-abgleich.timer` runs it every 15 minutes
  in a stock Python image (stdlib only). `laib diag`: yellow when a calendar
  has not been mirrored for `STALE_HOURS` (2) or Google answers 404 (the
  address was reset in Google), red when Radicale refuses the account
  `kalender`.

What was there before: ServiceBay had no ongoing sync, only
`solaris-import-google`'s one-shot upload, now `scripts/import-google.py`.
v0.12.0 shipped this template as `google-kalender` with OAuth against
Google's CalDAV (#516): a Google Cloud project, a published consent screen,
each person at a browser, a refresh token per person and their Radicale
password in one secret. It was never installed. The secret address is read
only and needs none of that; the price is that Google's iCal feed can lag
behind the app by some hours, which a family calendar tolerates.

## Einrichtung (einmal, durch den Besitzer)

1. Den Vorschlag „kalender“ in der Inbox freigeben (`laib add kalender`).
   Danach einmal `laib apply auth`, `laib apply radicale` und
   `laib apply home-assistant`: auth legt die Konten `kalender` und
   `homeassistant` an, Radicale bekommt die Rechte, Home Assistant den
   Kalender-Eintrag.
2. In Haus → Kalender → „Kalender hinzufügen“ → „Google-Kalender
   verbinden“. Die Adresse steht in Google Kalender (im Browser) unter
   Einstellungen → links den Kalender wählen → „Kalender integrieren“ →
   „Geheime Adresse im iCal-Format“. Kopieren, in Haus einfügen, fertig.
3. In Home Assistant unter Einstellungen → Geräte & Dienste den alten
   CalDAV-Eintrag mit deinem eigenen Konto löschen; der neue heißt
   „homeassistant“.

Wenn `laib diag` „Google kennt die geheime Adresse nicht mehr“ meldet: die
Adresse wurde in Google zurückgesetzt. Die neue Adresse in Haus wieder
verbinden.

Ein Termin, den jemand auf dem Handy in einen `google-…`- oder
`abo-…`-Kalender schreiben will, wird abgelehnt: der Kalender ist nur zum
Lesen. Termine weiter in Google anlegen.
