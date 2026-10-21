"""#495: kalender.py mirrors each subscription (a Google calendar's secret
iCal address, a public ICS) one way into the person's read-only Radicale
collection, as the service account `kalender`, and copies the family's
calendars into Home Assistant's own account. A fake source (the ICS
addresses) and a fake Radicale (which lets `kalender` write only where
templates/radicale's rights do) stand in; the script runs as the timer runs
it, the subscriptions in the environment. Its status file is what laib diag
shows; the secret address appears nowhere but in the request to Google."""
import base64, importlib.util, json, os, subprocess, sys, tempfile, threading, unittest, urllib.parse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

HERE = Path(__file__).resolve().parent.parent
SCRIPT = HERE / "kalender.py"
IMPORT = HERE.parent.parent / "scripts" / "import-google.py"
SECRET = "private-0123456789abcdef"


def load(path, name):
    spec = importlib.util.spec_from_file_location(name, path)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


def ics(*events):
    body = "".join(f"BEGIN:VEVENT\r\nUID:{uid}\r\nDTSTART;TZID=Europe/Berlin:20261101T100000\r\nSUMMARY:{s}\r\nEND:VEVENT\r\n" for uid, s in events)
    return ("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Google Inc//Google Calendar 70.9054//EN\r\n"
            "BEGIN:VTIMEZONE\r\nTZID:Europe/Berlin\r\nEND:VTIMEZONE\r\n" + body + "END:VCALENDAR\r\n")


def serve(handler):
    srv = ThreadingHTTPServer(("127.0.0.1", 0), handler)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    return srv, f"http://127.0.0.1:{srv.server_address[1]}"


class Base(BaseHTTPRequestHandler):
    def log_message(self, *a): pass

    def answer(self, code, body=b"", ctype="application/xml"):
        self.send_response(code); self.send_header("Content-Type", ctype); self.send_header("Content-Length", str(len(body))); self.end_headers(); self.wfile.write(body)

    def body(self):
        return self.rfile.read(int(self.headers.get("Content-Length") or 0))


class FakeSource:
    """GET <path> answers the ICS set for it, 404 for anything else; the queries are kept"""
    def __init__(self):
        self.cals, self.seen = {}, []
        outer = self

        class H(Base):
            def do_GET(self):
                u = urllib.parse.urlsplit(self.path)
                outer.seen.append((u.path, dict(urllib.parse.parse_qsl(u.query)), self.headers.get("Accept")))
                if u.path not in outer.cals:
                    return self.answer(404)
                self.answer(200, outer.cals[u.path].encode(), "text/calendar")

        self.srv, self.url = serve(H)


MIRROR_RIGHTS = ("/lea/google-", "/lea/abo-", "/haus/abo-", "/homeassistant/")


class FakeRadicale:
    """collections and items as Radicale keeps them; `kalender` may write only the mirrors and HA's home,
    and read haus/; a person's home exists only once they logged in (homes)"""
    def __init__(self, pw):
        self.store, self.cols, self.props, self.methods = {}, {}, {}, []
        self.homes = {"/lea/", "/haus/"}
        outer, want = self, "Basic " + base64.b64encode(f"kalender:{pw}".encode()).decode()

        class H(Base):
            def authed(self, write):
                outer.methods.append(f"{self.command} {self.path}")
                if self.headers.get("Authorization") != want:
                    self.answer(401); return False
                ok = self.path.startswith(MIRROR_RIGHTS) or (not write and self.path.startswith("/haus/"))
                if not ok:
                    self.answer(403); return False
                return True

            def do_PROPFIND(self):
                if not self.authed(False):
                    return
                self.body()
                if self.path not in outer.cols and self.path not in outer.homes:
                    return self.answer(404)
                hrefs = [(self.path, False)]
                if self.headers.get("Depth") == "1":
                    hrefs += [(p, False) for p in outer.store if p.startswith(self.path)]
                    hrefs += [(c, True) for c in outer.cols if c.startswith(self.path) and c != self.path and c.count("/") == self.path.count("/") + 1]
                rs = "".join(f"<D:response><D:href>{h}</D:href><D:propstat><D:prop><D:resourcetype>{'<D:collection/><C:calendar/>' if cal else ''}</D:resourcetype></D:prop></D:propstat></D:response>"
                             for h, cal in hrefs)
                self.answer(207, f'<D:multistatus xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">{rs}</D:multistatus>'.encode())

            def do_MKCALENDAR(self):
                if self.authed(True):
                    parent = self.path.rstrip("/").rsplit("/", 1)[0] + "/"
                    if parent not in outer.homes:
                        return self.answer(409)
                    outer.cols[self.path] = self.body().decode(); self.answer(201)

            def do_MKCOL(self):
                if self.authed(True):
                    outer.homes.add(self.path); self.answer(201)

            def do_PROPPATCH(self):
                if self.authed(True):
                    if self.path not in outer.cols:
                        return self.answer(404)
                    outer.props[self.path] = self.body().decode(); self.answer(207, b'<D:multistatus xmlns:D="DAV:"/>')

            def do_GET(self):
                if self.authed(False):
                    if self.path not in outer.cols:
                        return self.answer(404)
                    items = [v for k, v in outer.store.items() if k.startswith(self.path)]
                    inner = "".join(t.split("END:VTIMEZONE\r\n", 1)[-1].split("END:VCALENDAR")[0] for t in items)
                    self.answer(200, ("BEGIN:VCALENDAR\r\nVERSION:2.0\r\n" + inner + "END:VCALENDAR\r\n").encode(), "text/calendar")

            def do_PUT(self):
                if self.authed(True):
                    col = self.path.rsplit("/", 1)[0] + "/"
                    if col not in outer.cols:
                        return self.answer(409)
                    outer.store[self.path] = self.body().decode(); outer.puts += 1; self.answer(201)

            def do_DELETE(self):
                if self.authed(True):
                    if self.path.endswith("/"):
                        outer.cols.pop(self.path, None)
                        for k in [k for k in outer.store if k.startswith(self.path)]:
                            del outer.store[k]
                    outer.store.pop(self.path, None); self.answer(204)

        self.puts = 0
        self.srv, self.url = serve(H)

    def items(self, col):
        return {k: v for k, v in self.store.items() if k.startswith(col)}


