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
- The family's login (OIDC), rewritten from Laib's secret on every apply.

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
  podcasts are added in the app; `sync_podcasts.py` can go.
