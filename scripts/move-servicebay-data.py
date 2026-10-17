#!/usr/bin/env python3
"""One-shot: copy the Kraftwerk's data into Laib's layout (#229, ADR 0010).

Laib's built-ins keep their own layout; the data moves once, here, rather
than the templates learning ServiceBay's paths. Per service, after its
ServiceBay unit is stopped and its file moved out of the Quadlet root
(docs/MIGRATION.md step 4a), before `laib add`:

  vaultwarden     <D>/vaultwarden/*               -> <D>/vaultwarden/data/
  home-assistant  <D>/home-assistant/matter-server -> <D>/home-assistant/matter
                  Z-Wave JS's server: ServiceBay's external-settings file put
                  it on 3001 (3000 was NPM's); it goes into zwave-js-ui's own
                  settings.json on its default 3000, and Home Assistant's
                  Z-Wave JS entry follows
  file-share      podman volume file-share-syncthing-config -> <D>/file-share/syncthing-config
                  <D>/file-share/filebrowser-db/filebrowser.db -> <D>/file-share/filebrowser/
                  Samba's passdb stays in place (<D>/file-share/samba-private): said, whose
                  accounts carry over with their passwords
  radicale        <D>/radicale/data stays where it is and becomes the box user's,
                  whom Laib's unit maps onto radicale's uid (keep-id), #311
  werkstatt       the git checkouts of <D>/claude-dev/workspace and
                  <D>/pi-web/workspace -> <D>/werkstatt/workspace (pi-web's
                  under pi-web-<name> where claude-dev has the same name)
  immich          the external libraries ServiceBay's disk import made move
                  into Laib's photos layout (#263), one folder per library:
                  <D>/file-share/data/<user>/photos -> <D>/file-share/data/photos/<user>
                  <D>/file-share/data/photos         -> <D>/file-share/data/photos/shared
                  Immich's import paths, asset and sidecar paths and its
                  path checksums are rewritten in one transaction, so every
                  asset keeps its ID and with it its albums, faces,
                  favourites, descriptions and edits; the Syncthing folders
                  on those paths follow in config.xml with their IDs

It copies, it never moves: ServiceBay's paths stay as they were, so handing a
service back is re-enabling its unit; they go with ServiceBay's data dir after
30 days. The two files it edits in place (zwave-js settings.json, Home
Assistant's core.config_entries) keep ServiceBay's version beside them as
<file>.servicebay. A service whose ServiceBay file is still in the Quadlet
root, whose ServiceBay unit still runs (--active, from the wrapper), whose
target already holds data or whose copy does not fit is refused before
anything of it is written. Each target is copied into <target>.partial and
renamed once synced: it is whole or absent.

immich is the exception: the photos are moved (renamed on the same disk, no
second copy of the library), and the database is rewritten in place. The
wrapper takes a pg_dump of Immich's database first
(<D>/immich/before-library-move.dump, kept); the run keeps a journal
(<D>/immich/library-move.json), so a run cut off anywhere is finished by the
next one, and a finished one does nothing. A rollback is that dump restored
and the folders renamed back (docs/MIGRATION.md).

Runs as root in the laib-hooks image (scripts/move-servicebay-data.sh), which
is root over the box's container uids: ownership is kept, the Werkstatt's
checkouts go to its dev user (1000).

Usage: move-servicebay-data.py --data-dir D --quadlet Q [--volumes V] [--active UNITS]
                               [--pg-socket DIR] [--pg-dump FILE] [--dry-run] <service>...
"""
import argparse, hashlib, json, os, re, shutil, stat, subprocess, sys
import xml.etree.ElementTree as ET
from xml.sax.saxutils import escape

ZWAVE_PORT = 3000          # zwave-js-ui's default; nothing on a Laib box holds it
WERKSTATT_UID = 1000       # werkstatt/Containerfile: useradd -u 1000 dev
SERVICEBAY_UNITS = {"werkstatt": ["claude-dev", "pi-web"]}
RESERVE = 1 << 30          # a data disk filled to the last byte wedges every service on it
# units that must not run either while a service moves (#263): Laib's own
# Immich database, and both Syncthings, whose folders move with the photos
QUIET_UNITS = {"immich": ["immich-db", "file-share", "file-share-syncthing"]}
PHOTOS = "/mnt/photos"     # where immich-server sees the photos: ServiceBay's mount and Laib's
SYNC = "/var/syncthing/Sync"   # where Syncthing sees file-share/data: ServiceBay's mount and Laib's
SHARED = "shared"          # Laib's folder for ServiceBay's Shared library
TMP_SHARED = ".photos-servicebay-shared"   # the Shared area on its way under photos/


class Refused(Exception):
    pass


