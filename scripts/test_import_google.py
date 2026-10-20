"""#372: scripts/import-google.py, ADR 0017's one-shot importer: a Google
Takeout export (Calendar .ics, Contacts .vcf, as the zip or its files) into a
person's Radicale over CalDAV/CardDAV as that person, never into Radicale's
files. A second run duplicates nothing. The Radicale here is a fake DAV
server that keeps what is PUT; the script runs as a process, the password on
stdin."""
import base64, io, os, subprocess, tempfile, threading, unittest, zipfile
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent / "import-google.py"

ICS = """BEGIN:VCALENDAR\r
PRODID:-//Google Inc//Google Calendar 70.9054//EN\r
VERSION:2.0\r
X-WR-CALNAME:Familie\r
BEGIN:VTIMEZONE\r
TZID:Europe/Berlin\r
BEGIN:STANDARD\r
DTSTART:19701025T030000\r
TZOFFSETFROM:+0200\r
TZOFFSETTO:+0100\r
END:STANDARD\r
END:VTIMEZONE\r
BEGIN:VEVENT\r
DTSTART;TZID=Europe/Berlin:20260301T100000\r
SUMMARY:Zahnarzt Lea\r
UID:abc123@google.com\r
END:VEVENT\r
BEGIN:VEVENT\r
DTSTART;TZID=Europe/Berlin:20260101T090000\r
RRULE:FREQ=WEEKLY\r
SUMMARY:Turnen\r
UID:weekly@google.com\r
END:VEVENT\r
BEGIN:VEVENT\r
DTSTART;TZID=Europe/Berlin:20260108T100000\r
RECURRENCE-ID;TZID=Europe/Berlin:20260108T090000\r
SUMMARY:Turnen (später)\r
UID:weekly@google.com\r
END:VEVENT\r
BEGIN:VEVENT\r
DTSTART;VALUE=DATE:20260512\r
SUMMARY:Geburtstag Oma, mit einer sehr langen Beschreibung die Google über die\r
  Zeilengrenze faltet\r
END:VEVENT\r
END:VCALENDAR\r
"""

VCF = """BEGIN:VCARD\r
VERSION:3.0\r
FN:Oma Erna\r
EMAIL;TYPE=INTERNET:erna@example.test\r
END:VCARD\r
BEGIN:VCARD\r
VERSION:3.0\r
TEL;TYPE=CELL:+49 170 0000000\r
END:VCARD\r
"""


class FakeRadicale:
    def __init__(self, user, pw):
        store = self.store = {}
        cols = self.cols = {}
        want = "Basic " + base64.b64encode(f"{user}:{pw}".encode()).decode()

        class H(BaseHTTPRequestHandler):
            def log_message(self, *a): pass

            def answer(self, code, body=b""):
                self.send_response(code); self.send_header("Content-Length", str(len(body))); self.end_headers(); self.wfile.write(body)

            def authed(self):
                if self.headers.get("Authorization") != want:
                    self.answer(401); return False
                if not self.path.startswith(f"/{user}/"):
                    self.answer(403); return False   # owner_only
                return True

            def body(self):
                return self.rfile.read(int(self.headers.get("Content-Length") or 0))

            def do_PROPFIND(self):
                if self.authed():
                    self.body(); self.answer(207 if self.path in cols else 404, b"<multistatus xmlns='DAV:'/>")

            def do_MKCALENDAR(self):
                if self.authed():
                    cols[self.path] = ("calendar", self.body().decode()); self.answer(201)

            def do_MKCOL(self):
                if self.authed():
                    cols[self.path] = ("addressbook", self.body().decode()); self.answer(201)

            def do_PUT(self):
                if self.authed():
                    col = self.path.rsplit("/", 1)[0] + "/"
                    if col not in cols:
                        return self.answer(409)
                    store[self.path] = (self.headers.get("Content-Type"), self.body().decode())
                    self.answer(201)

        self.srv = ThreadingHTTPServer(("127.0.0.1", 0), H)
        self.url = f"http://127.0.0.1:{self.srv.server_address[1]}"
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()

    def close(self):
        self.srv.shutdown(); self.srv.server_close()


class ImportGoogle(unittest.TestCase):
    def setUp(self):
        self.dav = FakeRadicale("lea", "pw-lea")
        self.tmp = tempfile.TemporaryDirectory()
        self.zip = Path(self.tmp.name) / "takeout.zip"
        with zipfile.ZipFile(self.zip, "w") as z:
            z.writestr("Takeout/Kalender/Familie.ics", ICS)
            z.writestr("Takeout/Contacts/Meine Kontakte/Meine Kontakte.vcf", VCF)

    def tearDown(self):
        self.dav.close(); self.tmp.cleanup()

    def run_it(self, *args, pw="pw-lea\n"):
        return subprocess.run(["python3", str(SCRIPT), "--url", self.dav.url, "--user", "lea", "--password-stdin", *args],
                              input=pw, capture_output=True, text=True)

    def items(self, ext):
        return {p: v for p, v in self.dav.store.items() if p.endswith(ext)}

    def test_events_and_cards_land_in_the_persons_collections(self):
        r = self.run_it(str(self.zip))
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertEqual(self.dav.cols["/lea/google-familie/"][0], "calendar")
        self.assertIn("Familie", self.dav.cols["/lea/google-familie/"][1])
        self.assertEqual(self.dav.cols["/lea/google-kontakte/"][0], "addressbook")
        events = self.items(".ics")
        self.assertEqual(len(events), 3, list(events))       # the override goes with its series
        weekly = [v for _, v in events.values() if "weekly@google.com" in v][0]
        self.assertEqual(weekly.count("BEGIN:VEVENT"), 2)
        self.assertIn("TZID:Europe/Berlin", weekly)           # each item carries the timezones it needs
        self.assertTrue(all(ct.startswith("text/calendar") for ct, _ in events.values()))
        oma = [v for _, v in events.values() if "Geburtstag Oma" in v][0]
        self.assertIn("UID:import-", oma)                     # a stable UID where Google gave none
        cards = self.items(".vcf")
        self.assertEqual(len(cards), 2)
        nameless = [v for _, v in cards.values() if "+49 170" in v][0]
        self.assertIn("FN:+49 170 0000000", nameless)         # vCard 3.0 needs FN
        self.assertIn("3 Termine", r.stdout)
        self.assertIn("2 Kontakte", r.stdout)
        self.assertNotIn("pw-lea", r.stdout + r.stderr)

    def test_a_second_run_duplicates_nothing(self):
        self.run_it(str(self.zip))
        first = dict(self.dav.store)
        r = self.run_it(str(self.zip))
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertEqual(sorted(self.dav.store), sorted(first))
        self.assertEqual(self.dav.store, first)

    def test_a_single_ics_file_and_a_named_calendar(self):
        ics = Path(self.tmp.name) / "export.ics"
        ics.write_text(ICS)
        r = self.run_it("--calendar", "Google", str(ics))
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertEqual(len([p for p in self.dav.store if p.startswith("/lea/google/")]), 3)

    def test_a_wrong_password_writes_nothing_and_says_so(self):
        r = self.run_it(str(self.zip), pw="falsch\n")
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("401", r.stdout + r.stderr)
        self.assertEqual(self.dav.store, {})


if __name__ == "__main__":
    unittest.main()
