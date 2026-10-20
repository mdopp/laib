"""#495: google-kalender.py mirrors each person's Google calendars one way
into their Radicale collection google-<kalender>, as that person, over
CalDAV. A fake Google (token endpoint, CalDAV REPORT) and a fake Radicale
(owner_only, keeps what is PUT) stand in; the script runs as the timer runs
it, the accounts in the environment. Its status file is what laib diag
shows."""
import base64, importlib.util, json, os, subprocess, sys, tempfile, threading, unittest, urllib.parse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

HERE = Path(__file__).resolve().parent.parent
SCRIPT = HERE / "google-kalender.py"
IMPORT = HERE.parent.parent / "scripts" / "import-google.py"


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


class FakeGoogle:
    """refresh token → access token; REPORT on /<calendar>/events/ answers the calendar's resources"""
    def __init__(self):
        self.cals, self.tokens, self.methods = {}, {"rt-lea": "at-lea"}, []
        outer = self

        class H(Base):
            def do_POST(self):
                form = dict(x.split("=", 1) for x in self.body().decode().split("&"))
                if form.get("client_secret") != "cs" or form.get("refresh_token") not in outer.tokens:
                    return self.answer(400, b'{"error":"invalid_grant"}', "application/json")
                self.answer(200, json.dumps({"access_token": outer.tokens[form["refresh_token"]]}).encode(), "application/json")

            def do_REPORT(self):
                self.body()
                outer.methods.append("REPORT")
                if self.headers.get("Authorization") != "Bearer at-lea":
                    return self.answer(401)
                cal = urllib.parse.unquote(self.path.split("/")[-3])
                if cal not in outer.cals:
                    return self.answer(404)
                rs = "".join(f"<D:response><D:href>/caldav/v2/{cal}/events/{i}.ics</D:href><D:propstat><D:prop><D:getetag>\"{i}\"</D:getetag>"
                             f"<C:calendar-data>{t.replace('&', '&amp;').replace('<', '&lt;')}</C:calendar-data></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response>"
                             for i, t in enumerate(outer.cals[cal]))
                self.answer(207, f'<D:multistatus xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">{rs}</D:multistatus>'.encode())

            def __getattr__(self, name):
                if name.startswith("do_"):   # anything that would write to Google
                    outer.methods.append(name[3:])
                    return lambda: self.answer(405)
                raise AttributeError(name)

        self.srv, self.url = serve(H)


class FakeRadicale:
    def __init__(self, user, pw):
        self.store, self.cols = {}, {}
        outer, want = self, "Basic " + base64.b64encode(f"{user}:{pw}".encode()).decode()

        class H(Base):
            def authed(self):
                if self.headers.get("Authorization") != want:
                    self.answer(401); return False
                if not self.path.startswith(f"/{user}/"):
                    self.answer(403); return False   # owner_only
                return True

            def do_PROPFIND(self):
                if not self.authed():
                    return
                self.body()
                if self.path not in outer.cols:
                    return self.answer(404)
                hrefs = [self.path] + ([p for p in outer.store if p.startswith(self.path)] if self.headers.get("Depth") == "1" else [])
                rs = "".join(f"<D:response><D:href>{h}</D:href></D:response>" for h in hrefs)
                self.answer(207, f'<D:multistatus xmlns:D="DAV:">{rs}</D:multistatus>'.encode())

            def do_MKCALENDAR(self):
                if self.authed():
                    outer.cols[self.path] = self.body().decode(); self.answer(201)

            def do_PUT(self):
                if self.authed():
                    col = self.path.rsplit("/", 1)[0] + "/"
                    if col not in outer.cols:
                        return self.answer(409)
                    outer.store[self.path] = self.body().decode(); outer.puts += 1; self.answer(201)

            def do_DELETE(self):
                if self.authed():
                    outer.store.pop(self.path, None); self.answer(204)

        self.puts = 0
        self.srv, self.url = serve(H)


def blob(konten, secret="cs"):
    raw = json.dumps({"client_id": "cid", "client_secret": secret, "konten": konten}).encode()
    return base64.urlsafe_b64encode(raw).decode().rstrip("=")


