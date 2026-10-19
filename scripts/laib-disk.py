#!/usr/bin/env python3
"""#456: the disk import's copy tool, the mechanics under the Werkstatt skill
`importing-an-old-disk` (werkstatt/guide/skills/): the agent judges what goes
where and asks; this copies, the same way every time. Outside the kernel.

  laib-disk.py scan <mounted disk> [--json]
      what is on it, by kind (photos, documents, music, other), what is skipped,
      and where each kind goes on this box
  laib-disk.py copy <from> <to> [--kind photos|documents|music|other|all] [--go]
      without --go a dry run (the default): what it would copy, skip, rename

The copy never overwrites (a different file of the same name, also only in
case, gets "name (2).ext"), never writes to the source, keeps the folders below
<from>, skips what <to> already holds by content under any name and what is on
the disk twice, verifies each copy's sha256 after an fsync before it gets its
name, and journals one line per run (verb import). A run cut off (the disk
pulled) leaves no partial file and copies the rest on the next run.

From ServiceBay's disk-import worker (packages/disk-import-worker): the junk
list, keeping the source folders, never deleting, ownership like the target
(as root, i.e. under `podman unshare`, a new file and folder get the owner of
the folder they land in), a scan that tolerates unreadable folders. Better
than it: a full sha256, never a sampled fingerprint (a false match there left
a photo silently on the disk); $RECYCLE.BIN and System Volume Information
skipped; @eaDir matched in any case; sidecars (.xmp, .aae) and a Live Photo's
video kept with their photo; symlinks reported, never followed.
Stdlib only; runs on the host as core: `podman unshare python3 scripts/laib-disk.py …`.
"""
import argparse, hashlib, json, os, sys, time
from pathlib import Path

PHOTO = {"jpg", "jpeg", "heic", "heif", "png", "gif", "tif", "tiff", "webp", "dng", "cr2", "cr3", "nef", "arw", "raf", "orf", "rw2"}
VIDEO = {"mov", "mp4", "m4v", "avi", "mkv", "3gp", "mts", "m2ts"}
SIDECAR = {"xmp", "aae"}
DOCUMENT = {"pdf", "doc", "docx", "odt", "rtf", "txt", "xls", "xlsx", "ods", "ppt", "pptx", "odp", "csv", "md"}
MUSIC = {"mp3", "flac", "m4a", "aac", "ogg", "opus", "wav", "wma", "aiff", "aif", "alac"}
JUNK_NAMES = {"thumbs.db", ".ds_store", "desktop.ini", ".localized", "ehthumbs.db", "icon\r"}
JUNK_EXT = {"tmp", "temp", "part", "crdownload", "laib-part"}
JUNK_DIRS = {".trash", ".trashes", ".trash-1000", ".spotlight-v100", ".fseventsd", ".temporaryitems", "@eadir", "__macosx",
             "$recycle.bin", "recycler", "system volume information", "lost+found", ".git", "node_modules"}
DATA = os.environ.get("LAIB_DATA_DIR", "/mnt/data/stacks")
TARGETS = {"photos": f"{DATA}/file-share/data/photos",                 # Immich's external library (PHOTOS_RO)
           "documents": f"{DATA}/file-share/data/paperless-consume",   # Paperless's inbox (CONSUME): it files and removes them
           "music": f"{DATA}/file-share/data/music",                   # beets sorts it (MUSIC)
           "other": f"{DATA}/file-share/data/documents"}               # the share's documents folder, backed up
CHUNK = 1 << 20


def ext(name):
    return name.rsplit(".", 1)[-1].lower() if "." in name else ""


def stem(name):
    return name.rsplit(".", 1)[0].lower()


