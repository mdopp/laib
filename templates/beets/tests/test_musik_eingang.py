"""#639: the Musik-Eingang. musik-eingang.py runs inside the beets container
(beets-eingang.service, `podman exec -i … python3 -`) and takes what the
family drops into the share's musik-eingang folder into the library with
`beet import -q`. Here against a fake `beet` that moves audio files into a
fake library the way `import.move: yes` does, and logs like beets' import
log; every name is made up."""
import fcntl, json, os, subprocess, sys, tempfile, textwrap, unittest, zipfile
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent.parent / "musik-eingang.py"

# The fake beet: `beet -c <overlay> import -q [-s] <path>`. Audio files go to
# <BIB>/, except a name with "doppelt" (beets' duplicate-skip) or "kaputt"
# (an error, exit 1). The call and the overlay are kept for the asserts.
FAKE_BEET = textwrap.dedent(r'''
    import json, os, sys
    args = sys.argv[1:]
    overlay = open(args[args.index("-c") + 1]).read()
    log = [l.split(":", 1)[1].strip() for l in overlay.splitlines() if l.strip().startswith("log:")][0]
    bib = os.environ["FAKE_BIB"]
    with open(os.environ["FAKE_CALLS"], "a") as f:
        f.write(json.dumps({"args": args, "overlay": overlay}) + "\n")
    pfad = args[-1]
    audio = (".mp3", ".flac", ".m4a", ".aac", ".ogg", ".opus", ".wav")
    files = [pfad] if os.path.isfile(pfad) else [os.path.join(w, n) for w, _, ns in os.walk(pfad) for n in ns]
    rc = 0
    with open(log, "a") as lf:
        for f in sorted(files):
            if not f.lower().endswith(audio):
                continue
            if "doppelt" in f:
                lf.write("duplicate-skip " + os.path.dirname(f) + "\n")
                continue
            if "kaputt" in f:
                print("error reading " + f + ": not a valid audio file", file=sys.stderr)
                rc = 1
                continue
            ziel = os.path.join(bib, os.path.relpath(f, os.path.dirname(pfad.rstrip("/"))))
            os.makedirs(os.path.dirname(ziel), exist_ok=True)
            os.rename(f, ziel)
    sys.exit(rc)
''')


