# audiobookshelf

Hörbücher, Hörspiele und Podcasts, at `hoerbuecher.<domain>` and in the
Audiobookshelf apps; the family logs in with their Familienkonto (OIDC
through Authelia, #255). It takes spoken word out of Jellyfin, so a music
player's shuffle plays music only (docs/SERVICES.md, #646).

## What post-deploy sets up

- The root user (break-glass, `ABS_ROOT_PASSWORD`).
- **Metadata with the item** (`storeMetadataWithItem`): `metadata.json`
  and the cover next to each book in the share; the audio files are never
  rewritten. That is why `/audiobooks` is mounted read-write.
- The libraries, found by their folder (one the owner made or renamed
  stays): „Hörbücher & Hörspiele" (book, `/audiobooks`, matched against
  audible.de) and „Podcasts" (`/podcasts`).
- Music Assistant's account `musicassistant` (listens, changes nothing)
  and an API key for it, handed back as `ABS_MA_TOKEN`; music-assistant
  renders it, so the same apply adds Audiobookshelf as MA's source.
- Haus' account `haus`, an administrator (subscribing, checking a feed
  and asking for new episodes are admin-only in ABS), and its API key,
  handed back as `HAUS_ABS_TOKEN` (#676, haus#123); haus renders it as
  `HAUS_ABS_TOKEN` beside `HAUS_ABS_URL`, so the same apply gives Haus
  its podcast page. A key that still works is kept, as Music Assistant's.
- The family's login (OIDC), rewritten from Laib's secret on every apply.
- **The box's admins are ABS admins.** ABS registers a person at their
  first OIDC login as „user", and only an admin may make an API key (for
  an app like Symfonium). On every apply post-deploy reads LLDAP's group
  `admins` with the read-only account `audiobookshelf` (seeded by the auth
  template, `AUDIOBOOKSHELF_LDAP_PASSWORD`) and sets each ABS user of that
  name to admin. A box admin who never logged in to ABS has no account
  yet: the next apply after their first login makes them admin. Never
  touched: `root`, `musicassistant`, `haus`, and anyone who is not a box
  admin. Nobody is demoted: who leaves the admins stays ABS admin until
  the owner changes it in ABS (Einstellungen → Benutzer). LLDAP not read:
  nothing changes, the apply stays green and says so.

## The care: `abs-pflege.py`

`audiobookshelf-pflege.timer` runs it every 30 minutes in a stock Python
image; its status is `laib status audiobookshelf` / `laib diag`.

- **One-time match** against audible.de for each book without an ASIN:
  title, author, narrator, description, cover. ABS's quick match fills
  only what is empty, so an owner's edit stays; each item is asked once
  (`pflege/abgleich.json`), `MATCH_PER_RUN` (25) per run, a few seconds
  apart, the next run goes on.
- **Chapters** from Audible where the book has at most one chapter and
  Audible's runtime is within 1 % of the file's.
- **Hörspiel series**: a folder with at least two children named
  `<Nummer> - <Titel>` (`Die drei ???/001 - …`) is a series; each child
  gets the series, its number as the sequence and the bare title, where
  the match gave no series.
- **Tags that made a title up** (#678): a title with U+FFFD, only
  „CD 1"/„Teil 1", the author's or series' name, or the folder's name
  plus „CD 1" gets the folder's name (ABS's default precedence lets the
  album tag beat the folder; the API write lands in `metadata.json`).
  Audible is asked up to three times: then with umlauts restored
  („Schuesse" → „Schüsse") and without the author. A series folder takes
  the series name Audible gave most of its children. An item without a
  hit is asked again once there is a new question.
- **CDs in one folder** (`1-01 …`, `CD 1-01 …`) are put in disc order
  through ABS's track API, chapters with them, once per item and file
  count. **Copies** of a file (same size, same length) are a finding with
  the exact moves to `aussortiert/audiobooks/…`; nothing is moved or
  renamed by the care.
- **Items without a hit** are in the status („Hörbücher: 140
  abgeglichen, 3 ohne Treffer: …") and as findings, until the owner
  matched them in ABS (Buch öffnen → Bearbeiten → Abgleichen).
- **Podcasts**: the feeds in `podcasts/sync_podcasts.py` (its `PODCASTS`
  literal, read as data, never run) are subscribed with auto-download; a
  folder that is already there gets the feed and its episodes renamed
  from it, a new one starts with its three newest episodes. After that,
  podcasts are added in Haus (by link) or in the app; `sync_podcasts.py`
  can go.
- **The last three episodes** (#676, the owner 2026-10-25): every
  podcast keeps its newest three (`maxEpisodesToKeep: 3`) and fetches at
  most three new ones a check (`maxNewEpisodesToDownload: 3`), whoever
  subscribed it. ABS has no library-wide setting, so the care sets each
  podcast's: a field at ABS's default that the care never set. A value
  the owner chose in ABS stays, also one chosen after the care set 3
  (`pflege/abgleich.json`, `behalten`); the status counts them („2 mit
  eigener Einstellung"). ABS removes the oldest episode, file and all,
  after each automatic download, so a podcast with many episodes shrinks
  to three one new episode at a time.
