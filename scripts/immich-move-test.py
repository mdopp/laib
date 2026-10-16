#!/usr/bin/env python3
"""#263 for real: Immich's external libraries and their Syncthing folders move
from ServiceBay's layout into Laib's, through scripts/move-servicebay-data.sh.

The Kraftwerk's Immich (v3.3.1) and Postgres images run in a pod the way
ServiceBay ran them: all of file-share/data read-only at /mnt/photos, one
external library per user on /mnt/photos/<user>/photos and a Shared one on
/mnt/photos/photos. A second Syncthing stands in for the phones: it syncs the
box's camera folder and the shared photos. Albums, favourites, descriptions
(from XMP sidecars), a named person and an edit are made through
Immich's API. Then the mover runs (refused while Immich runs, a dry run, the
run, a second run) and Immich starts on Laib's mount (file-share/data/photos
at /mnt/photos). Immich's API must show the same asset IDs at the new paths
with all of it intact, a library rescan must add nothing and offline or
trash nothing, the box's Syncthing keeps its device and folder IDs on the new
paths with nothing deleted, a photo the peer adds afterwards arrives in the
new place and in Immich, and the dump the mover took restores.

Needs rootless podman and a user systemd (boot.yml's library-move job).
Leaves nothing behind."""
import json, os, re, subprocess, sys, tempfile, time, urllib.error, urllib.request
import xml.etree.ElementTree as ET

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SERVER = "ghcr.io/immich-app/immich-server:v3.3.1"                      # the Kraftwerk's, 2026-10-16
DB = "ghcr.io/immich-app/postgres:14-vectorchord0.4.3-pgvectors0.2.0"  # the Kraftwerk's and the built-in's
REDIS = "docker.io/library/redis:6.2-alpine"
SYNCTHING = "docker.io/syncthing/syncthing:latest"                     # the Kraftwerk's and the built-in's tag
HOOKS = os.environ.get("HOOKS_IMAGE", "localhost/laib-hooks:move-test")
API = "http://127.0.0.1:2283/api"
ST_A, ST_B = "http://127.0.0.1:18384/rest", "http://127.0.0.1:18385/rest"
PW = "move-test-only"          # a throwaway database in a throwaway runner

W = tempfile.mkdtemp(prefix="immich-move-")
D = os.path.join(W, "stacks")
FS = os.path.join(D, "file-share/data")
PG = os.path.join(D, "immich/pgdata")
UPLOAD = os.path.join(D, "immich/upload")
STCONF = os.path.join(D, "file-share/syncthing-config")   # file-share has moved already (MIGRATION.md order)
PEER = os.path.join(W, "peer")
QUADLET = os.path.join(W, "quadlet")
DUMP = os.path.join(D, "immich/before-library-move.dump")


def sh(*cmd, check=True, env=None, quiet=False):
    p = subprocess.run(cmd, capture_output=True, text=True, env=env)
    if check and p.returncode:
        sys.exit(f"FAIL: {' '.join(cmd)} -> {p.returncode}\n{p.stdout}\n{p.stderr}")
    if not quiet and p.returncode:
        print(f"  ({' '.join(cmd[:3])}… rc={p.returncode})")
    return p


def podman(*args, check=True):
    return sh("podman", *args, check=check)


def call(method, url, body=None, token=None, key=None):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(url, data=data, method=method)
    if data is not None:
        req.add_header("Content-Type", "application/json")
    if token:
        req.add_header("Authorization", "Bearer " + token)
    if key:
        req.add_header("X-API-Key", key)
    try:
        with urllib.request.urlopen(req, timeout=60) as r:
            raw = r.read()
    except urllib.error.HTTPError as e:
        sys.exit(f"FAIL: {method} {url}: HTTP {e.code}: {e.read()[:600]!r}")
    return json.loads(raw) if raw.strip() else None


