"""#646: abs-pflege.py looks after Audiobookshelf's libraries, against a fake
ABS 2.x (the routes and shapes of server/routers/ApiRouter.js).

The owner: "wie die Bücher und Podcasts und co miserabel taggen und pflegen".
The folders are decent (<Autor>/<Titel>, a Hörspiel series as <Serie>/<NNN> -
<Titel>), the file tags are not. So, once per item and paced across runs:
ABS's quick match against audible.de (it fills only what is empty, never an
owner's edit), the series from the folder where the match gave none, the
chapters where Audible's runtime fits, and the items without a hit listed in
the status. And the feeds of the hand-made sync_podcasts.py subscribed in
ABS, read as data, never run. Made-up books, series and feeds only."""
import json, os, re, subprocess, sys, tempfile, threading, unittest, urllib.parse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent.parent / "abs-pflege.py"
ROOT = "-root-pw-Zt"
ABS = {}

SYNC = '''#!/usr/bin/env python3
import os
PODCASTS = {
    "Gartenfunk": "https://feeds.example.org/gartenfunk.xml",
    "Nachtschicht Radio": "https://podcast.example.net/nachtschicht/feed/m4a"
}
open(os.path.join(os.path.dirname(os.path.abspath(__file__)), "RAN"), "w").write("the script ran")
'''


def book(i, rel, title=None, author=None, asin=None, series="", chapters=1, duration=3600.0, files=None):
    """files: [(name, size, seconds)] in ABS's current order; each file is one chapter, as ABS's scan makes them"""
    b = {"id": i, "relPath": rel, "mediaType": "book", "media": {"metadata": {
        "title": title or rel.split("/")[-1], "authorName": author or rel.split("/")[0], "seriesName": series, "asin": asin},
        "numChapters": chapters, "duration": duration}}
    if files:
        b["media"]["audioFiles"] = [{"index": n + 1, "ino": f"{i}-ino-{n}", "duration": d, "exclude": False,
                                     "metadata": {"filename": name, "size": size}} for n, (name, size, d) in enumerate(files)]
        starts = [sum(d for _, _, d in files[:n]) for n in range(len(files))]
        b["media"]["chapters"] = [{"id": n, "start": starts[n], "end": starts[n] + d, "title": name.rsplit(".", 1)[0]}
                                  for n, (name, _, d) in enumerate(files)]
        b["media"].update(numAudioFiles=len(files), numChapters=len(files), duration=sum(d for _, _, d in files))
    return b


def fresh(books=(), podcasts=()):
    ABS.clear()
    ABS.update(books={b["id"]: b for b in books}, podcasts={p["id"]: p for p in podcasts}, matches=[], media=[], chapters={},
               created=[], feeds=[], downloads={}, episode_matches=[], nohit=set(), series_from_match=set(), runtimes={}, scans=[],
               hit_only={}, tracks={})


