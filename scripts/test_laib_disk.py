"""#456: scripts/laib-disk.py, the deterministic half of the disk import (the
Werkstatt skill is the judging half): scan an attached disk, classify, then
copy one kind into its target, dry run by default. Never overwrites, verifies
each copy by its sha256, skips what the target already has (by content, under
any name), keeps sidecars with their photos, skips the junk of four operating
systems, and journals one line per batch. Edge cases from ServiceBay's
disk-import worker (packages/disk-import-worker) and the ones it missed."""
import hashlib, json, os, subprocess, tempfile, unittest
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent / "laib-disk.py"


def put(p, data):
    p.parent.mkdir(parents=True, exist_ok=True)
    p.write_bytes(data if isinstance(data, bytes) else data.encode())


class LaibDisk(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        t = self.t = Path(self.tmp.name)
        src = self.src = t / "disk"
        put(src / "DCIM/2019/IMG_0001.JPG", b"jpeg-one")
        put(src / "DCIM/2019/IMG_0001.xmp", b"<xmp/>")                # a sidecar goes with its photo
        put(src / "DCIM/2019/IMG_0002.HEIC", b"heic-two")
        put(src / "DCIM/2019/IMG_0002.MOV", b"live-photo-motion")     # a Live Photo's video goes with it
        put(src / "DCIM/2019/copy/IMG_0001.JPG", b"jpeg-one")         # the same photo twice on the disk
        put(src / "DCIM/2019/Thumbs.db", b"junk")
        put(src / "DCIM/2019/@eaDir/IMG_0001.JPG/SYNOPHOTO_THUMB.jpg", b"thumb")
        put(src / "Briefe/Steuer 2020.pdf", b"%PDF-steuer")
        put(src / "Musik/Band/01 Lied.mp3", b"mp3-data")
        put(src / "System Volume Information/IndexerVolumeGuid", b"x")
        put(src / "$RECYCLE.BIN/S-1-5/$R123.jpg", b"deleted")
        put(src / "__MACOSX/._IMG_0001.JPG", b"resource-fork")
        put(src / "Sonstiges/notes.bin", b"\x00\x01")
        os.symlink(src / "Briefe", src / "link-to-briefe")
        self.dest = t / "share" / "photos"
        self.dest.mkdir(parents=True)
        self.home = t / "laib"
        (self.home / "journal").mkdir(parents=True)

    def tearDown(self):
        self.tmp.cleanup()

    def run_it(self, *args):
        env = dict(os.environ, LAIB_HOME=str(self.home), LAIB_ACTOR="claude")
        return subprocess.run(["python3", str(SCRIPT), *args], capture_output=True, text=True, env=env)

    def files(self, root):
        return sorted(str(p.relative_to(root)) for p in root.rglob("*") if p.is_file())

    def journal(self):
        lines = []
        for f in (self.home / "journal").glob("*.jsonl"):
            lines += [json.loads(l) for l in f.read_text().splitlines()]
        return lines

    def test_scan_classifies_and_skips_the_junk(self):
        r = self.run_it("scan", str(self.src), "--json")
        self.assertEqual(r.returncode, 0, r.stderr)
        s = json.loads(r.stdout)
        self.assertEqual(s["kinds"]["photos"]["files"], 6)          # two photos, their sidecar and motion, the copy
        self.assertEqual(s["kinds"]["documents"]["files"], 1)
        self.assertEqual(s["kinds"]["music"]["files"], 1)
        self.assertEqual(s["kinds"]["other"]["files"], 1)
        self.assertIn("link-to-briefe", " ".join(s["symlinks"]))
        for junk in ("Thumbs.db", "SYNOPHOTO_THUMB.jpg", "IndexerVolumeGuid", "$R123.jpg", "._IMG_0001.JPG"):
            self.assertNotIn(junk, json.dumps(s["kinds"]))
        self.assertGreaterEqual(s["skipped"], 5)

    def test_the_dry_run_is_the_default_and_writes_nothing(self):
        r = self.run_it("copy", str(self.src), str(self.dest), "--kind", "photos")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(self.files(self.dest), [])
        self.assertIn("dry run", r.stdout)
        self.assertIn("5 to copy", r.stdout)                         # the second IMG_0001.JPG is the same content
        self.assertEqual(self.journal(), [])

    def test_copy_verifies_keeps_folders_and_journals_one_line(self):
        r = self.run_it("copy", str(self.src), str(self.dest), "--kind", "photos", "--go")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertEqual(self.files(self.dest), ["DCIM/2019/IMG_0001.JPG", "DCIM/2019/IMG_0001.xmp", "DCIM/2019/IMG_0002.HEIC", "DCIM/2019/IMG_0002.MOV"])
        self.assertEqual((self.dest / "DCIM/2019/IMG_0002.HEIC").read_bytes(), b"heic-two")
        self.assertEqual(self.files(self.src).count("DCIM/2019/IMG_0001.JPG"), 1)   # the source is never touched
        j = self.journal()
        self.assertEqual(len(j), 1)
        self.assertEqual((j[0]["verb"], j[0]["actor"], j[0]["result"]), ("import", "claude", "green"))
        self.assertIn("4 kopiert", j[0]["detail"])
        self.assertFalse([p for p in self.dest.rglob("*.laib-part")], "no partial file is left")

    def test_a_second_run_copies_nothing(self):
        self.run_it("copy", str(self.src), str(self.dest), "--kind", "photos", "--go")
        r = self.run_it("copy", str(self.src), str(self.dest), "--kind", "photos", "--go")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertIn("0 to copy", r.stdout)
        self.assertEqual(len(self.files(self.dest)), 4)

    def test_content_already_there_under_another_name_is_skipped(self):
        put(self.dest / "Alt/urlaub.heic", b"heic-two")
        r = self.run_it("copy", str(self.src), str(self.dest), "--kind", "photos", "--go")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertFalse((self.dest / "DCIM/2019/IMG_0002.HEIC").exists())
        self.assertIn("already there", r.stdout)

    def test_never_overwrites_a_different_file_of_the_same_name(self):
        put(self.dest / "DCIM/2019/IMG_0001.JPG", b"another photo")
        put(self.dest / "DCIM/2019/img_0002.heic", b"a third one")     # a case-insensitive client would see one name
        r = self.run_it("copy", str(self.src), str(self.dest), "--kind", "photos", "--go")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertEqual((self.dest / "DCIM/2019/IMG_0001.JPG").read_bytes(), b"another photo")
        self.assertEqual((self.dest / "DCIM/2019/IMG_0001 (2).JPG").read_bytes(), b"jpeg-one")
        self.assertEqual((self.dest / "DCIM/2019/IMG_0002 (2).HEIC").read_bytes(), b"heic-two")

    def test_a_target_outside_the_data_tree_needs_no_guess(self):
        r = self.run_it("copy", str(self.src), str(self.src / "inside"), "--kind", "all")
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("inside the source", r.stdout + r.stderr)

    def test_every_kind_has_a_proposed_target(self):
        r = self.run_it("scan", str(self.src), "--json")
        s = json.loads(r.stdout)
        self.assertEqual(s["targets"]["photos"], "/mnt/data/stacks/file-share/data/photos")
        self.assertEqual(s["targets"]["documents"], "/mnt/data/stacks/file-share/data/paperless-consume")
        self.assertEqual(s["targets"]["music"], "/mnt/data/stacks/file-share/data/music")


if __name__ == "__main__":
    unittest.main()