def until(pred, what, timeout=300, every=2):
    end = time.time() + timeout
    last = None
    while time.time() < end:
        try:
            last = pred()
        except (urllib.error.URLError, ConnectionError, OSError, ValueError, KeyError, SystemExit) as e:
            last = f"{type(e).__name__}: {e}"
        else:
            if last:
                return last
        time.sleep(every)
    sys.exit(f"FAIL: {what} not within {timeout}s (last: {last})")


def check(cond, what, detail=None):
    if not cond:
        sys.exit(f"FAIL: {what}" + (f"\n  {json.dumps(detail, indent=1, default=str)[:4000]}" if detail is not None else ""))
    print(f"ok  {what}")


def tree(top):
    """Every entry under top: path, size, mtime. What a run that must change nothing is held to."""
    out = []
    for r, ds, fs in os.walk(top):
        for n in sorted(ds + fs):
            p = os.path.join(r, n)
            st = os.lstat(p)
            out.append((os.path.relpath(p, top), st.st_size if not os.path.isdir(p) else 0, st.st_mtime_ns))
    return sorted(out)


# ---- Immich -----------------------------------------------------------------

def immich_pod(name, photos):
    podman("pod", "create", "--name", name, "-p", "127.0.0.1:2283:2283")
    podman("run", "-d", "--pod", name, "--name", f"{name}-db", "-e", f"POSTGRES_PASSWORD={PW}", "-e", "POSTGRES_USER=postgres",
           "-e", "POSTGRES_DB=immich", "-e", "POSTGRES_INITDB_ARGS=--data-checksums", "-v", f"{PG}:/var/lib/postgresql/data", DB)
    podman("run", "-d", "--pod", name, "--name", f"{name}-redis", REDIS)
    podman("run", "-d", "--pod", name, "--name", f"{name}-server", "-e", "DB_HOSTNAME=127.0.0.1", "-e", "DB_USERNAME=postgres",
           "-e", f"DB_PASSWORD={PW}", "-e", "DB_DATABASE_NAME=immich", "-e", "DB_VECTOR_EXTENSION=vectorchord",
           "-e", "REDIS_HOSTNAME=127.0.0.1", "-e", "IMMICH_MACHINE_LEARNING_URL=http://127.0.0.1:3003",
           "-v", f"{UPLOAD}:/usr/src/app/upload", "-v", f"{photos}:/mnt/photos:ro", SERVER)
    until(lambda: call("GET", API + "/server/ping").get("res") == "pong", f"{name}: Immich answers", timeout=600, every=5)


def stop_pod(name):
    podman("pod", "stop", "-t", "60", name, check=False)
    podman("pod", "rm", "-f", name, check=False)


def psql(pod, sql):
    return podman("exec", f"{pod}-db", "psql", "-X", "-At", "-U", "postgres", "-d", "immich", "-c", sql).stdout.strip()


def idle(token):
    """Every queue empty, three polls in a row."""
    def once():
        q = call("GET", API + "/jobs", token=token)
        return all(v["jobCounts"]["active"] + v["jobCounts"]["waiting"] + v["jobCounts"].get("delayed", 0) == 0 for v in q.values())
    n = 0
    end = time.time() + 600
    while n < 3:
        if time.time() > end:
            sys.exit(f"FAIL: Immich's queues not idle: {call('GET', API + '/jobs', token=token)}")
        n = n + 1 if once() else 0
        time.sleep(2)


def login(email, password):
    return call("POST", API + "/auth/login", {"email": email, "password": password})["accessToken"]


def assets(token, lib):
    out, page = [], 1
    while page:
        r = call("POST", API + "/search/metadata", {"libraryId": lib, "withDeleted": True, "size": 1000, "page": page}, token)
        out += r["assets"]["items"]
        page = int(r["assets"]["nextPage"]) if r["assets"].get("nextPage") else None
    return out


def in_album(token, album):
    r = call("POST", API + "/search/metadata", {"albumIds": [album], "size": 1000}, token)
    return sorted(a["id"] for a in r["assets"]["items"])