def abos(*entries):
    """KALENDER_ABOS as cmd/laib/calendar.go writes it: base64url without padding of the JSON list"""
    return base64.urlsafe_b64encode(json.dumps(list(entries)).encode()).decode().rstrip("=")


class Mirror(unittest.TestCase):
    def setUp(self):
        self.src, self.r = FakeSource(), FakeRadicale("pw-kal")
        self.data = tempfile.mkdtemp()
        self.addCleanup(lambda: [(s.srv.shutdown(), s.srv.server_close()) for s in (self.src, self.r)])
        self.google = {"person": "lea", "name": "familie", "kind": "google", "title": "Familie", "since": "2026-10-21T08:00:00Z",
                       "url": self.src.url + f"/calendar/ical/lea%40gmail.com/{SECRET}/basic.ics"}
        self.gpath = f"/calendar/ical/lea%40gmail.com/{SECRET}/basic.ics"

    def run_it(self, *subs, ha="homeassistant", pw="pw-kal", **env):
        e = dict(os.environ, DATA=self.data, RADICALE_URL=self.r.url, RADICALE_USER="kalender", RADICALE_PASSWORD=pw,
                 HA_USER=ha, KALENDER_ABOS=abos(*subs) if subs else "", **env)
        p = subprocess.run([sys.executable, "-"], input=SCRIPT.read_text(), env=e, capture_output=True, text=True, timeout=60)
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        status = Path(self.data, "laib-status.json").read_text()
        for where in (p.stdout + p.stderr, status, Path(self.data, "state.json").read_text() if Path(self.data, "state.json").exists() else "",
                      json.dumps(self.r.props)):
            self.assertNotIn(SECRET, where, "the secret address leaked")
            self.assertNotIn("pw-kal", where)
        return json.loads(status)

    def test_a_google_calendar_lands_read_only_in_the_persons_collection_on_the_import_paths(self):
        self.src.cals[self.gpath] = ics(("a@google.com", "Zahnarzt"), ("w@google.com", "Turnen"), ("w@google.com", "Turnen später"))
        s = self.run_it(self.google)
        self.assertEqual(s["findings"], [], s)
        self.assertIn("1 Kalender verbunden, 2 Termine", s["detail"])
        self.assertIn("Familie", self.r.cols["/lea/google-familie/"])
        imp = load(IMPORT, "import_google")
        want = {f"/lea/{imp.collection('Familie')}/{imp.uuid.uuid5(imp.NS_CAL, uid)}.ics" for uid in ("a@google.com", "w@google.com")}
        self.assertEqual(set(self.r.items("/lea/")), want, "not the paths scripts/import-google.py writes: a Takeout import would double")
        weekly = [v for k, v in self.r.store.items() if "w@google.com" in v][0]
        self.assertEqual(weekly.count("BEGIN:VEVENT"), 2)      # the override goes with its series
        self.assertIn("TZID:Europe/Berlin", weekly)
        self.assertIn("<L:abgleich>", self.r.props["/lea/google-familie/"], "Haus shows when it was last mirrored")
        self.assertEqual(self.src.seen[0][2], "text/calendar")

    def test_home_assistant_gets_a_copy_of_every_mirror_and_of_the_familys_calendars_only(self):
        self.src.cals[self.gpath] = ics(("a@google.com", "Zahnarzt"))
        self.r.cols["/haus/haushalt/"] = "Haushalt"
        self.r.cols["/haus/familie-geburtstage/"] = "Geburtstage"
        self.r.cols["/haus/privat/"] = "privat"
        self.r.store["/haus/haushalt/x.ics"] = ics(("h1", "Müll raus"))
        self.r.store["/haus/privat/y.ics"] = ics(("p1", "geheim"))
        s = self.run_it(self.google)
        self.assertEqual(s["findings"], [], s)
        self.assertIn("/homeassistant/", self.r.homes, "HA's home is made: HA itself may only read")
        self.assertEqual(len(self.r.items("/homeassistant/lea-google-familie/")), 1)
        self.assertIn("Müll raus", "".join(self.r.items("/homeassistant/haus-haushalt/").values()))
        self.assertIn("/homeassistant/haus-familie-geburtstage/", self.r.cols)
        self.assertNotIn("/homeassistant/haus-privat/", self.r.cols, "only the family's calendars go to HA")
        self.assertNotIn("geheim", "".join(self.r.items("/homeassistant/").values()))
        self.assertFalse([m for m in self.r.methods if m.startswith(("PUT /haus/", "DELETE /haus/", "PROPPATCH /haus/"))], "Haus's calendars are only read")
        self.run_it(self.google, ha="")
        self.assertIn("/homeassistant/lea-google-familie/", self.r.cols, "HA_USER emptied: its copies are left alone")

    def test_a_second_run_writes_only_what_changed_and_removes_what_the_source_dropped(self):
        self.src.cals[self.gpath] = ics(("a@google.com", "Zahnarzt"), ("b@google.com", "Elternabend"))
        self.run_it(self.google, ha="")
        self.assertEqual(self.r.puts, 2)
        self.run_it(self.google, ha="")
        self.assertEqual(self.r.puts, 2, "an unchanged event was written again")
        self.src.cals[self.gpath] = ics(("a@google.com", "Zahnarzt 11 Uhr"))
        self.run_it(self.google, ha="")
        self.assertEqual(self.r.puts, 3)
        self.assertEqual(len(self.r.items("/lea/google-familie/")), 1)
        self.assertIn("Zahnarzt 11 Uhr", list(self.r.items("/lea/google-familie/").values())[0])

    def test_trennen_removes_the_copy_and_the_has_copy_and_nothing_else(self):
        self.src.cals[self.gpath] = ics(("a@google.com", "Zahnarzt"))
        self.r.cols["/lea/google-kontakte/"] = "Takeout-Kontakte"
        self.run_it(self.google)
        self.run_it()
        self.assertNotIn("/lea/google-familie/", self.r.cols)
        self.assertNotIn("/homeassistant/lea-google-familie/", self.r.cols)
        self.assertIn("/lea/google-kontakte/", self.r.cols, "a collection the mirror never made is never deleted")
        self.assertEqual(self.r.items("/lea/google-familie/"), {})

    def test_an_empty_answer_keeps_the_copy_and_says_so(self):
        self.src.cals[self.gpath] = ics(("a@google.com", "Zahnarzt"))
        self.run_it(self.google, ha="")
        self.src.cals[self.gpath] = ics()
        s = self.run_it(self.google, ha="")
        self.assertEqual(len(self.r.items("/lea/google-familie/")), 1)
        self.assertEqual([f["severity"] for f in s["findings"]], ["gelb"])

    def test_google_forgetting_the_address_is_yellow_with_the_way_back_in_haus(self):
        self.src.cals[self.gpath] = ics(("a@google.com", "Zahnarzt"))
        self.run_it(self.google)
        del self.src.cals[self.gpath]                           # reset in Google: 404
        s = self.run_it(self.google)
        self.assertEqual(len(s["findings"]), 1, s)
        f = s["findings"][0]
        self.assertEqual(f["severity"], "gelb")
        self.assertIn("geheime Adresse nicht mehr", f["detail"])
        self.assertIn("Haus", f["fix"])
        self.assertIn("geheime Adresse nicht mehr", self.r.props["/lea/google-familie/"])
        self.assertEqual(len(self.r.items("/lea/google-familie/")), 1, "a failed fetch deletes nothing")

    def test_a_failure_is_quiet_until_the_last_good_run_is_older_than_two_hours(self):
        pub = {"person": "haus", "name": "muell", "kind": "abo", "title": "Müllabfuhr", "url": self.src.url + "/muell.ics"}
        self.src.cals["/muell.ics"] = ics(("m1", "Gelbe Tonne"))
        self.run_it(pub, ha="")
        self.src.cals["/muell.ics"] = "<html>Wartung</html>"
        s = self.run_it(pub, ha="")
        self.assertEqual(s["findings"], [], "one failed run of a public calendar is no finding yet")
        st = json.loads(Path(self.data, "state.json").read_text())
        st["haus/abo-muell/"]["ok"] -= 3 * 3600
        Path(self.data, "state.json").write_text(json.dumps(st))
        s = self.run_it(pub, ha="", STALE_HOURS="2")
        self.assertEqual([f["severity"] for f in s["findings"]], ["gelb"], s)
        self.assertIn("seit 3 h", s["findings"][0]["detail"])
        self.assertIn("liefert keinen Kalender", s["findings"][0]["detail"])

    def test_openholidays_gets_its_date_range(self):
        pub = {"person": "haus", "name": "feiertage", "kind": "abo", "title": "Feiertage",
               "url": self.src.url + "/PublicHolidays?countryIsoCode=DE&subdivisionCode=DE-NI&languageIsoCode=DE"}
        me = load(SCRIPT, "kalender")
        u = me.source_url(pub["url"].replace(self.src.url, "https://openholidaysapi.org"), 1792000000)   # 2026-10
        q = dict(urllib.parse.parse_qsl(urllib.parse.urlsplit(u).query))
        self.assertEqual((q["validFrom"], q["validTo"], q["subdivisionCode"]), ("2025-01-01", "2027-12-31", "DE-NI"))
        self.assertEqual(me.source_url(pub["url"], 1792000000), pub["url"], "another host keeps its address")

    def test_a_refused_service_account_is_red(self):
        self.src.cals[self.gpath] = ics(("a@google.com", "Zahnarzt"))
        s = self.run_it(self.google, ha="", pw="alt")
        self.assertEqual({f["severity"] for f in s["findings"]}, {"rot"})
        self.assertIn("Konto kalender", s["findings"][0]["detail"])

    def test_a_person_who_never_opened_radicale_is_told_how(self):
        self.r.homes.discard("/lea/")
        self.src.cals[self.gpath] = ics(("a@google.com", "Zahnarzt"))
        s = self.run_it(self.google, ha="")
        self.assertEqual([f["severity"] for f in s["findings"]], ["gelb"])
        self.assertIn("noch nie geöffnet", s["findings"][0]["detail"])

    def test_nothing_connected_is_no_finding_and_a_broken_list_is_red(self):
        s = self.run_it(ha="")
        self.assertEqual(s["findings"], [])
        self.assertIn("noch kein Kalender", s["detail"])
        e = dict(os.environ, DATA=self.data, RADICALE_URL=self.r.url, KALENDER_ABOS="kein-blob")
        subprocess.run([sys.executable, str(SCRIPT)], env=e, capture_output=True, timeout=30)
        s = json.loads(Path(self.data, "laib-status.json").read_text())
        self.assertEqual([f["severity"] for f in s["findings"]], ["rot"])