LEA = {"user": "lea", "password": "pw-lea", "token": "rt-lea", "kalender": [{"id": "lea@gmail.com", "name": "Familie"}]}


class Mirror(unittest.TestCase):
    def setUp(self):
        self.g, self.r = FakeGoogle(), FakeRadicale("lea", "pw-lea")
        self.data = tempfile.mkdtemp()
        self.addCleanup(lambda: [(s.srv.shutdown(), s.srv.server_close()) for s in (self.g, self.r)])

    def run_it(self, konten=None, raw=None, **env):
        e = dict(os.environ, DATA=self.data, RADICALE_URL=self.r.url, GOOGLE_TOKEN_URL=self.g.url + "/token",
                 GOOGLE_CALDAV_URL=self.g.url + "/caldav/v2", GOOGLE_KALENDER_KONTEN=raw if raw is not None else blob(konten or [LEA]), **env)
        p = subprocess.run([sys.executable, "-"], input=SCRIPT.read_text(), env=e, capture_output=True, text=True, timeout=60)
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertNotIn("pw-lea", p.stdout + p.stderr)
        self.assertNotIn("rt-lea", p.stdout + p.stderr)
        return json.loads(Path(self.data, "laib-status.json").read_text())

    def test_events_land_in_the_persons_google_collection_on_the_import_paths(self):
        self.g.cals["lea@gmail.com"] = [ics(("a@google.com", "Zahnarzt")), ics(("w@google.com", "Turnen"), ("w@google.com", "Turnen später"))]
        s = self.run_it()
        self.assertEqual(s["findings"], [], s)
        self.assertIn("2 Termine", s["detail"])
        self.assertIn("/lea/google-familie/", self.r.cols)
        self.assertIn("Familie", self.r.cols["/lea/google-familie/"])
        imp = load(IMPORT, "import_google")
        want = {f"/lea/{imp.collection('Familie')}/{imp.uuid.uuid5(imp.NS_CAL, uid)}.ics" for uid in ("a@google.com", "w@google.com")}
        self.assertEqual(set(self.r.store), want, "not the paths scripts/import-google.py writes: a Takeout import would double")
        weekly = [v for k, v in self.r.store.items() if "w@google.com" in v][0]
        self.assertEqual(weekly.count("BEGIN:VEVENT"), 2)      # the override goes with its series
        self.assertIn("TZID:Europe/Berlin", weekly)
        self.assertEqual(self.g.methods, ["REPORT"], "it may only read from Google")
        self.assertRegex(s["at"], r"^\d{4}-\d\d-\d\dT")

    def test_a_second_run_writes_only_what_changed_and_removes_what_google_dropped(self):
        self.g.cals["lea@gmail.com"] = [ics(("a@google.com", "Zahnarzt")), ics(("b@google.com", "Elternabend"))]
        self.run_it()
        self.assertEqual(self.r.puts, 2)
        self.run_it()
        self.assertEqual(self.r.puts, 2, "an unchanged event was written again")
        self.g.cals["lea@gmail.com"] = [ics(("a@google.com", "Zahnarzt 11 Uhr"))]
        self.run_it()
        self.assertEqual(self.r.puts, 3)
        self.assertEqual(len(self.r.store), 1)
        self.assertIn("Zahnarzt 11 Uhr", list(self.r.store.values())[0])

    def test_a_takeout_import_before_is_replaced_not_doubled(self):
        imp = load(IMPORT, "import_google")
        col = f"/lea/{imp.collection('Familie')}/"
        self.r.cols[col] = "Familie"
        for uid, text in imp.events(ics(("a@google.com", "Zahnarzt")))[1].items():
            self.r.store[col + f"{imp.uuid.uuid5(imp.NS_CAL, uid)}.ics"] = text
        self.g.cals["lea@gmail.com"] = [ics(("a@google.com", "Zahnarzt"))]
        self.run_it()
        self.assertEqual(len(self.r.store), 1)

    def test_an_empty_answer_from_google_keeps_the_copy_and_says_so(self):
        self.g.cals["lea@gmail.com"] = [ics(("a@google.com", "Zahnarzt"))]
        self.run_it()
        self.g.cals["lea@gmail.com"] = []
        s = self.run_it()
        self.assertEqual(len(self.r.store), 1)
        self.assertEqual([f["severity"] for f in s["findings"]], ["gelb"])

    def test_an_expired_google_token_is_red_with_the_fix(self):
        self.g.cals["lea@gmail.com"] = [ics(("a@google.com", "Zahnarzt"))]
        s = self.run_it([dict(LEA, token="rt-alt")])
        self.assertEqual(len(s["findings"]), 1, s)
        f = s["findings"][0]
        self.assertEqual(f["severity"], "rot")
        self.assertIn("abgelaufen", f["detail"])
        self.assertIn("konten", f["fix"])
        self.assertEqual(self.r.store, {})

    def test_a_refused_radicale_password_is_red(self):
        self.g.cals["lea@gmail.com"] = [ics(("a@google.com", "Zahnarzt"))]
        s = self.run_it([dict(LEA, password="alt")])
        self.assertEqual([f["severity"] for f in s["findings"]], ["rot"])
        self.assertIn("Radicale lehnt lea ab", s["findings"][0]["detail"])

    def test_a_failure_is_yellow_until_the_last_good_run_is_older_than_stale_hours(self):
        self.g.cals["lea@gmail.com"] = [ics(("a@google.com", "Zahnarzt"))]
        self.run_it()
        del self.g.cals["lea@gmail.com"]                       # Google answers 404 now
        s = self.run_it()
        self.assertEqual([f["severity"] for f in s["findings"]], ["gelb"], s)
        st = json.loads(Path(self.data, "state.json").read_text())
        st["lea/lea@gmail.com"]["ok"] -= 7 * 3600              # the last good run, seven hours ago
        Path(self.data, "state.json").write_text(json.dumps(st))
        s = self.run_it(STALE_HOURS="6")
        self.assertEqual([f["severity"] for f in s["findings"]], ["rot"], s)
        self.assertIn("seit 7 h", s["findings"][0]["detail"])

    def test_not_set_up_yet_is_a_finding_not_a_failed_unit(self):
        s = self.run_it(raw="")
        self.assertEqual([f["severity"] for f in s["findings"]], ["gelb"])
        self.assertIn("noch kein", s["detail"])
        s = self.run_it(raw="kein-blob")
        self.assertEqual([f["severity"] for f in s["findings"]], ["rot"])