def walk(root):
    """(files: [(rel, size)], symlinks, skipped count); unreadable folders are skipped, not fatal"""
    files, links, skipped = [], [], 0
    for d, dirs, names in os.walk(root, onerror=lambda e: None):
        keep = []
        for x in sorted(dirs):
            p = os.path.join(d, x)
            if os.path.islink(p):
                links.append(os.path.relpath(p, root))
            elif x.lower() in JUNK_DIRS:
                skipped += 1
            else:
                keep.append(x)
        dirs[:] = keep
        for n in sorted(names):
            p = os.path.join(d, n)
            if os.path.islink(p):
                links.append(os.path.relpath(p, root))
            elif n.lower() in JUNK_NAMES or ext(n) in JUNK_EXT or n.startswith("._"):
                skipped += 1
            else:
                try:
                    files.append((os.path.relpath(p, root), os.stat(p).st_size))
                except OSError:
                    skipped += 1
    return files, links, skipped


def classify(files):
    """rel → kind; a sidecar or a video beside a photo of its name, or in a folder of photos, is a photo"""
    photos_in = {}
    for rel, _ in files:
        if ext(rel) in PHOTO:
            photos_in.setdefault(os.path.dirname(rel), set()).add(stem(os.path.basename(rel)))
    kinds = {}
    for rel, _ in files:
        e, folder = ext(rel), photos_in.get(os.path.dirname(rel), set())
        if e in PHOTO or (e in SIDECAR | VIDEO and (stem(os.path.basename(rel)) in folder or (e in VIDEO and folder))):
            kinds[rel] = "photos"
        elif e in DOCUMENT:
            kinds[rel] = "documents"
        elif e in MUSIC:
            kinds[rel] = "music"
        else:
            kinds[rel] = "other"
    return kinds


def sha(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        while b := f.read(CHUNK):
            h.update(b)
    return h.hexdigest()


def scan(args):
    files, links, skipped = walk(args.src)
    kinds = classify(files)
    out = {"source": args.src, "kinds": {}, "symlinks": links, "skipped": skipped, "targets": TARGETS}
    for rel, size in files:
        k = out["kinds"].setdefault(kinds[rel], {"files": 0, "bytes": 0, "folders": {}})
        k["files"] += 1
        k["bytes"] += size
        top = rel.split(os.sep, 1)[0] if os.sep in rel else "."
        k["folders"][top] = k["folders"].get(top, 0) + 1
    if args.json:
        print(json.dumps(out, indent=1, ensure_ascii=False))
        return 0
    for kind, k in sorted(out["kinds"].items()):
        tops = ", ".join(f"{t} ({n})" for t, n in sorted(k["folders"].items(), key=lambda x: -x[1])[:6])
        print(f"{kind:10} {k['files']:6} Dateien {k['bytes'] / 1e9:8.2f} GB  → {TARGETS[kind]}  [{tops}]")
    print(f"übersprungen: {skipped} (Systemdateien, Papierkorb, Vorschaubilder); Verknüpfungen, nicht verfolgt: {len(links)}")
    return 0


def free_name(folder, name, taken):
    """name, or "name (2).ext" … when folder has it already, in any case"""
    base, dot, e = name.rpartition(".")
    if not dot:
        base, e = name, ""
    cand, n = name, 1
    while cand.lower() in taken:
        n += 1
        cand = f"{base} ({n}){dot}{e}"
    return cand


def journal(result, detail, src, dest):
    home = os.environ.get("LAIB_HOME", "/mnt/data/laib")
    actor = os.environ.get("LAIB_ACTOR") or "human:" + os.environ.get("USER", "?")
    line = {"t": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()), "actor": actor, "verb": "import", "reason": f"{src} → {dest}", "result": result, "detail": detail}
    try:
        with open(os.path.join(home, "journal", time.strftime("%Y-%m-%d", time.gmtime()) + ".jsonl"), "a") as f:
            f.write(json.dumps(line, ensure_ascii=False) + "\n")
    except OSError as e:
        print(f"journal: {e}", file=sys.stderr)


def like_parent(path):
    if os.geteuid() == 0:   # under podman unshare: the owner of the folder it lands in
        st = os.stat(os.path.dirname(path))
        os.chown(path, st.st_uid, st.st_gid)


