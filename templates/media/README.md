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