class Contract(unittest.TestCase):
    def test_the_same_items_as_the_takeout_import(self):
        imp, me = load(IMPORT, "import_google"), load(SCRIPT, "google_kalender")
        text = ics(("w@google.com", "Turnen"), ("w@google.com", "später"))
        self.assertEqual(set(imp.events(text)[1]), set(me.events(text)))
        nouid = "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nDTSTART:20260101\r\nSUMMARY:Oma\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
        self.assertEqual(set(imp.events(nouid)[1]), set(me.events(nouid)), "a UID Google did not give differs")
        self.assertEqual(imp.NS_CAL, me.NS_CAL)
        for name in ("Familie", "Google", "google-arbeit", "Müllabfuhr & Schule"):
            self.assertEqual(imp.collection(name), me.collection(name), name)

    def test_the_unit_hands_the_accounts_over_off_argv(self):
        unit = (HERE / "google-kalender-sync.service").read_text()
        exec_line = [l for l in unit.splitlines() if l.startswith("ExecStart=")][0]
        self.assertIn("-e GOOGLE_KALENDER_KONTEN ", exec_line)
        self.assertNotIn("${KONTEN}", exec_line)
        self.assertNotIn("[Install]", unit, "a timer's oneshot: laib verifies the timer, starts this as the status check")
        self.assertIn("check: google-kalender-sync.service", (HERE / "laib.yml").read_text())


if __name__ == "__main__":
    unittest.main()
