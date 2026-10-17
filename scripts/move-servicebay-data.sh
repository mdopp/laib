#!/usr/bin/env bash
# Runs move-servicebay-data.py (#229) on the box, as core: in the laib-hooks
# image (Fedora CoreOS has no Python), as the container's root, which is root
# over the box's container uids, so ownership is kept. Syncthing's podman
# volume is mounted by name when it exists. The user units that run are read
# here, on the host, and handed over: a ServiceBay unit whose file is out but
# which still runs is refused (a live database copied is no copy). Without
# that list it stops.
#
# immich (#263): Immich's Postgres is started here, on its data dir, for the
# rewrite (never beside one that has it open: a running unit or container
# that mounts the database or Syncthing's config is refused), a pg_dump is
# taken with Postgres's own pg_dump before a real run
# (<data>/immich/before-library-move.dump, kept; a later run keeps the first
# one), and the mover reaches it through the socket. Postgres stops again on
# exit. IMMICH_DB_IMAGE: the image the box runs Immich's Postgres with.
#
# Usage: scripts/move-servicebay-data.sh [--dry-run] <service>...
#   DATA_DIR (/mnt/data/stacks), QUADLET (~/.config/containers/systemd),
#   HOOKS_IMAGE (ghcr.io/mdopp/laib-hooks:latest) override the defaults.
set -euo pipefail
data=${DATA_DIR:-/mnt/data/stacks}
quadlet=${QUADLET:-$HOME/.config/containers/systemd}
here=$(cd "$(dirname "$0")" && pwd)
active=$(systemctl --user list-units --type=service --state=active,activating,deactivating,reloading --plain --no-legend | awk '{print $1}' | tr '\n' ' ')
vol=()
if podman volume exists file-share-syncthing-config 2>/dev/null; then
  vol=(-v file-share-syncthing-config:/servicebay-volumes/file-share-syncthing-config:ro)
fi
pg=() pgargs=()
case " $* " in *" immich "*)
  if [ -d "$data/immich/pgdata" ]; then
    for u in immich.service immich-db.service; do
      case " $active " in *" $u "*) echo "immich: REFUSED: $u still runs: systemctl --user stop $u first" >&2; exit 1;; esac
    done
    busy=$(podman ps -q | xargs -r podman inspect --format '{{.Name}}{{range .Mounts}} {{.Source}}{{end}}' |
      awk -v p="$data/immich/pgdata" -v s="$data/file-share/syncthing-config" \
        '{for (i = 2; i <= NF; i++) if ($i == p || $i == s || index($i, p "/") == 1 || index($i, s "/") == 1) { print $1; break }}' | tr '\n' ' ')
    if [ -n "$busy" ]; then
      echo "immich: REFUSED: these containers have Immich's database or Syncthing's config open: $busy- stop them first" >&2
      exit 1
    fi
    db=laib-move-immich-db
    sock=$(mktemp -d)
    chmod 0777 "$sock"
    stop_db() { podman stop -t 60 "$db" >/dev/null 2>&1 || true; podman unshare rm -rf "$sock" 2>/dev/null || rm -rf "$sock"; }
    trap stop_db EXIT
    podman run -d --rm --name "$db" --network none --security-opt label=disable \
      -v "$data/immich/pgdata:/var/lib/postgresql/data" -v "$sock:/var/run/postgresql" \
      "${IMMICH_DB_IMAGE:-ghcr.io/immich-app/postgres:14-vectorchord0.4.3-pgvectors0.2.0}" >/dev/null
    for i in $(seq 120); do
      podman exec "$db" pg_isready -q -h /var/run/postgresql -U postgres && break
      if [ "$i" = 120 ]; then echo "immich: REFUSED: Immich's Postgres did not come up:" >&2; podman logs --tail 20 "$db" >&2; exit 1; fi
      sleep 1
    done
    dump="$data/immich/before-library-move.dump"
    if [[ " $* " != *" --dry-run "* && ! -s "$dump" ]]; then
      need=$(podman exec "$db" psql -X -At -h /var/run/postgresql -U postgres -d immich -c "select pg_database_size('immich')")
      free=$(df -B1 --output=avail "$data/immich" | tail -1)
      if [ $((need + (1 << 30))) -gt "$free" ]; then echo "immich: REFUSED: the dump needs up to $((need >> 20)) MiB and 1 GiB to spare, $((free >> 20)) MiB free" >&2; exit 1; fi
      podman exec "$db" pg_dump -Fc -h /var/run/postgresql -U postgres immich > "$dump.partial"
      sync "$dump.partial"
      mv "$dump.partial" "$dump"
      echo "immich: pg_dump of Immich's database: $dump"
    fi
    pg=(-v "$sock:/pgsock")
    pgargs=(--pg-socket /pgsock --pg-dump "$dump")
  fi
esac
podman run --rm --network none --security-opt label=disable \
  -v "$data:$data" -v "$quadlet:/quadlet:ro" -v "$here:/m:ro" "${vol[@]}" "${pg[@]}" \
  "${HOOKS_IMAGE:-ghcr.io/mdopp/laib-hooks:latest}" \
  python3 /m/move-servicebay-data.py --data-dir "$data" --quadlet /quadlet --volumes /servicebay-volumes --active "$active" "${pgargs[@]}" "$@"