def has_data(p):
    return os.path.isdir(p) and any(os.scandir(p)) or os.path.isfile(p)


def clear(p):
    if os.path.isdir(p) and not os.path.islink(p):
        shutil.rmtree(p)
    elif os.path.lexists(p):
        os.remove(p)


def special(d, names):
    """Sockets, FIFOs, devices: a running app's, never data (git's fsmonitor socket)."""
    return [n for n in names if stat.S_IFMT(os.lstat(os.path.join(d, n)).st_mode) not in (stat.S_IFDIR, stat.S_IFREG, stat.S_IFLNK)]


def size(p):
    """Bytes the copy writes (a sparse file is written out whole)."""
    if os.path.islink(p) or not os.path.exists(p):
        return 0
    if not os.path.isdir(p):
        return os.path.getsize(p)
    return sum(os.lstat(os.path.join(r, f)).st_size for r, _, fs in os.walk(p) for f in fs)


def own(s, d, uid):
    st = os.lstat(s)
    try:
        os.lchown(d, uid if uid is not None else st.st_uid, uid if uid is not None else st.st_gid)
    except PermissionError:
        if os.geteuid() == 0:
            raise           # the wrapper runs as root: an owner that does not carry over stops the copy
        # not root: the tests


def chown_tree(p, uid, gid):
    """lchown p and everything below it; not root (the tests) leaves owners as they are"""
    for root, dirs, files in os.walk(p):
        for n in [root] + [os.path.join(root, x) for x in dirs + files]:
            try:
                os.lchown(n, uid, gid)
            except PermissionError:
                if os.geteuid() == 0:
                    raise


def put(src, dst, uid):
    if os.path.isdir(src) and not os.path.islink(src):
        shutil.copytree(src, dst, symlinks=True, ignore=special)
        own(src, dst, uid)
        for root, dirs, files in os.walk(dst):
            for n in dirs + files:
                t = os.path.join(root, n)
                own(os.path.join(src, os.path.relpath(t, dst)), t, uid)
    else:
        shutil.copy2(src, dst, follow_symlinks=False)
        own(src, dst, uid)


def copy(src, dst, uid=None, only=None):
    """cp -a (content, modes, times, owner, or uid for the whole tree) into
    <dst>.partial, synced, then renamed: the target is whole or absent, and a
    copy cut off by a power cut is a .partial the next run clears. only: these
    entries of src become dst's."""
    tmp = dst + ".partial"
    clear(tmp)
    os.makedirs(os.path.dirname(dst), exist_ok=True)
    if only is None:
        put(src, tmp, uid)
    else:
        os.mkdir(tmp)
        for n in only:
            put(os.path.join(src, n), os.path.join(tmp, n), uid)
        shutil.copystat(src, tmp)
        own(src, tmp, uid)
    os.sync()
    os.replace(tmp, dst)        # an empty dst folder is replaced
    os.sync()


def edit_json(path, change):
    """Rewrite a JSON file through change(doc), ServiceBay's version kept beside it."""
    def run():
        doc = json.load(open(path)) if os.path.exists(path) else {}
        if os.path.exists(path) and not os.path.exists(path + ".servicebay"):
            shutil.copy2(path, path + ".servicebay")
        change(doc)
        with open(path + ".tmp", "w") as f:
            json.dump(doc, f, indent=2, ensure_ascii=False)
            f.flush()
            os.fsync(f.fileno())
        os.replace(path + ".tmp", path)
    return run


