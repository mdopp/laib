---
name: importing-an-old-disk
description: "Importing an old disk: someone plugged a USB disk or an old drive into the box and wants its photos, documents and music in the household's apps (Immich, Paperless, the music folder, the file share). Read this before you copy anything: you judge what goes where and ask; scripts/laib-disk.py does the copying, dry run first."
---

# Importing an old disk

**Answer first:** you decide *what goes where* and ask the human where you
are unsure; `scripts/laib-disk.py` does the copy, the same way every time:
dry run first, never overwriting, every file checksum-verified, what the box
already has skipped by content, one journal line per run. You never copy by
hand (`cp`, `rsync`) and never without the dry run and the human's yes.

## Where you run it

The tool runs **on the box's host, as core**, inside podman's user namespace
(the share's folders belong to the containers' users):

```sh
podman unshare python3 ~/laib/scripts/laib-disk.py …
```

In the Werkstatt you have no host shell. Then give the human each command in
one German sentence, wait, and read what they paste back. With a host shell
(an SSH session as core) you run them yourself.

## The steps

**1. Find and mount the disk, read-only.** The human plugs it in.

```sh
lsblk -o NAME,SIZE,FSTYPE,LABEL,MOUNTPOINT,RM     # the disk: RM 1, its partition with an FSTYPE
sudo mkdir -p /var/mnt/import && sudo mount -o ro /dev/sdX1 /var/mnt/import
```

Always `-o ro`: nothing may change on the old disk. NTFS and exFAT mount
read-only like any other; if the mount refuses, say what it said, do not
force it.

**2. Look.** `laib-disk.py scan /var/mnt/import` lists, per kind, the files,
the size, the biggest folders and the target on this box:

| Kind | Target | What happens there |
|---|---|---|
| photos (with their .xmp/.aae and a Live Photo's video) | `file-share/data/photos` | Immich's external library; it shows them after its library scan |
| documents (pdf, office, text) | `file-share/data/paperless-consume` | Paperless files them and **removes them from the folder** |
| music | `file-share/data/music` | beets tags and sorts it |
| other | `file-share/data/documents` | the share's documents folder, backed up |

**3. Propose a plan and ask.** One German message: what is on the disk,
where you would put each part, and where you are unsure. Typical questions:
whose photos are these (a folder per person below `photos/`, e.g.
`photos/oma`)? Are the documents for Paperless (it reads and files every one)
or just to keep (then `documents/<name>`)? A folder of videos with no photos
is "other": ask whether it is a film for the media library. Ask once, with
your recommendation first.

**4. Dry run, per kind.** Without `--go` nothing is written:

```sh
podman unshare python3 ~/laib/scripts/laib-disk.py copy /var/mnt/import/DCIM /mnt/data/stacks/file-share/data/photos/oma --kind photos
```

It prints how many files it would copy, how many are already there (by
content, under any name) or twice on the disk, and which get a new name
because a different file already has theirs (`name (2).jpg`; nothing is ever
overwritten). Show the human the counts and anything surprising.

**5. Copy, after the human's yes.** The same line with `--go`. A run that is
cut off (the disk pulled) leaves no partial file; the same command again
copies the rest and nothing twice.

**6. Check it arrived, then unmount.** `laib journal --since 1h` shows the
`import` line with the counts. Photos: Immich shows them after its external
library scan (Immich → Administration → External Libraries → Scan, or the
nightly one). Documents: they vanish from `paperless-consume` and appear in
Paperless. Then `sudo umount /var/mnt/import`.

## What the tool already handles (do not redo it)

Junk of four systems is skipped (Thumbs.db, .DS_Store, `._` files,
`$RECYCLE.BIN`, `System Volume Information`, `@eaDir`, `.Trashes`,
`lost+found` …); symlinks are reported, never followed; unreadable folders
are skipped, not fatal; the source folders are kept below the target; new
files and folders get the owner of the folder they land in.
This came from ServiceBay's disk-import worker
(`/workspace/servicebay/packages/disk-import-worker`).
