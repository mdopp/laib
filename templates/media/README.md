# media

Jellyfin for music and video; `laib.yml` and `post-deploy` say what the
hook sets up (the wizard, the local admin, the libraries, the family's
LLDAP login, Home Assistant's account).

- **Musik watches its folder** (#639): post-deploy turns on
  `EnableRealtimeMonitor` for the library Musik (`/media/music`, found by
  name or folder, so ServiceBay's „Music“ too), idempotently, so what
  beets' Musik-Eingang (templates/beets, README „Musik-Eingang“) moves in
  shows in Jellyfin without a scan. Neue Lieder erscheinen damit kurz nach
  dem Import von selbst in Jellyfin.

- **Haus reads the music** (#695): Haus checks a wished song against the
  household's music. Jellyfin's API keys have no levels (each acts as an
  administrator), so post-deploy keeps a Jellyfin user `haus` instead:
  hidden, no administrator, `EnabledFolders` the music libraries only (the
  ones Music Assistant's account sees, spoken word left out), no playback,
  download or deletion. Its password is random and stored nowhere; its
  token, logged in on the device `laib-haus`, goes back as
  `HAUS_JELLYFIN_TOKEN`. A token that still works is kept (`GET /Users/Me`
  as that device); one Jellyfin refuses, e.g. its device removed in the
  dashboard, is replaced by a new random password and a new login.

- **Music and video only** (#646, docs/SERVICES.md): Symfonium's shuffle
  played audiobooks, podcasts and radio between the music.
  - **ServiceBay's twins go.** post-deploy removes a library whose name
    is another's plus „2“, of the same kind, on exactly the same folders
    (Music2, Music (cdopp)2, Audiobooks2, Movies2, Shows2 on the box),
    with `DELETE /Library/VirtualFolders`: Jellyfin's entry only, never a
    file, each removal in the log. A „2“ without such a twin stays.
    Whoever saw the twin sees the original; new users get the original.
  - **Spoken word and radio leave last.** With `SPOKEN_WORD: jellyfin`
    (the default) Audiobooks, Podcasts and Radio stay and the log says
    so. The owner switches to `SPOKEN_WORD: audiobookshelf` (a values
    proposal) once Audiobookshelf and Music Assistant run; post-deploy
    then removes each of them only when its replacement answers with
    content: Audiobookshelf's book/podcast library on `/audiobooks` or
    `/podcasts` with items (read with `ABS_MA_TOKEN`), Music Assistant's
    radio count above zero (read with `HA_MUSIC_ASSISTANT_TOKEN`).
    Otherwise it stays and the apply is red with the reason. Die Familie
    ist so nie ohne ihre Hörbücher.