def plan(service, d, volumes, a=None):
    """(description, action) steps for one service; raises Refused."""
    steps, notes = [], []      # (what, the sources it copies, action)
    if service == "immich":
        return immich_plan(d, volumes, a and a.pg_socket, a and a.pg_dump, a and a.dry_run)

    def into(src, dst, uid=None):
        if has_data(dst):
            raise Refused(f"{dst} already holds data; move it aside first")
        steps.append((f"copy {src} -> {dst}", [src], lambda: copy(src, dst, uid)))

    if service == "vaultwarden":
        root = os.path.join(d, "vaultwarden")
        names = sorted(n for n in os.listdir(root) if n not in ("data", "data.partial")) if os.path.isdir(root) else []
        if has_data(os.path.join(root, "data")):
            raise Refused(f"{root}/data already holds data; move it aside first")
        if names:
            steps.append((f"copy {root}/{{{','.join(names)}}} -> {root}/data/", [os.path.join(root, n) for n in names],
                          lambda: copy(root, os.path.join(root, "data"), only=names)))
    elif service == "home-assistant":
        ha = os.path.join(d, "home-assistant")
        if os.path.isdir(os.path.join(ha, "matter-server")):
            into(os.path.join(ha, "matter-server"), os.path.join(ha, "matter"))
        ext = os.path.join(ha, "zwave-js", "sb-external-settings.json")
        if os.path.exists(ext):
            old = json.load(open(ext))
            prev = old.get("serverPort")

            def zwave(doc):
                z = doc.setdefault("zwave", {})
                z.update(old)
                z.update(serverEnabled=True, serverPort=ZWAVE_PORT, serverHost=old.get("serverHost", "127.0.0.1"))
            steps.append((f"zwave-js settings.json: server on {ZWAVE_PORT} (ServiceBay: {prev} via {os.path.basename(ext)})", [],
                          edit_json(os.path.join(ha, "zwave-js", "settings.json"), zwave)))
            if prev and prev != ZWAVE_PORT:
                def entries(doc):
                    for e in doc.get("data", {}).get("entries", []):
                        url = e.get("data", {}).get("url", "")
                        if e.get("domain") == "zwave_js" and url.rstrip("/").endswith(f":{prev}"):
                            e["data"]["url"] = url.rstrip("/")[: -len(str(prev))] + str(ZWAVE_PORT)
                cfg = os.path.join(ha, "homeassistant", ".storage", "core.config_entries")
                if os.path.exists(cfg):
                    steps.append((f"Home Assistant's Z-Wave JS entry: :{prev} -> :{ZWAVE_PORT}", [], edit_json(cfg, entries)))
    elif service == "file-share":
        fs = os.path.join(d, "file-share")
        vol = os.path.join(volumes or "", "file-share-syncthing-config")
        if volumes and os.path.isdir(vol):
            into(vol, os.path.join(fs, "syncthing-config"))
        else:
            notes.append("no podman volume file-share-syncthing-config: Syncthing starts with a new device ID")
        db = os.path.join(fs, "filebrowser-db", "filebrowser.db")
        if os.path.exists(db):
            into(db, os.path.join(fs, "filebrowser", "filebrowser.db"))
        if os.path.isdir(os.path.join(fs, "filebrowser-config")):
            notes.append("filebrowser-config stays behind: Laib's File Browser is configured by its unit")
        # the passdb sits where Laib's samba mounts it: nothing to copy, the NT hashes stay (#296);
        # the names only, never a hash, so the operator knows whose accounts carry over
        passdb = os.path.join(fs, "samba-private", "smbpasswd")
        if os.path.isfile(passdb):
            names = sorted(l.split(":", 1)[0] for l in open(passdb) if ":" in l)
            notes.append(f"Samba's accounts stay in place with their passwords: {', '.join(names)}; a person's "
                         "stays while they are in family or admins in LLDAP, the shared SHARE_USER always")
    elif service == "radicale":
        # ServiceBay's image chowned /data to its radicale user (2999) in the
        # default user namespace, a subuid on the host; Laib's unit maps core
        # onto 2999 (UserNS=keep-id), so the collections become core's, in
        # place: the wrapper's root is core. ServiceBay's image chowns them
        # back on its next start, so a rollback needs nothing (#311)
        data = os.path.join(d, "radicale", "data")
        if os.path.isdir(data):
            steps.append((f"hand {data} to the box's user (Laib maps it onto radicale's 2999)", [], lambda: chown_tree(data, 0, 0)))
    elif service == "werkstatt":
        dst = os.path.join(d, "werkstatt", "workspace")
        # the names follow from the sources alone, so a second run finds its
        # first run's copies as targets that hold data, never as a clash
        checkouts = {}
        for old in ("claude-dev", "pi-web"):
            src = os.path.join(d, old, "workspace")
            for n in sorted(os.listdir(src)) if os.path.isdir(src) else []:
                p = os.path.join(src, n)
                if not os.path.isdir(os.path.join(p, ".git")):
                    notes.append(f"stays behind: {p} (not a checkout)")
                    continue
                name = f"{old}-{n}" if n in checkouts else n
                if name in checkouts or has_data(os.path.join(dst, name)):
                    raise Refused(f"{dst}/{name} exists already")
                checkouts[name] = p
        for name, p in checkouts.items():
            steps.append((f"copy {p} -> {dst}/{name}", [p], lambda p=p, name=name: copy(p, os.path.join(dst, name), WERKSTATT_UID)))
        if os.path.isdir(os.path.join(d, "pi-web", "data")):
            notes.append(f"stays behind: {d}/pi-web/data (pi-web's sessions)")
    else:
        raise Refused(f"nothing to move for {service}: its built-in mounts ServiceBay's paths as they are")
    return steps, notes


# ---- immich (#263) ----------------------------------------------------------