def scan(admin, lib):
    before = call("GET", f"{API}/libraries/{lib}", token=admin).get("refreshedAt")
    call("POST", f"{API}/libraries/{lib}/scan", {}, admin)
    until(lambda: call("GET", f"{API}/libraries/{lib}", token=admin).get("refreshedAt") not in (None, before), f"library {lib} scanned")
    idle(admin)


def state(tokens, libs, albums):
    """What the acceptance is about, per asset ID, through Immich's API."""
    s = {"assets": {}, "albums": {}}
    for owner, lib in libs.items():
        for a in assets(tokens[owner], lib):
            d = call("GET", f"{API}/assets/{a['id']}", token=tokens[owner])
            faces = call("GET", f"{API}/faces?id={a['id']}", token=tokens[owner])
            edits = call("GET", f"{API}/assets/{a['id']}/edits", token=tokens[owner]).get("edits", [])
            s["assets"][a["id"]] = {
                "library": lib, "path": d["originalPath"], "favourite": d["isFavorite"],
                "description": (d.get("exifInfo") or {}).get("description") or "",
                "offline": d["isOffline"], "trashed": d["isTrashed"],
                "people": sorted((f.get("person") or {}).get("name", "") for f in faces),
                "edits": sorted((e["action"], json.dumps(e["parameters"], sort_keys=True)) for e in edits),
            }
    for owner, album in albums.items():
        s["albums"][album] = in_album(tokens[owner], album)
    return s


# ---- Syncthing ----------------------------------------------------------------

def syncthing(name, conf, data, gui):
    os.makedirs(conf, exist_ok=True)
    podman("run", "-d", "--name", name, "--network", "host", "-e", "PUID=0", "-e", "PGID=0", "-e", f"STGUIADDRESS=127.0.0.1:{gui}",
           "-e", "STNOUPNP=1", "-v", f"{data}:/var/syncthing/Sync", "-v", f"{conf}:/var/syncthing/config", SYNCTHING)
    key = until(lambda: ET.parse(os.path.join(conf, "config.xml")).getroot().findtext("gui/apikey"), f"{name}: config written", timeout=120)
    base = f"http://127.0.0.1:{gui}/rest"
    until(lambda: call("GET", base + "/system/ping", key=key).get("ping") == "pong", f"{name}: Syncthing answers", timeout=120)
    return base, key


def folder_status(base, key, fid):
    return call("GET", f"{base}/db/status?folder={fid}", key=key)


def settled(base, key, fid):
    s = folder_status(base, key, fid)
    return s if s["state"] == "idle" and s["needFiles"] == 0 and not s.get("errors") and not s.get("pullErrors") else None


def jpeg(path, colour, size="320x240"):
    """A real JPEG from the Immich image's own ffmpeg."""
    os.makedirs(os.path.dirname(path), exist_ok=True)
    podman("run", "--rm", "--entrypoint", "sh", "-v", f"{os.path.dirname(path)}:/out", SERVER, "-c",
           f'f=$(command -v ffmpeg || echo /usr/lib/jellyfin-ffmpeg/ffmpeg); "$f" -loglevel error -f lavfi -i color=c={colour}:s={size} -frames:v 1 /out/{os.path.basename(path)}')


SIDECAR = """<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
<rdf:Description rdf:about="" xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:description><rdf:Alt>
<rdf:li xml:lang="x-default">vom Sidecar</rdf:li></rdf:Alt></dc:description></rdf:Description></rdf:RDF></x:xmpmeta>
"""


def mover(*args):
    env = dict(os.environ, DATA_DIR=D, QUADLET=QUADLET, HOOKS_IMAGE=HOOKS, IMMICH_DB_IMAGE=DB)
    p = subprocess.run(["bash", os.path.join(ROOT, "scripts/move-servicebay-data.sh"), *args, "immich"], capture_output=True, text=True, env=env)
    print(f"--- move-servicebay-data.sh {' '.join(args)} immich -> {p.returncode}\n{p.stdout}{p.stderr}---")
    return p