class FakeABS(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *a):
        pass

    def reply(self, code, body=None):
        data = json.dumps(body).encode() if body is not None else b""
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def body(self):
        return json.loads(self.rfile.read(int(self.headers.get("Content-Length") or 0)) or b"{}")

    def authed(self):
        return self.headers.get("Authorization") == "Bearer access-1"

    def do_GET(self):
        if not self.authed():
            return self.reply(401)
        u = urllib.parse.urlsplit(self.path)
        q = {k: v[0] for k, v in urllib.parse.parse_qs(u.query).items()}
        if u.path == "/api/libraries":
            return self.reply(200, {"libraries": [
                {"id": "lib-b", "name": "Hörbücher & Hörspiele", "mediaType": "book", "folders": [{"id": "fo-b", "fullPath": "/audiobooks"}]},
                {"id": "lib-p", "name": "Podcasts", "mediaType": "podcast", "folders": [{"id": "fo-p", "fullPath": "/podcasts"}]}]})
        if m := re.fullmatch(r"/api/libraries/(lib-[bp])/items", u.path):
            items = [{**it, "media": {k: v for k, v in it["media"].items() if k not in ("audioFiles", "chapters")}}   # minified
                     for it in (ABS["books"] if m.group(1) == "lib-b" else ABS["podcasts"]).values()]
            limit, page = int(q.get("limit", 0)), int(q.get("page", 0))
            return self.reply(200, {"results": items[page * limit:(page + 1) * limit] if limit else items, "total": len(items),
                                    "limit": limit, "page": page})
        if (m := re.fullmatch(r"/api/items/([^/]+)", u.path)) and q.get("expanded") == "1":
            return self.reply(200, ABS["books"][m.group(1)])
        if u.path == "/api/search/chapters":
            asin = q["asin"]
            if q.get("region") != "de" or asin not in ABS["runtimes"]:
                return self.reply(200, {"error": "Chapters not found"})
            rt = ABS["runtimes"][asin]
            return self.reply(200, {"asin": asin, "runtimeLengthSec": rt, "chapters": [
                {"startOffsetMs": 0, "lengthMs": rt * 500, "title": "Kapitel 1"}, {"startOffsetMs": rt * 500, "lengthMs": rt * 500, "title": "Kapitel 2"}]})
        self.reply(404)

    def do_POST(self):
        b = self.body()
        if self.path == "/login":
            if (b.get("username"), b.get("password")) != ("root", ROOT):
                return self.reply(401)
            return self.reply(200, {"user": {"accessToken": "access-1"}})
        if not self.authed():
            return self.reply(401)
        if m := re.fullmatch(r"/api/libraries/([^/]+)/scan", self.path):
            ABS["scans"].append(m.group(1))
            return self.reply(200)
        if m := re.fullmatch(r"/api/items/([^/]+)/match", self.path):
            it = ABS["books"][m.group(1)]
            ABS["matches"].append((m.group(1), b))
            want = ABS["hit_only"].get(m.group(1))   # Audible knows it only under this (title, author)
            if it["media"]["metadata"]["title"] in ABS["nohit"] or b.get("title") in ABS["nohit"] or \
                    (want and (b.get("title"), b.get("author")) != want):
                return self.reply(200, {"warning": f"No {b.get('provider')} match found"})
            md = it["media"]["metadata"]
            md["asin"] = "B0" + m.group(1).upper().replace("-", "")
            series = [{"id": "s1", "name": "Aus Audible", "sequence": "7"}] if m.group(1) in ABS["series_from_match"] else []
            if series:
                md["seriesName"] = "Aus Audible #7"
            return self.reply(200, {"updated": True, "libraryItem": {"id": m.group(1), "media": {
                "metadata": {**md, "series": series}, "duration": it["media"]["duration"],
                "chapters": [{"id": 0, "start": 0, "end": it["media"]["duration"], "title": "x"}][:it["media"]["numChapters"]]}}})
        if m := re.fullmatch(r"/api/items/([^/]+)/chapters", self.path):
            ABS["chapters"][m.group(1)] = b["chapters"]
            ABS["books"][m.group(1)]["media"]["chapters"] = b["chapters"]
            return self.reply(200, {"success": True, "updated": True})
        if self.path == "/api/podcasts/feed":
            ABS["feeds"].append(b["rssFeed"])
            return self.reply(200, {"podcast": {"metadata": {"title": "Nachtschicht &amp; Co", "author": "Erfundene Leute", "feedUrl": b["rssFeed"],
                                                             "imageUrl": "https://img.example.net/n.jpg", "description": "made up"},
                                                "episodes": [{"title": f"Folge {n}", "enclosure": {"url": f"https://podcast.example.net/{n}.m4a"}}
                                                             for n in range(9, 0, -1)]}})
        if self.path == "/api/podcasts":
            ABS["created"].append(b)
            i = f"pod-{len(ABS['podcasts'])}"
            ABS["podcasts"][i] = {"id": i, "relPath": b["path"].split("/")[-1], "mediaType": "podcast",
                                  "media": {"metadata": b["media"]["metadata"], "autoDownloadEpisodes": b["media"].get("autoDownloadEpisodes")}}
            return self.reply(200, {"id": i, "media": b["media"]})
        if m := re.fullmatch(r"/api/podcasts/([^/]+)/download-episodes", self.path):
            ABS["downloads"][m.group(1)] = b
            return self.reply(200)
        if m := re.fullmatch(r"/api/podcasts/([^/]+)/match-episodes\?override=1", self.path):
            ABS["episode_matches"].append(m.group(1))
            return self.reply(200, {"numEpisodesUpdated": 5})
        self.reply(404)

    def do_PATCH(self):
        b = self.body()
        if self.authed() and (m := re.fullmatch(r"/api/items/([^/]+)/tracks", self.path)):
            media = ABS["books"][m.group(1)]["media"]
            by_ino = {f["ino"]: f for f in media["audioFiles"]}
            if sorted(by_ino) != sorted(f["ino"] for f in b["orderedFileData"]):
                return self.reply(400)   # ABS drops every file the list leaves out
            ABS["tracks"][m.group(1)] = [f["ino"] for f in b["orderedFileData"]]
            media["audioFiles"] = [{**by_ino[f["ino"]], "index": n + 1} for n, f in enumerate(b["orderedFileData"])]
            return self.reply(200, {"id": m.group(1)})
        m = re.fullmatch(r"/api/items/([^/]+)/media", self.path)
        if not self.authed() or not m:
            return self.reply(404)
        ABS["media"].append((m.group(1), b))
        it = ABS["books"].get(m.group(1)) or ABS["podcasts"][m.group(1)]
        md = it["media"]["metadata"]
        for k, v in (b.get("metadata") or {}).items():
            if k == "series":
                md["seriesName"] = ", ".join(f"{s['name']} #{s['sequence']}" for s in v)
            elif k == "authors":
                md["authorName"] = ", ".join(a["name"] for a in v)
            else:
                md[k] = v
        if "autoDownloadEpisodes" in b:
            it["media"]["autoDownloadEpisodes"] = b["autoDownloadEpisodes"]
        self.reply(200, {"updated": True})


