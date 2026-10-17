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
  werkstatt       the git checkouts of <D>/claude-dev/workspace and
                  <D>/pi-web/workspace -> <D>/werkstatt/workspace (pi-web's
                  under pi-web-<name> where claude-dev has the same name)

It copies, it never moves: ServiceBay's paths stay as they were, so handing a
service back is re-enabling its unit; they go with ServiceBay's data dir after
30 days. The two files it edits in place (zwave-js settings.json, Home
Assistant's core.config_entries) keep ServiceBay's version beside them as
<file>.servicebay. A service whose ServiceBay file is still in the Quadlet
root, whose ServiceBay unit still runs (--active, from the wrapper), whose
target already holds data or whose copy does not fit is refused before
anything of it is written. Each target is copied into <target>.partial and
renamed once synced: it is whole or absent.

Runs as root in the laib-hooks image (scripts/move-servicebay-data.sh), which
is root over the box's container uids: ownership is kept, the Werkstatt's
checkouts go to its dev user (1000).

Usage: move-servicebay-data.py --data-dir D --quadlet Q [--volumes V] [--active UNITS] [--dry-run] <service>...
"""
import argparse, json, os, shutil, stat, sys

ZWAVE_PORT = 3000          # zwave-js-ui's default; nothing on a Laib box holds it
WERKSTATT_UID = 1000       # werkstatt/Containerfile: useradd -u 1000 dev
SERVICEBAY_UNITS = {"werkstatt": ["claude-dev", "pi-web"]}
RESERVE = 1 << 30          # a data disk filled to the last byte wedges every service on it


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


def plan(service, d, volumes):
    """(description, action) steps for one service; raises Refused."""
    steps, notes = [], []      # (what, the sources it copies, action)

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


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--data-dir", required=True)
    ap.add_argument("--quadlet", required=True, help="the Quadlet root ServiceBay's files sat in")
    ap.add_argument("--volumes", help="a directory with podman named volumes mounted by name")
    ap.add_argument("--active", help="the user units that run, as the wrapper read them from systemctl")
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
            steps, notes = plan(svc, a.data_dir, a.volumes)
            need, free = sum(size(p) for _, srcs, _ in steps for p in srcs), shutil.disk_usage(a.data_dir).free
            if need + RESERVE > free:
                raise Refused(f"the copy needs {need >> 20} MiB and {RESERVE >> 20} MiB to spare, {a.data_dir} has {free >> 20} MiB free")
        except (Refused, OSError, ValueError) as e:
            print(f"{svc}: REFUSED: {e}", file=sys.stderr)
            rc = 1
            continue
        for what, _, act in steps:
            print(f"{svc}: {what}" + ("   (dry run)" if a.dry_run else ""))
            if not a.dry_run:
                act()
        for n in notes:
            print(f"{svc}: {n}")
        if not steps:
            print(f"{svc}: nothing to move")
    return rc


if __name__ == "__main__":
    sys.exit(main())