def main():
    for d in (FS, UPLOAD, PG, QUADLET, os.path.join(PEER, "data")):
        os.makedirs(d, exist_ok=True)
    for img in (SERVER, DB, REDIS, SYNCTHING):
        podman("pull", "-q", img)
    sh("podman", "build", "-q", "-t", HOOKS, "-f", os.path.join(ROOT, "hooks/Containerfile"), os.path.join(ROOT, "hooks"))

    # ServiceBay's layout: the Shared library's area and alice's
    for p, c in (("photos/2019/s1.jpg", "red"), ("photos/2019/s2.jpg", "green"), ("photos/s3.jpg", "blue"),
                 ("alice/photos/Camera/a1.jpg", "yellow"), ("alice/photos/Camera/a2.jpg", "cyan"),
                 ("alice/photos/Camera/a3.jpg", "magenta"), ("alice/photos/a4.jpg", "white")):
        jpeg(os.path.join(FS, p), c)
    # descriptions as Immich keeps them for a read-only library: from XMP
    # sidecars. One typed into Immich does not survive Immich itself there
    # (v3.3.1: its sidecar write fails on the read-only mount and the
    # metadata re-read that follows clears it), on ServiceBay as on Laib.
    for p, text in (("alice/photos/a4.jpg.xmp", "vom Sidecar"), ("photos/s3.jpg.xmp", "Familienfoto")):
        with open(os.path.join(FS, p), "w") as f:
            f.write(SIDECAR.replace("vom Sidecar", text))
    os.makedirs(os.path.join(FS, "alice/documents"))
    with open(os.path.join(FS, "alice/documents/brief.txt"), "w") as f:
        f.write("stays where it is")

    # the box's Syncthing (A) and a phone (B): alice's camera folder and the family's photos
    a, ka = syncthing("mt-st-a", STCONF, FS, 18384)
    off = {"globalAnnounceEnabled": False, "localAnnounceEnabled": False, "relaysEnabled": False, "natEnabled": False,
           "urAccepted": -1, "crashReportingEnabled": False, "autoUpgradeIntervalH": 0}
    call("PATCH", a + "/config/options", dict(off, listenAddresses=["tcp://127.0.0.1:22100"]), key=ka)
    b, kb = syncthing("mt-st-b", os.path.join(PEER, "config"), os.path.join(PEER, "data"), 18385)
    call("PATCH", b + "/config/options", dict(off, listenAddresses=["tcp://127.0.0.1:22101"]), key=kb)
    ida = call("GET", a + "/system/status", key=ka)["myID"]
    idb = call("GET", b + "/system/status", key=kb)["myID"]
    call("POST", a + "/config/devices", {"deviceID": idb, "name": "phone", "addresses": ["tcp://127.0.0.1:22101"]}, key=ka)
    call("POST", b + "/config/devices", {"deviceID": ida, "name": "box", "addresses": ["tcp://127.0.0.1:22100"]}, key=kb)
    folders = {fid: (rel, sum(len(fs) for _, _, fs in os.walk(os.path.join(FS, rel))))
               for fid, rel in (("alice-cam", "alice/photos/Camera"), ("fotos", "photos"))}
    for fid, (rel, _) in folders.items():
        common = {"id": fid, "label": fid, "type": "sendreceive", "fsWatcherEnabled": False, "rescanIntervalS": 3600,
                  "devices": [{"deviceID": ida}, {"deviceID": idb}]}
        call("POST", a + "/config/folders", dict(common, path=f"/var/syncthing/Sync/{rel}"), key=ka)
        call("POST", b + "/config/folders", dict(common, path=f"/var/syncthing/Sync/{fid}"), key=kb)
    for fid, (_, n) in folders.items():
        until(lambda: (lambda s: s and s["localFiles"] == n)(settled(b, kb, fid)), f"the phone has {fid}", timeout=300)
    st_before = {fid: folder_status(a, ka, fid) for fid in folders}
    peer_before = {fid: folder_status(b, kb, fid) for fid in folders}

    # ServiceBay's Immich: the whole share at /mnt/photos, libraries as its disk import made them
    immich_pod("mt-sb-immich", FS)
    call("POST", API + "/auth/admin-sign-up", {"email": "admin@move.test", "password": PW, "name": "Admin"})
    admin = login("admin@move.test", PW)
    cfg = call("GET", API + "/system-config", token=admin)
    cfg["machineLearning"]["enabled"] = False          # no GPU here; faces are named by hand below
    call("PUT", API + "/system-config", cfg, admin)
    alice_id = call("POST", API + "/admin/users", {"email": "alice@move.test", "password": PW, "name": "alice"}, admin)["id"]
    alice = login("alice@move.test", PW)
    admin_id = call("GET", API + "/users/me", token=admin)["id"]
    shared = call("POST", API + "/libraries", {"ownerId": admin_id, "name": "Shared", "importPaths": ["/mnt/photos/photos"], "exclusionPatterns": []}, admin)["id"]
    mine = call("POST", API + "/libraries", {"ownerId": alice_id, "name": "alice", "importPaths": ["/mnt/photos/alice/photos"], "exclusionPatterns": []}, admin)["id"]
    for lib in (shared, mine):
        scan(admin, lib)
    tokens, libs = {"admin": admin, "alice": alice}, {"admin": shared, "alice": mine}
    by_path = {}
    for owner, lib in libs.items():
        for x in until(lambda: (lambda xs: xs if len(xs) == (3 if lib == shared else 4) else None)(assets(tokens[owner], lib)), f"library {lib} imported"):
            by_path[x["originalPath"]] = x["id"]
    check(len(by_path) == 7, "ServiceBay's Immich imported the 7 photos")
    aid = {p.rsplit("/", 1)[1].split(".")[0]: i for p, i in by_path.items()}
    until(lambda: all((call("GET", f"{API}/assets/{aid[k]}", token=alice).get("exifInfo") or {}).get("exifImageWidth") for k in ("a1", "a2", "a3", "a4"))
          and (call("GET", f"{API}/assets/{aid['s3']}", token=admin).get("exifInfo") or {}).get("description"),
          "the photos have their metadata, the sidecars' descriptions read")

    # what the household did in Immich
    holiday = call("POST", API + "/albums", {"albumName": "Urlaub", "assetIds": [aid["a1"], aid["a2"], aid["a4"]]}, alice)["id"]
    family = call("POST", API + "/albums", {"albumName": "Familie", "assetIds": [aid["s1"], aid["s3"]]}, admin)["id"]
    call("PUT", f"{API}/assets/{aid['a1']}", {"isFavorite": True}, alice)
    call("PUT", f"{API}/assets/{aid['s3']}", {"isFavorite": True}, admin)
    oma = call("POST", API + "/people", {"name": "Oma"}, alice)["id"]
    call("POST", API + "/faces", {"assetId": aid["a2"], "personId": oma, "imageWidth": 320, "imageHeight": 240,
                                  "x": 40, "y": 40, "width": 80, "height": 80}, alice)
    call("PUT", f"{API}/assets/{aid['a3']}/edits", {"edits": [{"action": "rotate", "parameters": {"angle": 90}}]}, alice)
    idle(admin)
    albums = {"alice": holiday, "admin": family}
    before = state(tokens, libs, albums)
    ba = before["assets"]
    check(ba[aid["a1"]]["favourite"] and ba[aid["s3"]]["favourite"], "two favourites", ba)
    check(ba[aid["s3"]]["description"] == "Familienfoto" and ba[aid["a4"]]["description"] == "vom Sidecar", "two descriptions from sidecars", ba)
    check(ba[aid["a2"]]["people"] == ["Oma"], "a named person", ba)
    check(ba[aid["a3"]]["edits"] == [("rotate", '{"angle": 90}')], "an edit", ba)
    check(len(before["albums"][holiday]) == 3 and len(before["albums"][family]) == 2, "two albums", before["albums"])
    check(psql("mt-sb-immich", f"""select count(*) from asset_file where type = 'sidecar' and path = '/mnt/photos/alice/photos/a4.jpg.xmp'""") == "1",
          "the sidecar is an asset file on ServiceBay's path")

    # the mover refuses while ServiceBay's Immich has its database open
    p = mover()
    check(p.returncode != 0 and "REFUSED" in p.stderr and os.path.isdir(os.path.join(FS, "alice/photos")) and not os.path.exists(DUMP),
          "refused while ServiceBay's Immich runs, nothing moved, no dump")

    stop_pod("mt-sb-immich")
    podman("stop", "-t", "30", "mt-st-a")
    snap = tree(FS)

    p = mover("--dry-run")
    out = p.stdout + p.stderr
    check(p.returncode == 0, "the dry run")
    check(re.search(r"library alice: /mnt/photos/alice/photos -> /mnt/photos/alice: 4 asset paths, 4 checksums, 1 asset files", out) is not None, "the dry run counts alice's library")
    check(re.search(r"library Shared: /mnt/photos/photos -> /mnt/photos/shared: 3 asset paths, 3 checksums, 1 asset files", out) is not None, "the dry run counts the Shared library")
    check("kept by asset id: album_asset 5, asset_face 1, favourites 2, descriptions 2, asset_edit 1" in out, "the dry run counts what follows the IDs")
    check(tree(FS) == snap and not os.path.exists(DUMP), "the dry run moved nothing and dumped nothing")

    p = mover()
    check(p.returncode == 0, "the move")
    check(os.path.getsize(DUMP) > 0, "a pg_dump was taken first")
    check(sorted(os.listdir(os.path.join(FS, "photos"))) == ["alice", "shared"], "photos/ holds one folder per library")
    check(not os.path.exists(os.path.join(FS, "alice/photos")) and os.path.exists(os.path.join(FS, "alice/documents/brief.txt")),
          "alice's photos left her folder, her documents stayed")
    snap = tree(FS)
    p = mover()
    check(p.returncode == 0 and tree(FS) == snap, "a second run changes nothing")

    # Laib's built-in: only file-share/data/photos at /mnt/photos
    immich_pod("mt-laib-immich", os.path.join(FS, "photos"))
    admin, alice = login("admin@move.test", PW), login("alice@move.test", PW)
    tokens = {"admin": admin, "alice": alice}
    check(call("GET", f"{API}/libraries/{shared}", token=admin)["importPaths"] == ["/mnt/photos/shared"], "Shared imports /mnt/photos/shared")
    check(call("GET", f"{API}/libraries/{mine}", token=admin)["importPaths"] == ["/mnt/photos/alice"], "alice's imports /mnt/photos/alice")
    want = {}
    for i, v in before["assets"].items():
        p = v["path"]
        want[i] = dict(v, path="/mnt/photos/shared/" + p[len("/mnt/photos/photos/"):] if p.startswith("/mnt/photos/photos/") else "/mnt/photos/alice/" + p[len("/mnt/photos/alice/photos/"):])
    after = state(tokens, libs, albums)
    check(after["assets"] == want, "the same asset IDs at the new paths, favourites, descriptions, people and edits intact", {"want": want, "got": after["assets"]})
    check(after["albums"] == before["albums"], "the albums hold the same assets")
    check(psql("mt-laib-immich", "select count(*) from asset_file where type = 'sidecar' and path = '/mnt/photos/alice/a4.jpg.xmp'") == "1", "the sidecar's path moved too")
    for lib in (shared, mine):
        scan(admin, lib)
    again = state(tokens, libs, albums)
    check(again == after, "a rescan of both libraries adds nothing, offlines nothing, trashes nothing", again)
    check(psql("mt-laib-immich", f"""select count(*), count(*) filter (where "isOffline" or "deletedAt" is not null) from asset where "libraryId" in ('{shared}', '{mine}')""") == "7|0",
          "the database holds 7 assets, none offline or deleted")

    # the box's Syncthing: same device, same folders, new paths, nothing deleted
    podman("start", "mt-st-a")
    until(lambda: call("GET", a + "/system/ping", key=ka).get("ping") == "pong", "the box's Syncthing answers again", timeout=120)
    check(call("GET", a + "/system/status", key=ka)["myID"] == ida, "the box keeps its device ID")
    paths = {f["id"]: f["path"] for f in call("GET", a + "/config/folders", key=ka)}
    check(paths == {"alice-cam": "/var/syncthing/Sync/photos/alice/Camera", "fotos": "/var/syncthing/Sync/photos/shared"},
          "the folders keep their IDs and point at the new paths")
    for fid in folders:
        call("POST", f"{a}/db/scan?folder={fid}", key=ka)
        s = until(lambda: settled(a, ka, fid), f"the box's {fid} is idle without errors", timeout=180)
        old = st_before[fid]
        check((s["localFiles"], s["localDeleted"], s["sequence"]) == (old["localFiles"], old["localDeleted"], old["sequence"]),
              f"{fid}: the same files, nothing deleted, nothing re-announced")
        until(lambda: call("GET", f"{a}/db/completion?folder={fid}&device={idb}", key=ka).get("completion") == 100, f"{fid} in sync with the phone", timeout=180)
        pb = settled(b, kb, fid)
        check(pb and (pb["localFiles"], pb["localDeleted"]) == (peer_before[fid]["localFiles"], peer_before[fid]["localDeleted"]),
              f"{fid}: the phone deleted nothing")

    # a photo the phone takes now lands in the new place, and in Immich
    jpeg(os.path.join(PEER, "data/alice-cam/a5.jpg"), "orange")
    call("POST", f"{b}/db/scan?folder=alice-cam", key=kb)
    until(lambda: os.path.exists(os.path.join(FS, "photos/alice/Camera/a5.jpg")), "the phone's new photo arrives in photos/alice/Camera", timeout=180)
    check(not os.path.exists(os.path.join(FS, "alice/photos")), "nothing is recreated at the old path")
    scan(admin, mine)
    check(any(x["originalPath"] == "/mnt/photos/alice/Camera/a5.jpg" for x in assets(alice, mine)) and len(assets(alice, mine)) == 5,
          "Immich imports it into alice's library")

    # the dump restores, with ServiceBay's paths in it
    stop_pod("mt-laib-immich")
    podman("run", "-d", "--name", "mt-restore", "-v", f"{PG}:/var/lib/postgresql/data", "-v", f"{DUMP}:/dump:ro", DB)
    until(lambda: podman("exec", "mt-restore", "pg_isready", "-q", "-U", "postgres", check=False).returncode == 0, "Postgres for the restore", timeout=120)
    podman("exec", "mt-restore", "createdb", "-U", "postgres", "restore_check")
    podman("exec", "mt-restore", "pg_restore", "-U", "postgres", "-d", "restore_check", "--no-owner", "/dump", check=False)
    n = podman("exec", "mt-restore", "psql", "-X", "-At", "-U", "postgres", "-d", "restore_check", "-c",
               """select count(*) from asset where "originalPath" like '/mnt/photos/photos/%' or "originalPath" like '/mnt/photos/alice/photos/%'""").stdout.strip()
    check(n == "7", "the dump restores Immich's database as it was before the move")
    print("immich-move-test: all green")


def cleanup():
    for c in ("mt-st-a", "mt-st-b", "mt-restore"):
        podman("rm", "-f", "-t", "5", c, check=False)
    for pod in ("mt-sb-immich", "mt-laib-immich"):
        podman("pod", "rm", "-f", "-t", "5", pod, check=False)
    sh("podman", "unshare", "rm", "-rf", W, check=False)


if __name__ == "__main__":
    try:
        main()
    except SystemExit:
        for c in podman("ps", "-a", "--format", "{{.Names}}", check=False).stdout.split():
            if c.startswith("mt-"):
                p = podman("logs", "--tail", "40", c, check=False)
                print(f"--- podman logs --tail 40 {c}\n{p.stdout}{p.stderr}", flush=True)
        raise
    finally:
        cleanup()
