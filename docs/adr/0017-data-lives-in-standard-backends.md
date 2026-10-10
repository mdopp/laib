# ADR 0017 — Data lives in standard backends; agents are clients

Applies when an agent, a page or a tool wants its own store for something
a person wrote down: a note, an appointment, a contact, a reminder.

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