class Pflege(unittest.TestCase):
    def setUp(self):
        self.srv = ThreadingHTTPServer(("127.0.0.1", 0), FakeABS)
        self.srv.daemon_threads = True
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()
        self.addCleanup(self.srv.server_close)
        self.addCleanup(self.srv.shutdown)
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.data, self.pods = Path(self.tmp.name, "data"), Path(self.tmp.name, "podcasts")
        self.data.mkdir()
        self.pods.mkdir()

    def run_pflege(self, per_run="50"):
        env = {"PATH": os.environ["PATH"], "ABS_URL": f"http://127.0.0.1:{self.srv.server_port}", "ABS_PASSWORD": ROOT,
               "STATE_DIR": str(self.data), "PODCASTS_DIR": str(self.pods), "MATCH_PER_RUN": per_run, "MATCH_PAUSE_S": "0"}
        p = subprocess.run([sys.executable, str(SCRIPT)], env=env, capture_output=True, text=True, timeout=60)
        self.assertNotIn(ROOT, p.stdout + p.stderr)
        return p, json.loads((self.data / "laib-status.json").read_text())

    def test_the_match_is_paced_runs_once_per_item_and_resumes(self):
        fresh([book(f"b-{n}", f"Erfundene Autorin/Buch {n}") for n in range(7)])
        p, st = self.run_pflege(per_run="3")
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertEqual(len(ABS["matches"]), 3, "not paced: every item at once")
        self.assertEqual({b["provider"] for _, b in ABS["matches"]}, {"audible.de"})
        self.assertTrue(all(b.get("overrideDetails") is False for _, b in ABS["matches"]), "the match may overwrite what the owner wrote")
        self.assertIn("7 Titel, 3 abgeglichen, 4 noch offen", st["detail"])
        self.assertIn("4 noch offen", st["detail"])
        self.run_pflege(per_run="3")
        _, st = self.run_pflege(per_run="3")
        self.assertEqual(sorted(i for i, _ in ABS["matches"]), sorted(f"b-{n}" for n in range(7)), "an item matched twice or not at all")
        ABS["books"]["b-0"]["media"]["metadata"].update(title="Vom Besitzer korrigiert", asin=None)   # the owner edits it in ABS
        _, st = self.run_pflege(per_run="3")
        self.assertEqual(len(ABS["matches"]), 7, "an item the match already had was matched again")
        self.assertIn("Hörbücher: 7 Titel, 7 abgeglichen", st["detail"])

    def test_an_empty_book_library_is_scanned_and_says_so(self):
        # 2026-10-25: the library was made but never read in; the status said „0 abgeglichen“ for hours
        fresh()
        p, st = self.run_pflege()
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertEqual(ABS["scans"], ["lib-b"])
        self.assertIn("noch nichts eingelesen", st["detail"])
        self.assertNotIn("0 abgeglichen", st["detail"])
        p, st = self.run_pflege()
        self.assertEqual(ABS["scans"], ["lib-b"], "a scan was asked for again within the day")

    def test_the_count_of_books_is_in_the_status(self):
        fresh([book(f"b-{n}", f"Erfundene Autorin/Buch {n}") for n in range(3)])
        p, st = self.run_pflege()
        self.assertIn("3 Titel", st["detail"])

    def test_an_item_with_an_asin_is_left_as_it_is(self):
        fresh([book("b-1", "Erfundene Autorin/Schon gepflegt", asin="B0OWNER1"), book("b-2", "Erfundene Autorin/Neu")])
        _, st = self.run_pflege()
        self.assertEqual([i for i, _ in ABS["matches"]], ["b-2"])
        self.assertIn("2 abgeglichen", st["detail"])

    def test_items_without_a_hit_are_listed_until_the_owner_matched_them(self):
        fresh([book("b-1", "Erfundene Autorin/Gibt es"), book("b-2", "Niemand/Das Unauffindbare")])
        ABS["nohit"].add("Das Unauffindbare")
        p, st = self.run_pflege()
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertIn("Hörbücher: 2 Titel, 1 abgeglichen, 1 ohne Treffer: Das Unauffindbare", st["detail"])
        (f,) = [f for f in st["findings"] if "Das Unauffindbare" in f["detail"]]
        self.assertIn("Audiobookshelf", f["fix"])
        n = len(ABS["matches"])
        self.run_pflege()
        self.assertEqual(len(ABS["matches"]), n, "the unmatched item was tried again with the same queries")
        ABS["books"]["b-2"]["media"]["metadata"]["asin"] = "B0BYHAND1"     # matched by hand in ABS
        _, st = self.run_pflege()
        self.assertEqual(len(ABS["matches"]), n, "the unmatched item was tried again")
        self.assertFalse(any("Das Unauffindbare" in f["detail"] for f in st["findings"]))
        self.assertIn("2 abgeglichen", st["detail"])

    def test_a_hoerspiel_folder_becomes_a_series_with_its_numbers(self):
        fresh([book("h-1", "Die Gruselfüchse/001 - Der Nebelturm"), book("h-2", "Die Gruselfüchse/002 - Das Spukschiff"),
               book("h-3", "Die Gruselfüchse/010 - Die Mondbrücke"), book("h-4", "DiE GrUsEl/01 - Das Seeungeheuer"),
               book("h-5", "DiE GrUsEl/02 - Der Eiswurm"), book("e-1", "Einzelautor/01 - Vorwort an niemanden"),
               book("h-6", "Mit Serie/01 - Eins"), book("h-7", "Mit Serie/02 - Zwei")])
        ABS["nohit"].add("Der Eiswurm")
        ABS["series_from_match"].add("h-6")
        self.run_pflege()
        search = dict(ABS["matches"])
        self.assertEqual((search["h-1"]["title"], search["h-1"]["author"]), ("Der Nebelturm", "Die Gruselfüchse"),
                         "Audible is asked for '001 - Der Nebelturm'")
        md = lambda i: ABS["books"][i]["media"]["metadata"]
        self.assertEqual((md("h-1")["seriesName"], md("h-1")["title"]), ("Die Gruselfüchse #1", "Der Nebelturm"))
        self.assertEqual(md("h-3")["seriesName"], "Die Gruselfüchse #10")
        self.assertEqual(md("h-5")["seriesName"], "DiE GrUsEl #2", "an item without a hit still belongs to its series")
        self.assertEqual(md("h-6")["seriesName"], "Aus Audible #7", "the series the match found was overwritten")
        self.assertEqual(md("h-7")["seriesName"], "Mit Serie #2")
        self.assertEqual(md("e-1")["seriesName"], "", "one numbered folder is no series")
        n = len(ABS["media"])
        self.run_pflege()
        self.assertEqual(len(ABS["media"]), n, "the series were written again")

    def test_a_broken_tag_title_is_named_after_its_folder(self):
        # 2026-10-25: „CD 1“, „Die K\ufffdnguru Chroniken“, „Die volle Wahrheit CD 1“: the album tag, not the folder
        fresh([book("t-1", "Erfundene Autorin/Die Wüstenfähre", title="Die W\ufffdstenf\ufffdhre"),
               book("t-2", "Erfundener Autor/Nebelreise", title="CD 1", author="Nebelreise"),
               book("t-3", "Erfundene Autorin/Der lange Weg", title="Der lange Weg CD 1"),
               book("t-4", "Erfundene Hörspiele/01 - Das Moorlicht", title="01 - Das Moorlicht - Teil 1"),
               book("t-5", "Die Gruselfüchse/001 - Der Nebelturm", title="Die Gruselfüchse"),
               book("t-6", "Die Gruselfüchse/002 - Das Spukschiff"),
               book("t-7", "Erfundene Autorin/Gut benannt", title="Vom Besitzer benannt"),
               book("t-8", "Erfundene Autorin/Teilchenzoo", title="Teilchenzoo, Band 2")])
        p, st = self.run_pflege()
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        md = lambda i: ABS["books"][i]["media"]["metadata"]
        self.assertEqual([md(i)["title"] for i in ("t-1", "t-2", "t-3", "t-4")], ["Die Wüstenfähre", "Nebelreise", "Der lange Weg", "Das Moorlicht"])
        self.assertEqual(md("t-5")["title"], "Der Nebelturm", "the series' name as an episode's title")
        self.assertEqual(md("t-2")["authorName"], "Erfundener Autor", "the tag's artist is the book's name, the folder knows the author")
        self.assertEqual(md("t-3")["authorName"], "Erfundene Autorin")
        self.assertEqual((md("t-7")["title"], md("t-8")["title"]), ("Vom Besitzer benannt", "Teilchenzoo, Band 2"), "a sound title was replaced")
        first = {i: b for i, b in reversed(ABS["matches"])}
        self.assertEqual((first["t-2"]["title"], first["t-2"]["author"]), ("Nebelreise", "Erfundener Autor"), "Audible was asked for „CD 1“")
        self.assertEqual(first["t-1"]["title"], "Die Wüstenfähre")
        n = len(ABS["media"])
        self.run_pflege()
        self.assertEqual(len(ABS["media"]), n, "the titles were written again")

    def test_a_second_query_restores_umlauts_and_leaves_the_author_out(self):
        # „Der hoellische Werwolf“ in „043 - Der h_llische Werwolf“, „Schuesse aus dem Dunkel“ in „070 - Schüsse im Dunkeln“
        fresh([book("u-1", "Die Gruselfüchse/001 - Der Nebelturm"), book("u-2", "Die Gruselfüchse/043 - Der h_llische Kobold", title="Der hoellische Kobold"),
               book("u-3", "Die Gruselfüchse/070 - Schüsse im Moor", title="Schuesse aus dem Moor"),
               book("u-4", "Erfundene Autorin/Die Raeuberleiter")])
        ABS["hit_only"].update({"u-2": ("Der höllische Kobold", " "), "u-3": ("Schüsse aus dem Moor", " "), "u-4": ("Die Räuberleiter", " ")})
        p, st = self.run_pflege()
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        asked = lambda i: [(b["title"], b["author"]) for j, b in ABS["matches"] if j == i]
        self.assertEqual(asked("u-2")[0], ("Der hoellische Kobold", "Die Gruselfüchse"), "the folder's „_“ went to Audible")
        self.assertIn(("Der höllische Kobold", " "), asked("u-2"))
        self.assertEqual(asked("u-3")[0], ("Schüsse im Moor", "Die Gruselfüchse"))
        self.assertIn(("Schüsse aus dem Moor", " "), asked("u-3"), "the tag's title, with its umlauts, was never asked")
        self.assertLessEqual(max(len(asked(i)) for i in ("u-2", "u-3", "u-4")), 3, "too many questions for one item")
        self.assertIn("4 Titel, 4 abgeglichen", st["detail"])
        self.assertEqual(ABS["books"]["u-4"]["relPath"], "Erfundene Autorin/Die Raeuberleiter")

    def test_a_series_without_a_hit_takes_the_name_its_siblings_got_from_audible(self):
        kids = [book(f"s-{n}", f"Die Gruselfuechse/{n:03d} - Folge {n}") for n in range(1, 5)]
        fresh(kids + [book("s-9", "Die Gruselfuechse/009 - Ohne Treffer", series="Die Gruselfuechse #9")])
        ABS["series_from_match"].update({"s-1", "s-2", "s-3"})
        ABS["nohit"].update({"Ohne Treffer", "Folge 4"})
        self.run_pflege()
        self.run_pflege()   # the siblings' series is known once they are matched
        md = lambda i: ABS["books"][i]["media"]["metadata"]
        self.assertEqual(md("s-9")["seriesName"], "Aus Audible #9", "two series for one Hörspiel folder")
        self.assertEqual(md("s-4")["seriesName"], "Aus Audible #4")
        n = len(ABS["media"])
        self.run_pflege()
        self.assertEqual(len(ABS["media"]), n, "the series were written again")

    def test_an_item_without_a_hit_is_matched_again_when_its_query_changed(self):
        # the box's state from before this fix: „CD 1“ asked once, „ohne Treffer“ for good
        fresh([book("r-1", "Erfundener Autor/Nebelreise", title="CD 1", author="Nebelreise"), book("r-2", "Niemand/Das Unauffindbare")])
        ABS["nohit"].add("Das Unauffindbare")
        (self.data / "abgleich.json").write_text(json.dumps({"items": {"r-1": {"r": "ohne Treffer", "title": "CD 1"},
                                                                       "r-2": {"r": "ohne Treffer", "title": "Das Unauffindbare"}}}))
        _, st = self.run_pflege()
        self.assertEqual(ABS["books"]["r-1"]["media"]["metadata"]["asin"], "B0R1", "the renamed item was not matched again")
        self.assertNotIn("CD 1", st["detail"])
        self.assertIn("1 ohne Treffer: Das Unauffindbare", st["detail"])
        n = len(ABS["matches"])
        self.run_pflege()
        self.assertEqual(len(ABS["matches"]), n, "matched again although nothing changed")

    def test_the_cds_in_one_folder_play_in_disc_order(self):
        # 2026-10-25: „Der Zeitdieb“ played 1-01, 2-01, 3-01, 1-02 …: ABS ordered the files by their track number only
        fresh([book("d-1", "Erfundene Autorin/Die Uhrmacherin", asin="B0D1", files=[
                   ("1-01 - Die Uhrmacherin.mp3", 100, 60.0), ("2-01 - Die Uhrmacherin.mp3", 110, 70.0),
                   ("1-02 - Die Uhrmacherin.mp3", 120, 80.0), ("2-02 - Die Uhrmacherin.mp3", 130, 90.0)]),
               book("d-2", "Erfundene Autorin/Das Sammelband", asin="B0D2", files=[   # „NN - DTT“: disc and track the other way round
                   ("01 - 101.mp3", 10, 5.0), ("02 - 102.mp3", 11, 5.0), ("01 - 201.mp3", 12, 5.0), ("02 - 202.mp3", 13, 5.0)]),
               book("d-3", "Erfundene Autorin/Die Brücke", asin="B0D3", files=[
                   ("Die Brücke 1-1.mp3", 20, 5.0), ("Die Brücke 2-1.mp3", 21, 6.0), ("Die Brücke 1-2.mp3", 22, 7.0)])])
        p, st = self.run_pflege()
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertEqual(ABS["tracks"]["d-1"], ["d-1-ino-0", "d-1-ino-2", "d-1-ino-1", "d-1-ino-3"])
        self.assertEqual([(c["start"], c["end"], c["title"]) for c in ABS["chapters"]["d-1"]], [
            (0, 60, "1-01 - Die Uhrmacherin"), (60, 140, "1-02 - Die Uhrmacherin"), (140, 210, "2-01 - Die Uhrmacherin"),
            (210, 300, "2-02 - Die Uhrmacherin")], "the chapters still jump between the CDs")
        self.assertNotIn("d-2", ABS["tracks"], "a book without a disc in its file names was reordered")
        self.assertEqual(ABS["tracks"]["d-3"], ["d-3-ino-0", "d-3-ino-2", "d-3-ino-1"], "the title before the disc hides it")
        n = len(ABS["tracks"]), len(ABS["chapters"])
        self.run_pflege()
        self.assertEqual((len(ABS["tracks"]), len(ABS["chapters"])), n, "reordered twice")

    def test_files_held_twice_are_a_proposal_for_the_owner_never_a_move(self):
        # 2026-10-25: „The Bro Code“ holds CD 1 twice, „Schwarze Löcher“ a copy under a mojibake name
        fresh([book("x-1", "Erfundener Autor/Der Doppelband", asin="B0X1", files=[
                   ("01 - Track 1.mp3", 500, 30.0), ("CD1-CD1-01 Track 1.mp3", 500, 30.0), ("CD2-CD2-01 Piste 1.mp3", 700, 40.0),
                   ("02 - Track 2.mp3", 600, 35.0), ("CD1-CD1-02 Track 2.mp3", 600, 35.0)]),
               book("x-2", "Erfundener Autor/Schwarze Kästen", asin="B0X2", files=[
                   ("Schwarze Kästen.mp3", 900, 99.0), ("_from_alt-Schwarze KĂ¤sten.mp3", 900, 99.0)])])
        p, st = self.run_pflege()
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertEqual(ABS["tracks"], {}, "files were reordered before the owner decided about the copies")
        (f,) = [f for f in st["findings"] if "Der Doppelband" in f["detail"]]
        self.assertIn("2 Dateien doppelt", f["detail"])
        self.assertIn("„audiobooks/Erfundener Autor/Der Doppelband“ nach „aussortiert/audiobooks/Erfundener Autor/Der Doppelband“", f["fix"])
        self.assertIn("01 - Track 1.mp3, 02 - Track 2.mp3", f["fix"])
        self.assertNotIn("CD1-CD1-01", f["fix"])
        (g,) = [f for f in st["findings"] if "Schwarze Kästen" in f["detail"]]
        self.assertIn("_from_alt-Schwarze KĂ¤sten.mp3", g["fix"])
        self.assertNotIn("„Schwarze Kästen.mp3", g["fix"])
        # the owner moved the copies; ABS's scan sees four files fewer
        media = ABS["books"]["x-1"]["media"]
        media["audioFiles"] = [a for a in media["audioFiles"] if a["metadata"]["filename"].startswith("CD")]
        media["chapters"] = [c for c in media["chapters"] if c["title"].startswith("CD")]
        media.update(numAudioFiles=3, numChapters=3)
        _, st = self.run_pflege()
        self.assertFalse(any("Der Doppelband" in f["detail"] for f in st["findings"]))
        self.assertEqual(ABS["tracks"]["x-1"], ["x-1-ino-1", "x-1-ino-4", "x-1-ino-2"])

    def test_chapters_come_from_audible_only_when_the_runtime_fits(self):
        fresh([book("c-1", "Erfundene Autorin/Passt", duration=3600.0), book("c-2", "Erfundene Autorin/Passt nicht", duration=3600.0),
               book("c-3", "Erfundene Autorin/Hat Kapitel", chapters=12, duration=3600.0)])
        ABS["runtimes"].update({"B0C1": 3610, "B0C2": 5400, "B0C3": 3600})
        self.run_pflege()
        self.assertEqual(sorted(ABS["chapters"]), ["c-1"])
        self.assertEqual([(c["start"], c["end"], c["title"]) for c in ABS["chapters"]["c-1"]], [(0, 1805, "Kapitel 1"), (1805, 3610, "Kapitel 2")])

    def test_the_feeds_of_sync_podcasts_are_subscribed_and_the_script_never_runs(self):
        (self.pods / "sync_podcasts.py").write_text(SYNC)
        fresh(podcasts=[{"id": "pod-old", "relPath": "Gartenfunk", "mediaType": "podcast",
                         "media": {"metadata": {"title": "Gartenfunk", "feedUrl": None}, "autoDownloadEpisodes": False}}])
        p, st = self.run_pflege()
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertFalse((self.pods / "RAN").exists(), "sync_podcasts.py was executed")
        old = ABS["podcasts"]["pod-old"]["media"]
        self.assertEqual((old["metadata"]["feedUrl"], old["autoDownloadEpisodes"]), ("https://feeds.example.org/gartenfunk.xml", True))
        self.assertEqual(ABS["episode_matches"], ["pod-old"], "the episodes sync_podcasts.py wrote keep their mangled names")
        (new,) = ABS["created"]
        self.assertEqual((new["path"], new["folderId"], new["libraryId"]), ("/podcasts/Nachtschicht Radio", "fo-p", "lib-p"))
        self.assertTrue(new["media"]["autoDownloadEpisodes"])
        self.assertEqual(new["media"]["metadata"]["feedUrl"], "https://podcast.example.net/nachtschicht/feed/m4a")
        self.assertEqual(new["media"]["metadata"]["author"], "Erfundene Leute", "the feed's metadata is not used")
        (dl,) = ABS["downloads"].values()
        self.assertEqual([e["title"] for e in dl], ["Folge 9", "Folge 8", "Folge 7"], "a new subscription starts empty")
        self.assertIn("Podcasts: 2 abonniert", st["detail"])
        self.run_pflege()
        self.assertEqual((len(ABS["created"]), ABS["episode_matches"], len(ABS["feeds"])), (1, ["pod-old"], 1), "subscribed twice")

    def test_abs_down_is_red_in_the_status(self):
        env = {"PATH": os.environ["PATH"], "ABS_URL": "http://127.0.0.1:1", "ABS_PASSWORD": ROOT, "STATE_DIR": str(self.data),
               "PODCASTS_DIR": str(self.pods), "ABS_WAIT_S": "0"}
        p = subprocess.run([sys.executable, str(SCRIPT)], env=env, capture_output=True, text=True, timeout=60)
        self.assertNotEqual(p.returncode, 0)
        st = json.loads((self.data / "laib-status.json").read_text())
        self.assertEqual(st["findings"][0]["severity"], "rot")


if __name__ == "__main__":
    unittest.main()