class Contract(unittest.TestCase):
    def test_the_same_items_as_the_takeout_import(self):
        imp, me = load(IMPORT, "import_google"), load(SCRIPT, "kalender")
        text = ics(("w@google.com", "Turnen"), ("w@google.com", "später"))
        self.assertEqual(set(imp.events(text)[1]), set(me.events(text)))
        nouid = "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nDTSTART:20260101\r\nSUMMARY:Oma\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
        self.assertEqual(set(imp.events(nouid)[1]), set(me.events(nouid)), "a UID Google did not give differs")
        self.assertEqual(imp.NS_CAL, me.NS_CAL)
        self.assertEqual(me.collection({"person": "lea", "name": "familie", "kind": "google"}), "lea/" + imp.collection("Familie") + "/")

    def test_the_unit_hands_the_addresses_over_off_argv(self):
        unit = (HERE / "kalender-abgleich.service").read_text()
        exec_line = [l for l in unit.splitlines() if l.startswith("ExecStart=")][0]
        for var in ("KALENDER_ABOS", "RADICALE_PASSWORD"):
            self.assertIn(f"-e {var} ", exec_line)
        self.assertNotIn("${ABOS}", exec_line)
        self.assertNotIn("${RADICALE_PASSWORD}", exec_line)
        self.assertNotIn("[Install]", unit, "a timer's oneshot: laib verifies the timer, starts this as the status check")
        self.assertIn("check: kalender-abgleich.service", (HERE / "laib.yml").read_text())


if __name__ == "__main__":
    unittest.main()