def photo_plan(libraries):
    """ServiceBay's disk import made one external library per user on
    /mnt/photos/<user>/photos (file-share/data/<user>/photos) and a Shared one
    on /mnt/photos/photos (file-share/data/photos). Laib mounts
    file-share/data/photos at /mnt/photos; each library becomes one folder
    there, <user>/ and shared/, so no library's path holds another's photos.
    -> (rewrites [(library id, import path as stored, new import path)],
    moves [(src, dst)] relative to file-share/data with the Shared one
    first, notes)."""
    rewrites, moves, notes = [], {}, []
    for lib in libraries:
        if lib.get("deleted"):
            notes.append(f"library {lib['name']} is being deleted by Immich: left as it is")
            continue
        for p in lib["importPaths"]:
            n = os.path.normpath(p)
            if n != PHOTOS and not n.startswith(PHOTOS + "/"):
                notes.append(f"library {lib['name']}: {p} is outside the share, left as it is")
                continue
            parts = n[len(PHOTOS) + 1:].split("/") if n != PHOTOS else []
            if parts == ["photos"]:
                src, dst = "photos", "photos/" + SHARED
            elif len(parts) == 2 and parts[1] == "photos" and parts[0] not in ("photos", SHARED) and not parts[0].startswith("."):
                src, dst = parts[0] + "/photos", "photos/" + parts[0]
            else:
                raise Refused(f"library {lib['name']}: {p} is none of the paths ServiceBay's disk import made "
                              f"({PHOTOS}/<user>/photos, {PHOTOS}/photos; a user named {SHARED} or photos clashes with Laib's layout): move it by hand")
            rewrites.append((lib["id"], p, PHOTOS + dst[len("photos"):]))
            moves[src] = dst
    return rewrites, sorted(moves.items(), key=lambda m: m[0] != "photos"), notes


def load(path):
    try:
        with open(path) as f:
            return json.load(f)
    except FileNotFoundError:
        return {}


def save(path, doc):
    with open(path + ".tmp", "w") as f:
        json.dump(doc, f, indent=2)
        f.flush()
        os.fsync(f.fileno())
    os.replace(path + ".tmp", path)
    sync_dir(os.path.dirname(path))