def copy(args):
    src, dest = os.path.realpath(args.src), os.path.realpath(args.dest)
    if dest == src or dest.startswith(src + os.sep) or src.startswith(dest + os.sep):
        print(f"{dest} is inside the source or holds it: refused", file=sys.stderr)
        return 2
    files, links, _ = walk(src)
    kinds = classify(files)
    todo = [(rel, size) for rel, size in files if args.kind == "all" or kinds[rel] == args.kind]
    # what the target holds, by size; hashed only where a size matches
    have, hashes = {}, {}
    if os.path.isdir(dest):
        for rel, size in walk(dest)[0]:
            have.setdefault(size, []).append(os.path.join(dest, rel))
    def there(size, h):
        for p in have.get(size, []):
            if p not in hashes:
                try:
                    hashes[p] = sha(p)
                except OSError:
                    hashes[p] = ""
            if hashes[p] == h:
                return p
        return None
    plan, dupes, renamed, seen, names = [], [], 0, {}, {}
    for rel, size in todo:
        h = sha(os.path.join(src, rel))
        if h in seen or there(size, h):
            dupes.append(rel)
            continue
        seen[h] = rel
        folder = os.path.join(dest, os.path.dirname(rel))
        if folder not in names:
            names[folder] = {n.lower() for n in os.listdir(folder)} if os.path.isdir(folder) else set()
        name = free_name(folder, os.path.basename(rel), names[folder])
        renamed += name != os.path.basename(rel)
        names[folder].add(name.lower())
        plan.append((rel, os.path.join(folder, name), h, size))
    total = sum(p[3] for p in plan)
    print(f"{'dry run: ' if not args.go else ''}{len(plan)} to copy ({total / 1e6:.1f} MB), {len(dupes)} already there or twice on the disk, {renamed} renamed (a different file had the name), {len(links)} symlinks not followed")
    for rel, target, _, _ in plan[: 0 if args.go else 50]:
        print(f"  {rel} → {target}")
    if not args.go:
        print("nothing written; run again with --go")
        return 0
    done = 0
    for rel, target, h, size in plan:
        part = os.path.join(os.path.dirname(target), "." + os.path.basename(target) + ".laib-part")
        try:
            made = []
            d = os.path.dirname(target)
            while not os.path.isdir(d):
                made.append(d)
                d = os.path.dirname(d)
            for d in reversed(made):
                os.mkdir(d)
                like_parent(d)
            with open(os.path.join(src, rel), "rb") as fi, open(part, "xb") as fo:
                while b := fi.read(CHUNK):
                    fo.write(b)
                fo.flush()
                os.fsync(fo.fileno())
            if sha(part) != h:
                raise OSError(f"checksum differs after the copy: {rel}")
            st = os.stat(os.path.join(src, rel))
            os.utime(part, (st.st_atime, st.st_mtime))
            like_parent(part)
            os.link(part, target)   # never replaces: fails when the name exists
            os.unlink(part)
            done += 1
        except OSError as e:
            if os.path.exists(part):
                os.unlink(part)
            detail = f"{done} kopiert, abgebrochen bei {rel}: {e}; ein neuer Lauf kopiert den Rest"
            print(detail, file=sys.stderr)
            journal("red", detail, src, dest)
            return 1
    detail = f"{done} kopiert ({total / 1e6:.1f} MB), {len(dupes)} schon da, {renamed} umbenannt, Art {args.kind}"
    print(detail)
    journal("green", detail, src, dest)
    return 0


def main():
    a = argparse.ArgumentParser(description="the disk import's copy tool (#456)")
    sub = a.add_subparsers(dest="cmd", required=True)
    s = sub.add_parser("scan")
    s.add_argument("src")
    s.add_argument("--json", action="store_true")
    c = sub.add_parser("copy")
    c.add_argument("src")
    c.add_argument("dest")
    c.add_argument("--kind", default="all", choices=["photos", "documents", "music", "other", "all"])
    c.add_argument("--go", action="store_true")
    args = a.parse_args()
    return scan(args) if args.cmd == "scan" else copy(args)


if __name__ == "__main__":
    sys.exit(main())