class MusikEingang(unittest.TestCase):
    def setUp(self):
        t = tempfile.TemporaryDirectory()
        self.addCleanup(t.cleanup)
        self.root = Path(t.name)
        self.eingang, self.config, self.bib = self.root / "eingang", self.root / "config", self.root / "bib"
        for d in (self.eingang, self.config, self.bib):
            d.mkdir()
        (self.root / "beet.py").write_text(FAKE_BEET)
        self.calls = self.root / "calls.jsonl"

    def run_it(self, stable="0", **extra):
        env = dict(os.environ, MUSIK_EINGANG=str(self.eingang), BEETS_DATA=str(self.config),
                   BEET=f"{sys.executable} {self.root / 'beet.py'}", MUSIK_STABIL_S=stable,
                   FAKE_BIB=str(self.bib), FAKE_CALLS=str(self.calls), **extra)
        with open(SCRIPT) as f:   # as the unit runs it: the script on stdin
            p = subprocess.run([sys.executable, "-"], stdin=f, env=env, capture_output=True, text=True, timeout=60)
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        return p

    def twice(self, **extra):
        """first sight, then the same size and mtime again: stable"""
        self.run_it(**extra)
        return self.run_it(**extra)

    def status(self):
        return json.loads((self.config / "laib-status.json").read_text())

    def calls_made(self):
        return [json.loads(l) for l in self.calls.read_text().splitlines()] if self.calls.exists() else []

    def nicht_importiert(self):
        ni = self.eingang / "_nicht-importiert"
        return sorted(str(p.relative_to(ni)) for p in ni.rglob("*") if p.is_file()) if ni.exists() else []

    def left_in_inbox(self):
        return sorted(str(p.relative_to(self.eingang)) for p in self.eingang.rglob("*")
                      if "_nicht-importiert" not in p.parts)

    def test_a_single_file_goes_in_as_a_singleton_quietly_as_is(self):
        (self.eingang / "Erfundene Band - Lied.mp3").write_bytes(b"ID3" + b"x" * 100)
        self.twice()
        self.assertTrue((self.bib / "Erfundene Band - Lied.mp3").exists())
        self.assertEqual(self.left_in_inbox(), [], "the inbox keeps what was imported")
        c = self.calls_made()
        self.assertEqual(len(c), 1)
        self.assertIn("-q", c[0]["args"])
        self.assertIn("-s", c[0]["args"], "a loose song is a singleton, not a one-track album")
        for want in ("quiet_fallback: asis", "move: yes", "duplicate_action: skip"):
            self.assertIn(want, c[0]["overlay"])
        s = self.status()
        self.assertEqual(s["detail"], "Musik-Eingang: zuletzt 1 Titel importiert")
        self.assertEqual(s["letzte"][0]["titel"], 1)
        self.assertEqual(s["letzte"][0]["nicht_importiert"], [])
        self.assertEqual(s["findings"], [])

    def test_a_nested_album_folder_goes_in_whole_and_its_folders_leave(self):
        for cd in ("CD1", "CD2"):
            d = self.eingang / "Erfundenes Album" / cd
            d.mkdir(parents=True)
            for n in (1, 2):
                (d / f"{n:02d} Titel.flac").write_bytes(b"fLaC" + b"y" * 50)
        self.twice()
        self.assertEqual(len(list(self.bib.rglob("*.flac"))), 4)
        self.assertEqual(self.left_in_inbox(), [], "empty folders stay behind")
        c = self.calls_made()
        self.assertEqual(len(c), 1)
        self.assertNotIn("-s", c[0]["args"], "an album folder is imported as albums")
        s = self.status()
        self.assertEqual((s["letzte"][0]["titel"], s["letzte"][0]["alben"]), (4, 2))
        self.assertEqual(s["detail"], "Musik-Eingang: zuletzt 4 Titel (2 Alben) importiert")

    def test_a_zip_is_unpacked_imported_and_removed(self):
        with zipfile.ZipFile(self.eingang / "Erfundenes Album.zip", "w") as z:
            z.writestr("Erfundenes Album/01 Eins.mp3", b"ID3" + b"a" * 40)
            z.writestr("Erfundenes Album/02 Zwei.mp3", b"ID3" + b"b" * 40)
            z.writestr("__MACOSX/Erfundenes Album/._01 Eins.mp3", b"meta")
        self.twice()
        self.assertEqual(len(list(self.bib.rglob("*.mp3"))), 2)
        self.assertEqual(self.left_in_inbox(), [], "the zip stays in the inbox after its music went in")
        self.assertEqual(self.nicht_importiert(), [])

    def test_a_zip_with_path_traversal_is_refused_whole_and_kept(self):
        with zipfile.ZipFile(self.eingang / "boese.zip", "w") as z:
            z.writestr("gut.mp3", b"ID3" + b"g" * 10)
            z.writestr("../../ausserhalb.mp3", b"ID3" + b"x" * 10)
        self.twice()
        self.assertFalse((self.root / "ausserhalb.mp3").exists())
        self.assertFalse(any(self.root.rglob("ausserhalb.mp3")), "a member escaped the inbox")
        self.assertEqual(list(self.bib.rglob("*")), [], "nothing of a refused zip is imported")
        ni = self.nicht_importiert()
        self.assertTrue(any(p.endswith("/boese.zip") for p in ni), ni)
        grund = next((self.eingang / "_nicht-importiert").glob("*/GRUND.txt")).read_text()
        self.assertIn("boese.zip", grund)
        self.assertIn("unsicher", grund)
        s = self.status()
        self.assertEqual(s["letzte"][0]["nicht_importiert"][0]["datei"], "boese.zip")
        self.assertIn("unsicher", s["letzte"][0]["nicht_importiert"][0]["grund"])

    def test_a_zip_with_an_absolute_path_or_a_symlink_is_refused(self):
        with zipfile.ZipFile(self.eingang / "absolut.zip", "w") as z:
            z.writestr("/etc/erfunden.mp3", b"ID3")
        with zipfile.ZipFile(self.eingang / "verweis.zip", "w") as z:
            i = zipfile.ZipInfo("link.mp3")
            i.external_attr = (0o120777 << 16)
            z.writestr(i, "/etc/passwd")
        self.twice()
        self.assertEqual(list(self.bib.rglob("*")), [])
        ni = self.nicht_importiert()
        self.assertTrue(any(p.endswith("/absolut.zip") for p in ni) and any(p.endswith("/verweis.zip") for p in ni), ni)

    def test_a_zip_over_the_caps_is_refused(self):
        with zipfile.ZipFile(self.eingang / "riesig.zip", "w") as z:
            for n in range(5):
                z.writestr(f"{n}.mp3", b"ID3" + b"z" * 1000)
        self.twice(MUSIK_ZIP_MAX_DATEIEN="3")
        self.assertEqual(list(self.bib.rglob("*")), [])
        self.assertTrue(any(p.endswith("/riesig.zip") for p in self.nicht_importiert()))
        with zipfile.ZipFile(self.eingang / "gross.zip", "w") as z:
            z.writestr("a.mp3", b"ID3" + b"z" * 1000)
        self.twice(MUSIK_ZIP_MAX_BYTES="100")
        self.assertEqual(list(self.bib.rglob("*")), [])
        self.assertTrue(any(p.endswith("/gross.zip") for p in self.nicht_importiert()))

    def test_what_is_not_music_is_kept_with_a_reason_in_german(self):
        (self.eingang / "Einkaufsliste.docx").write_bytes(b"PK..")
        (self.eingang / "Erfundenes Album.rar").write_bytes(b"Rar!")
        a = self.eingang / "Erfundenes Album"
        a.mkdir()
        (a / "01 Eins.mp3").write_bytes(b"ID3" + b"1" * 20)
        (a / "cover.jpg").write_bytes(b"\xff\xd8")
        self.twice()
        self.assertTrue((self.bib / "Erfundenes Album" / "01 Eins.mp3").exists())
        ni = self.nicht_importiert()
        self.assertEqual(sorted(Path(p).name for p in ni),
                         ["Einkaufsliste.docx", "Erfundenes Album.rar", "GRUND.txt", "cover.jpg"], ni)
        self.assertTrue(any(p.endswith("Erfundenes Album/cover.jpg") for p in ni), "the folder's shape is kept")
        grund = next((self.eingang / "_nicht-importiert").glob("*/GRUND.txt")).read_text()
        self.assertIn("Einkaufsliste.docx: keine Musikdatei", grund)
        self.assertIn("Erfundenes Album.rar: kann ich nicht auspacken", grund)
        self.assertEqual(self.left_in_inbox(), [], "nothing stays in the inbox itself")
        s = self.status()
        self.assertEqual(s["detail"], "Musik-Eingang: zuletzt 1 Titel (1 Album) importiert, 3 Dateien nicht importiert")
        self.assertEqual(sum(len(x["nicht_importiert"]) for x in s["letzte"]), 3)
        self.assertEqual({f["severity"] for f in s["findings"]}, {"info"})
        self.assertIn("_nicht-importiert/", s["findings"][0]["fix"])

    def test_a_duplicate_is_skipped_never_overwritten_and_kept(self):
        (self.eingang / "doppelt.mp3").write_bytes(b"ID3" + b"d" * 20)
        self.twice()
        self.assertEqual(list(self.bib.rglob("*")), [])
        ni = self.nicht_importiert()
        self.assertTrue(any(p.endswith("/doppelt.mp3") for p in ni), ni)
        grund = next((self.eingang / "_nicht-importiert").glob("*/GRUND.txt")).read_text()
        self.assertIn("schon in der Musiksammlung", grund)

    def test_a_failed_import_keeps_the_file_and_puts_the_raw_error_into_einzelheiten(self):
        (self.eingang / "kaputt.mp3").write_bytes(b"ID3")
        self.twice()
        self.assertTrue(any(p.endswith("/kaputt.mp3") for p in self.nicht_importiert()))
        s = self.status()
        self.assertIn("error reading", s["einzelheiten"])
        self.assertNotIn("error reading", s["detail"], "a raw error in the family's line (#588)")
        self.assertEqual(s["findings"][0]["severity"], "gelb")
        self.assertIn("error reading", s["findings"][0]["einzelheiten"])

    def test_a_file_still_being_written_is_not_taken(self):
        f = self.eingang / "Lied.flac"
        f.write_bytes(b"fLaC" + b"1" * 10)
        self.run_it()
        with open(f, "ab") as h:            # the copy goes on between two runs
            h.write(b"2" * 10)
        self.run_it()
        self.assertEqual(self.calls_made(), [], "a growing file was imported")
        self.assertIn("wird noch kopiert", self.status()["detail"])
        self.run_it()
        self.assertTrue((self.bib / "Lied.flac").exists())

    def test_a_folder_still_filling_is_not_taken(self):
        a = self.eingang / "Album"
        a.mkdir()
        (a / "01.mp3").write_bytes(b"ID3")
        self.run_it()
        (a / "02.mp3").write_bytes(b"ID3")
        self.run_it()
        self.assertEqual(self.calls_made(), [], "a folder that grew between two looks was imported")
        self.run_it()
        self.assertEqual(len(list(self.bib.rglob("*.mp3"))), 2)

    def test_a_staged_upload_is_ignored_until_renamed(self):
        (self.eingang / "._Lied.mp3.part").write_bytes(b"ID3" + b"p" * 10)
        (self.eingang / "Album").mkdir()
        (self.eingang / "Album" / "01.mp3").write_bytes(b"ID3")
        (self.eingang / "Album" / "._02.mp3.part").write_bytes(b"ID3")
        self.twice()
        self.assertEqual(self.calls_made(), [], "an upload still staged was taken")
        self.assertTrue((self.eingang / "._Lied.mp3.part").exists())
        os.rename(self.eingang / "._Lied.mp3.part", self.eingang / "Lied.mp3")
        os.rename(self.eingang / "Album" / "._02.mp3.part", self.eingang / "Album" / "02.mp3")
        self.twice()
        self.assertEqual(len(list(self.bib.rglob("*.mp3"))), 3)

    def test_the_default_wait_is_fifteen_seconds(self):
        (self.eingang / "Lied.mp3").write_bytes(b"ID3")
        self.run_it(stable="15")
        self.run_it(stable="15")
        self.assertEqual(self.calls_made(), [], "taken before it held still for 15 s")

    def test_a_second_run_waits_for_the_lock(self):
        (self.eingang / "Lied.mp3").write_bytes(b"ID3")
        self.run_it()
        lock = self.config / "musik-eingang" / "lauf.lock"
        with open(lock, "w") as h:
            fcntl.flock(h, fcntl.LOCK_EX)
            p = self.run_it()
            self.assertIn("läuft schon", p.stdout)
            self.assertEqual(self.calls_made(), [], "a second run imported beside the first")
        self.run_it()
        self.assertEqual(len(self.calls_made()), 1)

    def test_an_interrupted_run_leaves_nothing_behind_unseen(self):
        w = self.eingang / ".in-arbeit" / "2026-01-01_00-00-00-1" / "Album"
        w.mkdir(parents=True)
        (w / "01.mp3").write_bytes(b"ID3")
        self.run_it()
        self.assertTrue(any(p.endswith("Album/01.mp3") for p in self.nicht_importiert()), self.nicht_importiert())
        self.assertFalse((self.eingang / ".in-arbeit" / "2026-01-01_00-00-00-1").exists())
        grund = next((self.eingang / "_nicht-importiert").glob("*/GRUND.txt")).read_text()
        self.assertIn("unterbrochen", grund)

    def test_an_empty_inbox_says_ready(self):
        self.run_it()
        s = self.status()
        self.assertEqual(s["detail"], "Musik-Eingang: bereit, noch nichts importiert")
        self.assertEqual((s["letzte"], s["findings"]), ([], []))
        self.assertTrue(s["at"].endswith("Z"))


if __name__ == "__main__":
    unittest.main()
