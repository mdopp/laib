# ADR 0032 — Agents stay out of the family's data: sealed and approval services

Applies when an agent (the Operator, Claude, pi, the Bote) wants to look
into a service's files or containers, when you write a template that keeps
the family's own data, or when a repair needs to see inside Paperless,
Immich and the like.

- **Status:** Accepted, 2026-11-09
- **Deciders:** the owner (mdopp), decision D of the security plan
  („Daten sicher, ohne Sperren“, 2026-11-09): option 2, „Nur mit einer
  Freigabe pro Anlass, ohne Inhalte im Journal. Das Archiv bleibt immer
  zu.“

## Context

`laib look` ran `cat`, `grep` and `find` at the read tier in every
service's containers, the archive and Paperless included, without budget
(ADR 0013: looking is unbounded). Its output went into the journal, cut to
1,500 characters, with only the box's own secrets blacked out; on
2026-11-08 an exec in Paperless wrote lines of an insurance document into
the journal, and the journal goes into every backup. Agents repair the box;
they need not read the family's letters, photos, calendars or messages to
do so, and the input that reaches them from outside (a scanned letter, a
contact form) can carry instructions.

## Decision

A template marks the family's data with `private:` in its `laib.yml`:

- **`sealed`** — the person's message archive (`archiv`). No `look`,
  `read`, `exec` or `write` reaches its containers or files, for no hand,
  the human at the shell included: the archive has one door, its person's
  passkey. Its state is in its status file and its logs.
- **`approval`** — Paperless, Immich, Radicale, Reitti, the file share and
  Vaultwarden. A human looks as before. Any other hand's `look`, `read` and
  `exec` are refused until a human approved an **Einblick** for that hand
  and service: the refusal files one (class human, `einblicke/<time>-<hand>-
  <service>.md`, a card in the Inbox with the command it was for; a second
  refusal while it waits points at the same card). After the approval
  that hand may look into that service for one hour (`envelope.GrantFor`);
  then the next look files a new card. Approving runs nothing. `write`
  stays in the envelope as before.
- For every private service the journal keeps of a `look` or `exec` only
  the size of its output („1,2 KB Ausgabe“), never the text; the hand still
  gets the output it asked for.

`inspect`, `logs`, `status` and the status file are unchanged: they are how
agents learn a service's state. A service without `private:` is unchanged.

## Consequences

- Repairs inside Paperless and the like take a tap from the owner first
  (the split of 2026-11-08 would have waited for one). That gap is chosen.
- The hour is per hand and service, not per command: a card is one
  occasion, as the owner asked, not one line.
- A typo (`private: offen`) does not load: a template cannot leave the
  family's data open by misspelling the guard.
- Root on the box still reads everything that is open; this keeps agents
  from reading along by the way, it does not stop an admin who means to
  (the plan's „Was unmöglich bleibt“).
