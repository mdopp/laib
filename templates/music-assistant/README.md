# music-assistant

Music Assistant drives the speakers for Home Assistant Assist (ADR 0015).
`laib.yml` and `post-deploy` say what the hook sets up: onboarding with
Laib's administrator, Jellyfin (account `musicassistant`, the music
libraries only) and Radio Browser as sources, the token for Home
Assistant (#526).

- **The house's radio stations** (#646): every station of the share's
  `radio/*.m3u` (name and logo from `#EXTINF`) and `radio/*.strm` is added
  once through MA's builtin provider and made a favourite. A station the
  family removes in MA stays removed (`data/laib-state.json`); a stream MA
  cannot open is named in the log and tried again next apply. Die Sender
  aus dem alten Jellyfin-„Radio" spielen so auf den Lautsprechern.
- **Audiobookshelf as a source** (#646): once audiobookshelf hands over
  `ABS_MA_TOKEN` (its user `musicassistant`'s API key), Hörbücher and
  Podcasts play on the speakers. A new key sets the source up again.
- Jellyfin libraries on `/media/podcasts`, `/media/radio` or
  `/media/audiobooks` are not given to MA's Jellyfin account: they are
  not music.
