# beets

[beets](https://beets.io) tags and sorts the music in the file share's
`music` folder, which Jellyfin (templates/media) reads as the library
„Musik“. Its web UI (`beets.<domain>`, admins) browses what beets knows; by
hand, `podman exec -it systemd-beets beet import /music` asks about
anything unclear. post-deploy writes a first `config.yaml` once (library in
`/config`, `import: move: yes, write: yes`, the plugins web, fetchart,
lyrics) and keeps it afterwards.

## Musik-Eingang

**Für die Familie:** Neue Musik kommt in den Ordner `musik-eingang` auf der
Freigabe (`\\<box>\data\musik-eingang`, in File Browser unter `data`, oder
über die Haus-App). Hinein darf ein einzelnes Lied (mp3, flac, m4a/aac,
ogg/opus, wav), ein ganzer Album-Ordner, auch mit Unterordnern wie `CD1`
und `CD2`, oder eine ZIP-Datei. Etwa eine Minute, nachdem das Kopieren
fertig ist, holt beets die Musik ab, ergänzt die Angaben (Interpret, Album,
Cover) und legt sie in die Musiksammlung; Jellyfin zeigt sie kurz danach.
Was abgeholt wurde, verschwindet aus `musik-eingang`. Was nicht Musik ist,
schon in der Sammlung liegt oder nicht klappte, landet in
`musik-eingang/_nicht-importiert/<Datum Uhrzeit>/`, und `GRUND.txt` daneben
sagt warum. Dort wird nichts gelöscht: aufräumen oder berichtigt zurück in
`musik-eingang` legen. RAR- und 7z-Archive kann die Box nicht auspacken;
bitte als ZIP oder ausgepackt hineinlegen. `laib status beets` (und die
Box-Seite) zeigt: „Musik-Eingang: zuletzt 12 Titel (2 Alben) importiert,
1 Datei nicht importiert“. Und nach jedem Abholen kommt eine Nachricht über
den Boten (Signal) an die Person, die über die Haus-App hochgeladen hat, sonst an die
Person für Mitteilungen im Haushalt:

> 🎵 2 Alben (12 Titel) sind in der Musik: Erfundenes Album, Noch ein Album ·
> 1 Datei nicht übernommen: cover.rar – kann ich nicht auspacken (liegt in
> musik-eingang/_nicht-importiert)

**How it works** (#639): `beets-eingang.timer` starts
`beets-eingang.service` once a minute, which runs `musik-eingang.py` inside
the beets container (`podman exec -i systemd-beets python3 -`, the script
on stdin). One run at a time: a run that finds the lock
(`/config/musik-eingang/lauf.lock`) held leaves it alone.

- **One upload is one entry at the folder's top**: a file, a folder (its
  whole tree) or a `.zip`. It is taken once its size and mtime, over the
  whole tree, are the same as on the previous run and at least 15 s old
  (`MUSIK_STABIL_S`): in practice a minute of quiet, so a copy in progress
  is never taken. What was seen is in `/config/musik-eingang/gesehen.json`.
- **It moves into `.in-arbeit/` first**, a rename on the share, so nobody
  changes it while beets works. A run that dies there leaves it for the
  next run, which puts it into `_nicht-importiert/` as interrupted.
- **A zip is unpacked with Python's zipfile** (the image has no unzip, 7z
  or unrar): refused whole, and kept, if a member leaves the folder (`..`),
  is absolute or a symlink, or the zip has a password, more than 5000 files
  (`MUSIK_ZIP_MAX_DATEIEN`), more than 20 GB unpacked
  (`MUSIK_ZIP_MAX_BYTES`, the bytes counted while unpacking, not the
  header's) or more than the disk has free. `__MACOSX/` is not unpacked.
  An unpacked zip is deleted: its content was imported or is kept.
  Another archive (rar, 7z, tar …) or a zip inside a folder is kept with
  its reason.
- **`beet -c /config/musik-eingang/beets.yaml import -q`**, the overlay
  written every run over the user's `config.yaml`: `quiet_fallback: asis`
  (an album beets cannot match goes in with its own tags instead of being
  skipped), `move: yes`, `write: yes`, `incremental: no`, and
  **`duplicate_action: skip`**: what the library holds already is never
  replaced or doubled, the new copy is kept in `_nicht-importiert/` with
  „schon in der Musiksammlung (Duplikat)“; if it is the better copy, an
  admin replaces it by hand. `keep` would put a second copy beside it, and
  `remove` would delete the library's copy without anyone looking. A single
  file is imported as a singleton (`-s`), a folder as albums.
- **What beets did not move is kept**: every file left after the import
  goes to `_nicht-importiert/<zeit>/` in its folder's shape, with a line in
  `GRUND.txt`. Only emptied folders and an unpacked zip are deleted.
- **One message per upload** that imported or kept something, through the
  Bote's bote-notify (`POST /mitteilung {an, text}`, bearer
  `BOTE_NOTIFY_TOKEN`, as templates/scan): „🎵 1 Album (2 Titel) ist in der
  Musik: …“, „🎵 3 Titel sind in der Musik: …“ for single songs, „🎵 Nichts
  Neues in der Musik · 1 Datei nicht übernommen: doppelt.mp3 – schon in der
  Musiksammlung (…)“; at most three names, then „und N weitere“. The names
  are beets' own (`beet ls -a -f '$album' added:<start>..`, `$title` for a
  singleton), the upload's name when beets has none. It is for the uploader
  Haus named (below), else `NOTIFY_PERSON` (box.yml `notify.person`). An
  empty upload or a run that took nothing writes none; `MUSIK_MELDEN:
  "false"` writes none.