def sync_dir(p):
    fd = os.open(p, os.O_RDONLY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def photo_steps(base, moves):
    """The renames in order. The Shared area goes under photos/ through a
    temporary name beside it; a library whose folder never existed gets an
    empty one, so its import path is valid."""
    steps = []
    for src, dst in moves:
        if src == "photos":
            steps += [["rename", "photos", TMP_SHARED], ["mkdir", "photos", TMP_SHARED], ["rename", TMP_SHARED, dst]]
        else:
            steps.append(["mkdir", "photos", "."])
            steps.append(["rename", src, dst] if os.path.isdir(os.path.join(base, src)) else ["mkdir", dst, "photos"])
    return steps


def move_photos(base, moves, journal):
    """Run the renames, each one recorded in the journal once done: a run cut
    off between two is finished by the next one, a finished one does nothing."""
    j = load(journal)
    if "disk" not in j:
        j["disk"], j["disk_next"] = photo_steps(base, moves), 0
        save(journal, j)
    while j["disk_next"] < len(j["disk"]):
        op, a, b = j["disk"][j["disk_next"]]
        pa, pb = os.path.join(base, a), os.path.join(base, b)
        if op == "rename":
            if os.path.lexists(pa) and not os.path.lexists(pb):
                os.rename(pa, pb)
                sync_dir(os.path.dirname(pa))
                sync_dir(os.path.dirname(pb))
            elif os.path.lexists(pa) or not os.path.lexists(pb):
                raise Refused(f"{pa} -> {pb} is next in {journal}, but {'both' if os.path.lexists(pa) else 'neither'} exist: look before re-running")
        elif not os.path.isdir(pa):
            os.mkdir(pa)
            os.chmod(pa, stat.S_IMODE(os.stat(pb).st_mode))
            own(pb, pa, None)
            sync_dir(os.path.dirname(pa))
        j["disk_next"] += 1
        save(journal, j)


def _sync_rel(raw):
    """A Syncthing folder path as a path below file-share/data, or None."""
    p = os.path.normpath("/var/syncthing" + raw[1:] if raw.startswith("~") else raw) if raw else ""
    if p == SYNC:
        return ""
    return p[len(SYNC) + 1:] if p.startswith(SYNC + "/") else None


def syncthing_repoint(text, moves):
    """Point the Syncthing folders on moved paths at the new ones, in
    config.xml's text; folder and device IDs stay, so the devices keep
    syncing without re-pairing. A folder that holds a moved path (its devices
    would delete those photos) or would hold one afterwards (they would get
    photos that are not theirs) is refused. -> (text, [(id, old, new)])."""
    root = ET.fromstring(text)
    changes = {}
    for f in root.findall("folder"):
        fid, raw = f.get("id"), f.get("path", "")
        rel = _sync_rel(raw)
        if rel is None:
            continue
        for src, _ in moves:
            if rel == "" or src.startswith(rel + "/"):
                raise Refused(f"Syncthing's folder {fid} ({raw}) holds {src}: its devices would delete those photos; repoint the folder by hand first")
        new = next((dst + rel[len(src):] for src, dst in moves if rel == src or rel.startswith(src + "/")), rel)
        for _, dst in moves:
            if dst.startswith(new + "/"):
                raise Refused(f"Syncthing's folder {fid} ({raw}) would hold {dst}: its devices would get those photos")
        if new == rel:
            continue
        vp = (f.findtext("versioning/fsPath") or "").strip()
        if vp and _sync_rel(vp) is not None and any(_sync_rel(vp) == s or _sync_rel(vp).startswith(s + "/") for s, _ in moves):
            raise Refused(f"Syncthing's folder {fid} keeps its versions in {vp}, which moves too: repoint it by hand first")
        changes[fid] = (raw, f"{SYNC}/{new}")

    def retag(m):
        tag = m.group(0)
        i = re.search(r'\sid=(["\'])(.*?)\1', tag)
        fid = i and ET.fromstring(f"<x id={i.group(1)}{i.group(2)}{i.group(1)}/>").get("id")
        if fid not in changes:
            return tag
        return re.sub(r'(\spath=)(["\'])(.*?)\2', lambda p: p.group(1) + p.group(2) + escape(changes[fid][1], {'"': "&quot;", "'": "&apos;"}) + p.group(2), tag, count=1)
    return re.sub(r"<folder\b[^>]*>", retag, text), [(k, *v) for k, v in changes.items()]


def lit(s):
    if "\0" in s:
        raise Refused("a NUL in a path")
    return "'" + s.replace("'", "''") + "'"


def copy_text(s):
    return s.replace("\\", "\\\\").replace("\t", "\\t").replace("\n", "\\n").replace("\r", "\\r")


class Immich:
    """Immich's Postgres through psql, on the socket of the Postgres the
    wrapper started on Immich's data (Immich itself is stopped)."""

    def __init__(self, sock):
        self.cmd = ["psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-h", sock, "-U", "postgres", "-d", "immich", "-f", "-"]

    def run(self, script, one=False):
        try:
            p = subprocess.run(self.cmd + (["-1"] if one else []), input=script, capture_output=True, text=True)
        except FileNotFoundError:
            raise Refused("no psql in this image: the laib-hooks image predates #263")
        if p.returncode:
            raise Refused("Immich's database: " + (p.stderr.strip().splitlines() or ["psql failed"])[-1])
        return p.stdout

    def value(self, sql):
        return json.loads(self.run(sql).strip() or "null")

    def libraries(self):
        v = self.value("""select json_build_object(
  'columns', (select count(*) from information_schema.columns where table_schema = current_schema() and (table_name, column_name) in
     (('library','importPaths'),('library','deletedAt'),('asset','originalPath'),('asset','libraryId'),('asset','checksum'),
      ('asset','checksumAlgorithm'),('asset_file','path'),('asset_file','assetId'))),
  'libraries', (select coalesce(json_agg(json_build_object('id', id, 'name', name, 'importPaths', "importPaths",
     'deleted', "deletedAt" is not null) order by "createdAt"), '[]'::json) from library))""")
        if v["columns"] != 8:
            raise Refused("not the Immich schema this move knows (v3: library.importPaths, asset.originalPath, asset_file.path)")
        return v["libraries"]


def mvalues(rewrites):
    return "(values " + ", ".join(f"({lit(l)}::uuid, {lit(s)}, {lit(os.path.normpath(s))}, {lit(n)})" for l, s, n in rewrites) + ") m(lib, stored, old, new)"


UNDER = 'left({col}, length(m.{p}) + 1) = m.{p} || \'/\''


def db_preflight(db, rewrites):
    """Counts per library, what follows the IDs, and the refusals: a path in a
    table this move does not rewrite, or an asset already at a new path."""
    m = mvalues(rewrites)
    old = UNDER.format(col='a."originalPath"', p="old")
    counts = db.value(f"""select coalesce(json_agg(json_build_object('lib', m.lib, 'stored', m.stored,
  'asset', (select count(*) from asset a where a."libraryId" = m.lib and {old}),
  'checksum', (select count(*) from asset a where a."libraryId" = m.lib and {old} and a."checksumAlgorithm" = 'sha1-path'),
  'asset_file', (select count(*) from asset_file f join asset a on a.id = f."assetId" where a."libraryId" = m.lib and {UNDER.format(col='f.path', p='old')}),
  'at_new', (select count(*) from asset a where a."libraryId" = m.lib and {UNDER.format(col='a."originalPath"', p='new')} and not {old}))), '[]'::json) from {m}""")
    mine = 'exists (select 1 from {m} where m.lib = a."libraryId" and {u})'
    guards = db.value(f"""select json_build_object(
  'asset of another library or an upload', (select count(*) from asset a where exists (select 1 from {m} where {old})
       and not {mine.format(m=m, u=old)}),
  'asset_file of another asset', (select count(*) from asset_file f join asset a on a.id = f."assetId" where exists (select 1 from {m} where {UNDER.format(col='f.path', p='old')})
       and not {mine.format(m=m, u=UNDER.format(col='f.path', p='old'))}),
  'person.thumbnailPath', (select count(*) from person p where exists (select 1 from {m} where {UNDER.format(col='p."thumbnailPath"', p='old')})),
  'user.profileImagePath', (select count(*) from "user" u where exists (select 1 from {m} where {UNDER.format(col='u."profileImagePath"', p='old')})),
  'move_history', (select count(*) from move_history h where exists (select 1 from {m} where {UNDER.format(col='h."oldPath"', p='old')} or {UNDER.format(col='h."newPath"', p='old')})))""")
    for c in counts:
        if c["at_new"]:
            raise Refused(f"library {c['lib']} already has {c['at_new']} assets under its new path: look before moving")
    for table, n in guards.items():
        if n:
            raise Refused(f"{n} rows in {table} name a library path this move rewrites only for the library's own assets: move by hand")
    try:
        kept = db.value(f"""with a as (select a.id, a."isFavorite" from asset a join {m} on a."libraryId" = m.lib and {old})
select json_build_object('album_asset', (select count(*) from album_asset x join a on a.id = x."assetId"),
  'asset_face', (select count(*) from asset_face x join a on a.id = x."assetId"),
  'favourites', (select count(*) from a where a."isFavorite"),
  'descriptions', (select count(*) from asset_exif x join a on a.id = x."assetId" where coalesce(x.description, '') <> ''),
  'asset_edit', (select count(*) from asset_edit x join a on a.id = x."assetId"))""")
    except Refused as e:
        kept = {"unknown": str(e)}
    sums = db.value(f"""select coalesce(json_agg(json_build_array(a.id, m.new || substr(a."originalPath", length(m.old) + 1))), '[]'::json)
  from asset a join {m} on a."libraryId" = m.lib and {old} where a."checksumAlgorithm" = 'sha1-path'""")
    return counts, kept, sums


def db_apply(db, rewrites, counts, sums):
    """One transaction: the asset paths, their files' (sidecars), the path
    checksums Immich would compute for the new paths (sha1 of "path:<path>",
    library.service processEntity), the import paths. Every UPDATE must hit
    exactly the rows the preflight counted, or nothing of it stays."""
    n_asset, n_file = sum(c["asset"] for c in counts), sum(c["asset_file"] for c in counts)
    rows = "\n".join(f"{i}\t{copy_text(p)}\t\\\\x{hashlib.sha1(('path:' + p).encode()).hexdigest()}" for i, p in sums)
    check = "GET DIAGNOSTICS n = ROW_COUNT; IF n <> {want} THEN RAISE EXCEPTION '{what}: % rows, % counted', n, {want}; END IF;"
    script = f"""LOCK TABLE library, asset, asset_file IN SHARE ROW EXCLUSIVE MODE;
CREATE TEMP TABLE laib_m (lib uuid, stored text, old text, new text) ON COMMIT DROP;
INSERT INTO laib_m SELECT * FROM {mvalues(rewrites)};
CREATE TEMP TABLE laib_c (id uuid PRIMARY KEY, path text NOT NULL, checksum bytea NOT NULL) ON COMMIT DROP;
COPY laib_c FROM STDIN;
{rows + chr(10) if rows else ''}\\.
DO $laib$
DECLARE n bigint;
BEGIN
  UPDATE asset a SET "originalPath" = m.new || substr(a."originalPath", length(m.old) + 1)
    FROM laib_m m WHERE a."libraryId" = m.lib AND {UNDER.format(col='a."originalPath"', p='old')};
  {check.format(want=n_asset, what='asset paths')}
  UPDATE asset_file f SET path = m.new || substr(f.path, length(m.old) + 1)
    FROM asset a, laib_m m WHERE f."assetId" = a.id AND a."libraryId" = m.lib AND {UNDER.format(col='f.path', p='old')};
  {check.format(want=n_file, what='asset files')}
  UPDATE asset a SET checksum = '\\x00'::bytea || uuid_send(a.id)
    FROM laib_c c WHERE a.id = c.id AND a."originalPath" = c.path AND a."checksumAlgorithm" = 'sha1-path';
  {check.format(want=len(sums), what='checksums')}
  UPDATE asset a SET checksum = c.checksum FROM laib_c c WHERE a.id = c.id;
  {check.format(want=len(sums), what='checksums')}
  UPDATE library l SET "importPaths" = ARRAY(SELECT coalesce(m.new, u.p) FROM unnest(l."importPaths") WITH ORDINALITY AS u(p, o)
      LEFT JOIN laib_m m ON m.lib = l.id AND m.stored = u.p ORDER BY u.o)
    WHERE l.id IN (SELECT lib FROM laib_m);
  {check.format(want=len({l for l, _, _ in rewrites}), what='libraries')}
END $laib$;
"""
    db.run(script, one=True)
    after = db.value(f"""select json_build_object(
  'old', (select count(*) from asset a join {mvalues(rewrites)} on a."libraryId" = m.lib and {UNDER.format(col='a."originalPath"', p='old')}),
  'new', (select count(*) from asset a join {mvalues(rewrites)} on a."libraryId" = m.lib and {UNDER.format(col='a."originalPath"', p='new')}))""")
    if after != {"old": 0, "new": n_asset}:
        raise Refused(f"after the transaction Immich's database shows {after}, {n_asset} expected under the new paths")


def db_state(libraries, rewrites):
    """'old' when every import path is still ServiceBay's, 'new' when every one
    is Laib's; anything else is refused."""
    by = {l["id"]: l["importPaths"] for l in libraries}
    seen = set()
    for lib, stored, new in rewrites:
        paths = by.get(lib, [])
        seen.add("old" if stored in paths and new not in paths else "new" if new in paths and stored not in paths else "?")
    if len(seen) != 1 or "?" in seen:
        raise Refused(f"Immich's import paths are neither all ServiceBay's nor all Laib's: {by}")
    return seen.pop()


def immich_plan(d, volumes, sock, dump, dry_run):
    steps, notes = [], []
    if not os.path.isdir(os.path.join(d, "immich", "pgdata")):
        raise Refused(f"no Immich database in {d}/immich/pgdata")
    base = os.path.join(d, "file-share", "data")
    journal = os.path.join(d, "immich", "library-move.json")
    st = os.path.join(d, "file-share", "syncthing-config", "config.xml")
    if not os.path.exists(st) and volumes and os.path.isdir(os.path.join(volumes, "file-share-syncthing-config")):
        raise Refused("Syncthing's config is still in ServiceBay's podman volume: move file-share first (docs/MIGRATION.md order), its folders on the photos follow them")
    if not sock:
        raise Refused("Immich's database is reached through scripts/move-servicebay-data.sh, which starts its Postgres: run that")
    db = Immich(sock)
    libraries = db.libraries()
    j = load(journal)
    if j.get("rewrites") and db_state(libraries, [tuple(r) for r in j["rewrites"]]) == "new":
        rewrites, moves, db_done = [tuple(r) for r in j["rewrites"]], [tuple(m) for m in j["moves"]], True
    else:
        rewrites, moves, more = photo_plan(libraries)
        notes += more
        db_done, j = False, {}
        if not rewrites:
            return [], notes + ["no library on ServiceBay's photo paths"]
        db_state(libraries, rewrites)
    if db_done and j.get("syncthing_done") and j.get("disk_next", -1) == len(j.get("disk", [None])):
        return [], [f"moved already ({journal})"]

    # the disk: one filesystem (renames, no copy), free names
    if "disk" not in j:
        if os.path.lexists(os.path.join(base, TMP_SHARED)):
            raise Refused(f"{base}/{TMP_SHARED} exists without a journal: look before moving")
        shared = any(src == "photos" for src, _ in moves)
        dev = os.stat(base).st_dev
        for src, dst in moves:
            if os.path.isdir(os.path.join(base, src)) and os.stat(os.path.join(base, src)).st_dev != dev:
                raise Refused(f"{base}/{src} is another filesystem: the photos are renamed, never copied")
            if not shared and os.path.lexists(os.path.join(base, dst)):
                raise Refused(f"{base}/{dst} exists: move it aside first")

    # Syncthing: from ServiceBay's version of config.xml once one is kept
    st_text = None
    if os.path.exists(st) and not j.get("syncthing_done"):
        with open(st + ".servicebay" if os.path.exists(st + ".servicebay") else st) as f:
            st_text, st_changes = syncthing_repoint(f.read(), moves)

    if not db_done:
        counts, kept, sums = db_preflight(db, rewrites)
        if not dry_run and not (dump and os.path.isfile(dump) and os.path.getsize(dump) > 0):
            raise Refused(f"no pg_dump of Immich's database at {dump}: the wrapper takes it first")
        names = {l["id"]: l["name"] for l in libraries}

        def apply_db():
            save(journal, {"rewrites": rewrites, "moves": moves})
            db_apply(db, rewrites, counts, sums)
        for i, ((lib, stored, new), c) in enumerate(zip(rewrites, counts)):
            steps.append((f"library {names.get(lib, lib)}: {stored} -> {new}: {c['asset']} asset paths, {c['checksum']} checksums, {c['asset_file']} asset files",
                          [], apply_db if i == len(rewrites) - 1 else None))
        notes.append("kept by asset id: " + ", ".join(f"{k} {v}" for k, v in kept.items()))
        notes.append(f"pg_dump before the move: {dump}")
    for i, (src, dst) in enumerate(moves):
        steps.append((f"move {base}/{src} -> {base}/{dst}", [], (lambda: move_photos(base, moves, journal)) if i == len(moves) - 1 else None))
    if st_text is not None:
        def apply_st():
            if not os.path.exists(st + ".servicebay"):
                shutil.copy2(st, st + ".servicebay")
            with open(st + ".tmp", "w") as f:
                f.write(st_text)
                f.flush()
                os.fsync(f.fileno())
            shutil.copystat(st, st + ".tmp")
            own(st, st + ".tmp", None)
            os.replace(st + ".tmp", st)
            jj = load(journal)
            jj["syncthing_done"] = True
            save(journal, jj)
        for fid, old, new in st_changes:
            steps.append((f"Syncthing folder {fid}: {old} -> {new}", [], None))
        steps.append(("Syncthing: config.xml written, ServiceBay's version kept beside it as config.xml.servicebay" if st_changes
                      else "Syncthing: no folder on the moved paths", [], apply_st))
    elif not os.path.exists(st):
        notes.append("no Syncthing config in file-share/syncthing-config: no folder to repoint")
    return steps, notes


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--data-dir", required=True)
    ap.add_argument("--quadlet", required=True, help="the Quadlet root ServiceBay's files sat in")
    ap.add_argument("--volumes", help="a directory with podman named volumes mounted by name")
    ap.add_argument("--active", help="the user units that run, as the wrapper read them from systemctl")
    ap.add_argument("--pg-socket", help="immich: the socket directory of the Postgres the wrapper started on Immich's data")
    ap.add_argument("--pg-dump", help="immich: the pg_dump the wrapper took before the move")
    ap.add_argument("--dry-run", action="store_true")
    ap.add_argument("services", nargs="+")
    a = ap.parse_args()
    rc = 0
    for svc in a.services:
        try:
            for unit in SERVICEBAY_UNITS.get(svc, [svc]):
                left = [f for f in os.listdir(a.quadlet) if os.path.splitext(f)[0] == unit and os.path.isfile(os.path.join(a.quadlet, f))]
                if left:
                    raise Refused(f"ServiceBay's {', '.join(sorted(left))} is still in the Quadlet root: stop the unit and move its files out first")
                if unit + ".service" in (a.active or "").split():
                    raise Refused(f"ServiceBay's {unit}.service still runs: systemctl --user stop {unit}.service first")
            for unit in QUIET_UNITS.get(svc, []):
                if unit + ".service" in (a.active or "").split():
                    raise Refused(f"{unit}.service still runs: systemctl --user stop {unit}.service first (start it again after the move)")
            steps, notes = plan(svc, a.data_dir, a.volumes, a)
            need, free = sum(size(p) for _, srcs, _ in steps for p in srcs), shutil.disk_usage(a.data_dir).free
            if need + RESERVE > free:
                raise Refused(f"the copy needs {need >> 20} MiB and {RESERVE >> 20} MiB to spare, {a.data_dir} has {free >> 20} MiB free")
        except (Refused, OSError, ValueError) as e:
            print(f"{svc}: REFUSED: {e}", file=sys.stderr)
            rc = 1
            continue
        try:
            for what, _, act in steps:
                print(f"{svc}: {what}" + ("   (dry run)" if a.dry_run else ""))
                if not a.dry_run and act:
                    act()
        except Refused as e:       # immich's transaction refused, or a journal step that does not fit
            print(f"{svc}: REFUSED: {e}", file=sys.stderr)
            rc = 1
            continue
        for n in notes:
            print(f"{svc}: {n}")
        if not steps:
            print(f"{svc}: nothing to move")
    return rc


if __name__ == "__main__":
    sys.exit(main())
