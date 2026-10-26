"""#727: the Bestand. musik-bestand.py runs inside the beets container
(beets-bestand.service, `podman exec -i … python3 -`, every 20 minutes of
quiet from beets-bestand.timer) and sorts what an earlier run left
unsorted: the songs in „Unbekanntes Album“/„Unknown“ folders, everything
under „Unbekannter Künstler“, the nameless files at the music folder's top.
Here against a fake `beet` and a fake library tree. Every name is made up.

The fake `beet -c <overlay> import -q -s --set laib_bestand=<id> <file>`:
a file whose name holds "treffer" is a sure match and goes to
<music>/Single Tracks/<artist> - <stem><ext> (the singleton path), "doppelt"
is beets' duplicate-skip, "kaputt" an error (exit 1), anything else is
skipped as not sure, written to the import log as beets does.
`beet -c <overlay> ls -p laib_bestand::<regex>` answers the moved paths."""
import json, os, re, subprocess, sys, tempfile, textwrap, unittest
from pathlib import Path

HIER = Path(__file__).resolve().parent.parent
SCRIPT = HIER / "musik-bestand.py"
EINGANG_SCRIPT = HIER / "musik-eingang.py"

FAKE_BEET = textwrap.dedent(r'''
    import json, os, re, sys
    args = sys.argv[1:]
    db = os.environ["FAKE_DB"]
    items = json.load(open(db)) if os.path.exists(db) else []
    if "ls" in args:
        q = args[-1]
        feld, _, rx = q.partition("::")
        assert feld == "laib_bestand", q
        for it in items:
            if re.search(rx, it["laib_bestand"]):
                print(it["path"] if "-p" in args else it["laib_bestand"])
        sys.exit(0)
    overlay = open(args[args.index("-c") + 1]).read()
    with open(os.environ["FAKE_CALLS"], "a") as f:
        f.write(json.dumps({"args": args, "overlay": overlay}) + "\n")
    log = [l.split(":", 1)[1].strip() for l in overlay.splitlines() if l.strip().startswith("log:")][0]
    musik = [l.split(":", 1)[1].strip() for l in overlay.splitlines() if l.startswith("directory:")][0]
    setze = args[args.index("--set") + 1].split("=", 1)[1]
    pfad = args[-1]
    name = os.path.basename(pfad)
    with open(log, "a") as lf:
        if "kaputt" in name:
            print("error reading " + pfad, file=sys.stderr)
            sys.exit(1)
        if "doppelt" in name:
            lf.write("duplicate-skip " + pfad + "\n")
            sys.exit(0)
        if "treffer" not in name:
            lf.write("skip " + pfad + "\n")
            sys.exit(0)
    stamm, ext = os.path.splitext(name)
    ziel = os.path.join(musik, "Single Tracks", "Erfundene Band - " + stamm + ext)
    os.makedirs(os.path.dirname(ziel), exist_ok=True)
    os.rename(pfad, ziel)
    items.append({"path": ziel, "laib_bestand": setze})
    json.dump(items, open(db, "w"))
''')

VORSCHLAG = "Erfundener Komiker; Erfundenes Hörspiel; Gibt Es Nicht"


