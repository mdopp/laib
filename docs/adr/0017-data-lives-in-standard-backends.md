# ADR 0017 — Data lives in standard backends; agents are clients

Applies when an agent, a page or a tool wants its own store for something
a person wrote down: a note, an appointment, a contact, a reminder; or
when a template needs the household's address (amendment, #561).

- **Status:** Accepted, 2026-10-10

## Context

Solaris kept notes, favorites, topics, documents and pending residents in
its own stores, and its importers wrote calendars into its own database.
Radicale (CalDAV/CardDAV) was installed and empty. With the Bote built on
Hermes, the question was whether notes and calendars move into the agent.

## Decision

1. **Standard backends hold the data:** Radicale for appointments,
   contacts and reminders (VEVENT/VTODO with VALARM); Markdown files in
   the file share (Syncthing) for notes; Paperless for documents; Home
   Assistant for timers and devices.
2. **Agents are clients.** The Bote and Claude reach these through one
   MCP source, "Haushalt", served by Haus (calendar, contacts, notes,
   document search, memory search); Haus uses the same connections for
   its pages. Home Assistant reads Radicale through its CalDAV integration.
3. **The Bote remembers about you, never for you.** Hermes's memory
   (Honcho) holds preferences and conversation context per number; a note
   you ask it to keep becomes a Markdown file, a reminder becomes a
   calendar entry. No second index over the notes: Haus's memory library
   is the one search.
4. Importers (Google Takeout) become one-shot scripts writing ICS and
   vCard into Radicale.

## Consequences

- Phones sync natively (DAVx⁵, iOS); every app on the phone can edit a
  note; the family sees the same calendar the Bote writes to.
- Radicale gets one account per resident from LLDAP and a service account
  for the Bote (a secret in its template).
- Favorites go to Haus; topics and sessions go away; pending residents
  become a Box card.

## Amendment 2026-10-22 (#561): the household's address is box.yml zuhause

Home Assistant asks for its home in its onboarding, a solar forecast needs
coordinates, a waste-collection lookup needs the street. Kept per service,
a move means finding every copy. The owner: "wir brauchen also irgendwo ein
Setup, so dass wir Adressen auch nachträglich ändern können."

- **The address is box configuration, not a backend's data.** Templates
  read it when they render and when their hooks run, so it lives in the
  state repo once: `box.yml zuhause` (street, postcode, city, country,
  latitude, longitude, elevation), validated on load, given to units as
  the `ZUHAUSE_*` builtins and to the hooks of a service whose units name
  one (docs/STATE.md). No other service sees it.
- **Changing it is the owner's decision.** `laib zuhause set` (MCP
  `laib_zuhause`) writes the block as a proposal; the review marks a
  change to `zuhause` class human, so neither the dial nor the caller lets
  it in. The haus hand may call it (Haus's form, admins only); it decides
  nothing. The proposal's reason carries no address, so the inbox list, the
  journal and the owner's chat message do not either; the diff does, for
  the one who approves.
- **Laib never geocodes.** Street to coordinates is a call to an outside
  service and sends the address out; the kernel makes none. The
  coordinates are entered (Haus's form: a map pin, or a lookup the person
  starts knowingly, saying where it goes). A template that needs
  coordinates and finds only an address says so and changes nothing.

## Amendment 2026-10-28 (#893): a person's health data has no standard backend here

Gesundheit (`mdopp/gesundheit`) reads lab sheets and reports out of Paperless
into a timeline per person and takes a short daily note. The documents stay
in Paperless (§1). The values read and the daily notes are a person's own
and private: the share's `notes` folder is the family's (Samba, Syncthing,
File Browser read it all), Radicale holds appointments, Home Assistant
devices, so none of the backends in §1 keeps something one person may see
and another may not.

- **Gesundheit keeps them itself**, under `${DATA_DIR}/gesundheit`, one
  folder per person (plain JSON, backed up), exportable as CSV and
  deletable per person. Per-person data a program keeps for lack of a
  per-person backend is the pattern Haus set with `favoriten.json`.
- **Agents stay clients** (§2): the Bote reaches it over its MCP with one
  bearer, names a person by login, and never sees one person's data for
  another. Haus links to it and holds nothing.
- Paperless' administrators see every document (ADR 0028, „Nur für mich“);
  the page says so.
