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