class Bestand(unittest.TestCase):
    def setUp(self):
        t = tempfile.TemporaryDirectory()
        self.addCleanup(t.cleanup)
        self.root = Path(t.name)
        self.musik, self.hoer, self.config = self.root / "music", self.root / "audiobooks", self.root / "config"
        for d in (self.musik, self.hoer, self.config):
            d.mkdir()
        (self.root / "beet.py").write_text(FAKE_BEET)
        self.calls = self.root / "calls.jsonl"
        self.baum()

    def schreib(self, rel, inhalt=b"ID3xx"):
        p = self.musik / rel
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_bytes(inhalt)
        return p

    def baum(self):
        """the fake library: what an unfinished download script and a quiet skip run leave behind"""
        self.schreib("Erfundene Sängerin/Unbekanntes Album/01 Erstes treffer.mp3")
        self.schreib("Erfundene Sängerin/Unbekanntes Album/01 Erstes treffer.lrc", b"[00:01]la")
        self.schreib("Erfundene Sängerin/Unbekanntes Album/02 Zweites.mp3")
        self.schreib("Erfundene Sängerin/Unbekanntes Album/02 Zweites.txt", b"la la")
        self.schreib("Erfundene Sängerin/Unbekanntes Album/folder.jpg", b"JPG")
        self.schreib("Erfundene Sängerin/Richtiges Album/01 Bleibt.mp3")
        self.schreib("Erfundene Sängerin/artist.nfo", b"<artist/>")
        self.schreib("Unbekannter Künstler/Album inconnu (10_11_2008)/a treffer.mp3")
        self.schreib("Unbekannter Künstler/Vom Leben/b doppelt.mp3")
        self.schreib("Unbekannter Künstler/02 Titel 02 (3).mp3")
        self.schreib("Erfundene Band/Unknown/c kaputt.flac", b"fLaC")
        self.schreib("Erfundene Band/Unknown Pleasures/01 echt.mp3")
        self.schreib("Erfundene Band/Untitled - New CD (5)/u treffer.mp3")
        self.schreib("__.1.mp3")
        self.schreib("__.1.lrc", b"[00:01]x")
        self.schreib("__.2 treffer.mp3")
        self.schreib("__.2 treffer.lrc", b"[00:01]y")
        self.schreib("download_log.txt", b"START: x")
        self.schreib("pipeline_progress_log.txt", b"1/2")
        self.schreib("cover.jpg", b"JPG")
        (self.musik / "_downloads").mkdir()
        self.schreib("simplemp3s/Erfundener Mix/m treffer.mp3")
        self.schreib("simplemp3s/artist.nfo", b"<artist/>")
        self.schreib("Erfundener Komiker/Unknown/witz.mp3")
        self.schreib("Erfundener Komiker/Live/01 witz.mp3")
        self.schreib("Erfundenes Hörspiel/Folge 1/01 teil.mp3")
        (self.hoer / "Erfundener Komiker" / "Altes Programm").mkdir(parents=True)
        (self.hoer / "Erfundener Komiker" / "Altes Programm" / "01.mp3").write_bytes(b"ID3")
        (self.hoer / "Erfundener Komiker" / "Live").mkdir()
        (self.hoer / "Erfundener Komiker" / "Live" / "01 witz.mp3").write_bytes(b"ID3 schon da")

    def env(self, **extra):
        e = dict(os.environ, BESTAND_MUSIK=str(self.musik), BESTAND_HOERBUECHER=str(self.hoer), BEETS_DATA=str(self.config),
                 BEET=f"{sys.executable} {self.root / 'beet.py'}", FAKE_DB=str(self.root / "db.json"),
                 FAKE_CALLS=str(self.calls), BESTAND_GESPROCHEN_VORSCHLAG=VORSCHLAG, BESTAND_GESPROCHEN="")
        e.update(extra)
        return e

    def run_it(self, script=SCRIPT, **extra):
        with open(script) as f:   # as the unit runs it: the script on stdin
            p = subprocess.run([sys.executable, "-"], stdin=f, env=self.env(**extra), capture_output=True, text=True, timeout=60)
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        return p

    def bis_fertig(self, **extra):
        for _ in range(20):
            self.run_it(**extra)
            if self.stand().get("fertig"):
                return
        self.fail("the Bestand never finished: %s" % self.stand())

    def stand(self):
        return json.loads((self.config / "bestand" / "stand.json").read_text())

    def calls_made(self):
        return [json.loads(l) for l in self.calls.read_text().splitlines()] if self.calls.exists() else []

    def alle(self):
        """every file under music and audiobooks, by content: nothing may go missing"""
        return sorted(p.read_bytes() for d in (self.musik, self.hoer) for p in d.rglob("*")
                      if p.is_file() and p.name not in ("LIESMICH.txt", ".ignore"))

    def test_it_works_in_chunks_and_resumes_where_the_tree_stands(self):
        self.run_it(BESTAND_JE_LAUF="2")
        self.assertEqual(len(self.calls_made()), 2, "a run takes at most BESTAND_JE_LAUF files")
        s = self.stand()
        self.assertFalse(s["fertig"])
        self.assertEqual(s["gesamt"], 10, "every unsorted song is counted, the spoken word and the sorted albums not")
        self.run_it(BESTAND_JE_LAUF="2")
        self.assertEqual(len(self.calls_made()), 4, "the next run goes on, it does not start over")
        self.bis_fertig(BESTAND_JE_LAUF="2")
        importiert = [c["args"][-1] for c in self.calls_made()]
        self.assertEqual(len(importiert), len(set(importiert)) + 2, "only the broken file is tried again (three tries)")

    def test_a_sure_match_is_moved_by_beets_with_its_lyrics_and_nothing_is_deleted(self):
        vorher = self.alle()
        self.bis_fertig()
        self.assertEqual(self.alle(), vorher, "a file went missing or was doubled")
        st = self.musik / "Single Tracks"
        self.assertTrue((st / "Erfundene Band - 01 Erstes treffer.mp3").exists())
        self.assertEqual((st / "Erfundene Band - 01 Erstes treffer.lrc").read_bytes(), b"[00:01]la",
                         "the lyrics follow the song under its new name")
        self.assertTrue((st / "Erfundene Band - __.2 treffer.lrc").exists())
        self.assertTrue((st / "Erfundene Band - m treffer.mp3").exists(), "the download script's folder is music too")
        for c in self.calls_made():
            self.assertIn("-q", c["args"])
            self.assertIn("-s", c["args"], "songs from an unknown album are singletons, not one album")
            for want in ("move: yes", "copy: no", "quiet_fallback: skip", "duplicate_action: skip",
                         "ratelimit: 1", "ratelimit_interval: 3.0", "incremental: no"):
                self.assertIn(want, c["overlay"])
            self.assertNotIn("remove", c["overlay"], "beets must never delete a copy")
        self.assertTrue((self.musik / "Erfundene Sängerin/Richtiges Album/01 Bleibt.mp3").exists(), "a sorted album is left alone")
        self.assertTrue((self.musik / "Erfundene Band/Unknown Pleasures/01 echt.mp3").exists(), "an album called Unknown … is an album")
        self.assertTrue((st / "Erfundene Band - u treffer.mp3").exists())
        self.assertTrue((self.musik / "cover.jpg").exists(), "the library's own picture stays")

    def test_what_is_not_sure_goes_to_unklar_by_artist_with_the_reason(self):
        self.bis_fertig()
        u = self.musik / "_unklar"
        self.assertTrue((u / "Erfundene Sängerin/Unbekanntes Album/02 Zweites.mp3").exists())
        self.assertTrue((u / "Erfundene Sängerin/Unbekanntes Album/02 Zweites.txt").exists(), "its lyrics beside it")
        self.assertTrue((u / "Erfundene Sängerin/Unbekanntes Album/folder.jpg").exists(), "the rest of the folder too")
        self.assertTrue((u / "Unbekannter Künstler/Vom Leben/b doppelt.mp3").exists())
        self.assertTrue((u / "Unbekannter Künstler/02 Titel 02 (3).mp3").exists())
        self.assertTrue((u / "Erfundene Band/Unknown/c kaputt.flac").exists(), "after three failed tries it is unklar")
        self.assertTrue((u / "Unbekannter Künstler/__.1.mp3").exists(), "a nameless file at the top has no artist")
        self.assertTrue((u / "Unbekannter Künstler/__.1.lrc").exists())
        self.assertTrue((u / "_reste/download_log.txt").exists())
        self.assertTrue((u / "_reste/pipeline_progress_log.txt").exists())
        self.assertTrue((u / "_reste/_downloads").is_dir(), "an empty leftover folder moves, it is not removed")
        self.assertTrue((u / "_reste/simplemp3s/artist.nfo").exists())
        self.assertTrue((u / ".ignore").exists(), "Jellyfin leaves _unklar out of the library")
        grund = (u / "LIESMICH.txt").read_text()
        self.assertIn("Erfundene Sängerin/Unbekanntes Album/02 Zweites.mp3: nicht sicher erkannt", grund)
        self.assertIn("Unbekannter Künstler/Vom Leben/b doppelt.mp3: schon in der Musiksammlung", grund)
        self.assertIn("Erfundene Band/Unknown/c kaputt.flac: beets konnte die Datei nicht lesen", grund)
        self.assertFalse((self.musik / "Erfundene Sängerin/Unbekanntes Album").exists(), "the emptied folder leaves")
        self.assertFalse((self.musik / "Unbekannter Künstler").exists())
        self.assertTrue((self.musik / "Erfundene Sängerin/artist.nfo").exists())
        s = self.stand()
        self.assertEqual((s["einsortiert"], s["unklar"], s["offen"], s["gesamt"]), (5, 5, 0, 10), s)

    def test_spoken_word_is_proposed_left_alone_and_moved_only_after_the_yes(self):
        vorher = self.alle()
        self.bis_fertig()
        self.assertTrue((self.musik / "Erfundener Komiker/Unknown/witz.mp3").exists(), "the sort keeps its hands off spoken word")
        self.assertEqual(self.stand()["gesprochen"]["vorschlag"], ["Erfundener Komiker", "Erfundenes Hörspiel"],
                         "only what the music folder holds is proposed")
        self.assertFalse(any(self.hoer.glob("Erfundenes Hörspiel")), "nothing moves without the owner's yes")
        self.run_it(BESTAND_GESPROCHEN="Erfundener Komiker; Erfundenes Hörspiel; ../etc")
        self.assertEqual(self.alle(), vorher, "a file went missing in the move")
        self.assertTrue((self.hoer / "Erfundenes Hörspiel/Folge 1/01 teil.mp3").exists())
        self.assertFalse((self.musik / "Erfundenes Hörspiel").exists())
        self.assertTrue((self.hoer / "Erfundener Komiker/Unknown/witz.mp3").exists(), "merged into the folder that is there")
        self.assertTrue((self.hoer / "Erfundener Komiker/Altes Programm/01.mp3").exists())
        self.assertEqual((self.hoer / "Erfundener Komiker/Live/01 witz.mp3").read_bytes(), b"ID3 schon da", "never overwritten")
        self.assertTrue((self.musik / "Erfundener Komiker/Live/01 witz.mp3").exists(), "a name taken there stays here")
        g = self.stand()["gesprochen"]
        self.assertEqual(g["verschoben"], ["Erfundenes Hörspiel"])
        self.assertEqual([x["name"] for x in g["probleme"]], ["Erfundener Komiker", "../etc"])
        self.assertEqual(g["vorschlag"], [], "a confirmed name is no longer proposed")

    def test_the_status_line_and_findings_through_the_musik_eingang(self):
        self.run_it(BESTAND_JE_LAUF="3")
        (self.root / "eingang").mkdir()
        self.run_it(script=EINGANG_SCRIPT, MUSIK_EINGANG=str(self.root / "eingang"), MUSIK_STABIL_S="0")
        s = json.loads((self.config / "laib-status.json").read_text())
        self.assertRegex(s["detail"], r"^Musik-Eingang: bereit, noch nichts importiert · Bestand: \d von 10 einsortiert, \d unklar$")
        g = [f for f in s["findings"] if f["detail"].startswith("Gesprochenes")]
        self.assertEqual(len(g), 1, s["findings"])
        self.assertIn("Erfundener Komiker; Erfundenes Hörspiel", g[0]["detail"])
        self.assertIn('BESTAND_GESPROCHEN: "Erfundener Komiker; Erfundenes Hörspiel"', g[0]["fix"])
        self.bis_fertig()
        self.run_it(script=EINGANG_SCRIPT, MUSIK_EINGANG=str(self.root / "eingang"), MUSIK_STABIL_S="0")
        s = json.loads((self.config / "laib-status.json").read_text())
        self.assertTrue(s["detail"].endswith("Bestand: fertig, 5 von 10 einsortiert, 5 unklar"), s["detail"])
        u = [f for f in s["findings"] if "unklar" in f["fix"]]
        self.assertEqual(u[0]["severity"], "info")
        self.assertIn("music/_unklar", u[0]["fix"])

    def test_big_numbers_read_german(self):
        sys.path.insert(0, str(HIER))
        ns = {}
        exec(compile(EINGANG_SCRIPT.read_text().replace('if __name__ == "__main__":\n    main()', ""), "x", "exec"), ns)
        self.assertEqual(ns["bestand_zeile"]({"einsortiert": 640, "unklar": 120, "gesamt": 1600, "fertig": False}),
                         "Bestand: 640 von 1.600 einsortiert, 120 unklar")

    def test_off_switch_and_one_run_at_a_time(self):
        self.run_it(MUSIK_BESTAND="false")
        self.assertEqual(self.calls_made(), [])
        import fcntl
        (self.config / "bestand").mkdir(exist_ok=True)
        with open(self.config / "bestand" / "lauf.lock", "w") as l:
            fcntl.flock(l, fcntl.LOCK_EX)
            p = self.run_it()
        self.assertEqual(self.calls_made(), [])
        self.assertIn("läuft schon", p.stdout)


if __name__ == "__main__":
    unittest.main()