- **Sent from the host, never from the container.** beets is not on the
  host network and bote-notify listens on the host's loopback only
  (`127.0.0.1:8645`), so the importer only writes each message, the
  payload plus `"at"` (epoch), as a JSON line into
  `/config/musik-eingang/meldungen.jsonl` (staged and renamed), and the
  unit's `ExecStartPost=-/bin/sh …/musik-meldungen.sh` sends them on the
  host with curl (Fedora CoreOS has sh and curl, no python). The bearer
  reaches curl as its config on stdin (`-K -`), never on argv, and never
  enters the container. A person bote-notify does not know (404) gets the
  line once more to `NOTIFY_PERSON`. A send that fails (unreachable, 429,
  5xx) stays for the next run; an unreachable bote-notify is asked once per
  run; a line older than a day is dropped. Each failure is a line in the
  journal (`journalctl --user -u beets-eingang`), never the text, and while
  lines wait the status' `einzelheiten` says „N Mitteilungen nicht
  zugestellt …“. Without the Bote (no token) the file is removed unsent.
  `curl -m 60`: bote-notify tries each channel, up to 50 s, and a shorter
  cap would send a delivered message again. Nothing about a message ever
  fails or holds up the import (`-`).

The status file `/config/laib-status.json`
(`${DATA_DIR}/beets/config/laib-status.json`, the template's `status:`):

```json
{"at": "…Z", "detail": "Musik-Eingang: zuletzt 12 Titel (2 Alben) importiert, 1 Datei nicht importiert",
 "einzelheiten": "beet import …: Exit 1\n…", "findings": [{"severity": "info|gelb", "detail", "fix", "einzelheiten"}],
 "wartet": 0, "lauf": {"at", "titel", "alben", "nicht", "ordner", "gelb", "einzelheiten"},
 "letzte": [{"at", "eingang", "von", "titel", "alben", "namen", "ordner", "nicht_importiert": [{"datei", "grund"}]}]}
```

`detail` and every `grund` are German and for the family; beets' raw
output goes only into `einzelheiten` (#588). `letzte` holds the last 20
uploads, newest first; `eingang` is the upload's name, `von` the uploader
from Haus' sidecar (empty without one), `namen` beets' names for what went
in (albums, or titles for a song), `datei` is relative to `ordner`
(`_nicht-importiert/<zeit>`). The finding lasts while that
folder exists: `gelb` when an import failed, `info` for what is simply not
music or a duplicate.

### Uploading from Haus

Haus mounts only `musik-eingang`, at `/musik-eingang` (templates/haus).
The contract for any uploader:

1. Write the file as `._<name>.part` in the folder (or inside the album
   folder being uploaded) and rename it to its final name when it is whole.
   The importer ignores names starting with `.` (so `._…`) or `~` and
   names ending in `.part`, `.tmp`, `.crdownload` or `.partial`; a folder
   holding one is still being filled and waits.
2. **Who uploaded** (haus#107): before that rename, write the sidecar
   `._<name>.von` beside the upload, at the folder's top, `<name>` exactly
   the upload's final name (`._Erfundenes Album.von` for the folder
   `Erfundenes Album`, `._Lied.mp3.von`, `._Album.zip.von`). Its first line
   is the signed-in LLDAP user, the name box.yml `people` knows the person
   by (`erika`); lower case, `[a-z0-9._@-]`, at most 64 characters, else it
   is ignored. The importer reads and deletes it when it takes the upload
   and the message goes to that person; without one, or for a person
   bote-notify does not know (404), to `notify.person`. A sidecar whose upload
   never came is deleted after a day. A sidecar, not a staging folder named
   after the user: a folder at the top is an upload (an album) itself.
3. A finished upload is taken about a minute later.
4. Progress and results are `laib status beets`: `wartet` counts uploads
   still settling, `letzte` names each upload (`eingang`, `von`) with its
   titles, beets' names for them and what was kept back and why.

### Values

| Value | Default | |
|---|---|---|
| `MUSIK_MELDEN` | `"true"` | the message per upload; `"false"`: none |
| `NOTIFY_TOKEN` | `!secret BOTE_NOTIFY_TOKEN` | bote-notify's bearer, generated by `laib add bote` (external, optional); empty without the Bote |
