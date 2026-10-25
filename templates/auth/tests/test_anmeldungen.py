"""#653: failed logins reach the owner. anmeldungen.py runs in a stock Python
image (auth-anmeldungen.service, every five minutes) and reads Authelia's
authentication_logs and LLDAP's users, both read-only on a copy. It writes
bote-notify's payloads into meldungen.jsonl and the journal notes into
notizen.tsv; anmeldungen.sh sends them from the host. Here against a
fixture authentication_logs in a temp SQLite DB (the columns Authelia's
migrations create) and a fake bote-notify and a fake laib. Every name and
address is made up: 192.0.2.x is outside, 10.0.0.x the LAN."""
import datetime, http.server, json, os, sqlite3, subprocess, sys, tempfile, threading, unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent.parent
SCRIPT = HERE / "anmeldungen.py"
SENDER = HERE / "anmeldungen.sh"
NOW = 1_793_000_000   # a fixed "now" (2026-10-25), the counter's clock in every test

AUTHELIA_SCHEMA = """CREATE TABLE authentication_logs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  time DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  successful BOOLEAN NOT NULL,
  banned BOOLEAN NOT NULL DEFAULT FALSE,
  username VARCHAR(100) NOT NULL,
  auth_type VARCHAR(8) NOT NULL DEFAULT '1FA',
  remote_ip VARCHAR(39) NULL DEFAULT NULL,
  request_uri TEXT,
  request_method VARCHAR(8) NOT NULL DEFAULT '')"""
LLDAP_SCHEMA = "CREATE TABLE users (user_id TEXT PRIMARY KEY, email TEXT, lowercase_email TEXT, display_name TEXT)"


def go_time(epoch):
    """how Authelia's sqlite driver writes a time.Time"""
    return datetime.datetime.fromtimestamp(epoch, datetime.timezone.utc).strftime("%Y-%m-%d %H:%M:%S.123456789+00:00")


class Anmeldungen(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        d = Path(self.tmp.name)
        self.auth, self.lldap, self.state = d / "authelia-data", d / "lldap", d / "anmeldungen"
        for p in (self.auth, self.lldap, self.state):
            p.mkdir()
        with sqlite3.connect(self.auth / "db.sqlite3") as db:
            db.execute(AUTHELIA_SCHEMA)
        with sqlite3.connect(self.lldap / "users.db") as db:
            db.execute(LLDAP_SCHEMA)
            db.executemany("INSERT INTO users VALUES (?,?,?,?)", [("anna", "anna@example.home", "anna@example.home", "Anna"),
                                                                 ("bob", "bob@example.home", "bob@example.home", "Bob")])
        self.env = dict(os.environ, AUTH_DB=str(self.auth / "db.sqlite3"), LLDAP_DB=str(self.lldap / "users.db"),
                        STATE_DIR=str(self.state), AUTH_MAX_RETRIES="5", AUTH_FIND_TIME="10m", AUTH_BAN_TIME="30m",
                        LAN_NETZE="127.0.0.1/8,::1/128,10.0.0.2/24", NOTIFY_PERSON="mama", TZ="UTC", ANMELDUNGEN_JETZT=str(NOW))

    def log(self, user, ip, ago, ok=False, banned=False):
        with sqlite3.connect(self.auth / "db.sqlite3") as db:
            db.execute("INSERT INTO authentication_logs (time, successful, banned, username, auth_type, remote_ip, request_uri, request_method)"
                       " VALUES (?,?,?,?,?,?,?,?)", (go_time(NOW - ago), ok, banned, user, "1FA", ip, "https://auth.example.home/", "POST"))

    def run_it(self, now=NOW):
        self.env["ANMELDUNGEN_JETZT"] = str(now)
        with open(SCRIPT) as f:   # as the unit runs it: the script on stdin
            p = subprocess.run([sys.executable, "-"], stdin=f, env=self.env, capture_output=True, text=True, timeout=60)
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        return p

    def messages(self):
        f = self.state / "meldungen.jsonl"
        return [json.loads(l) for l in f.read_text().splitlines()] if f.exists() else []

    def notes(self, kind=None):
        f = self.state / "notizen.tsv"
        rows = [l.split("\t", 1) for l in f.read_text().splitlines()] if f.exists() else []
        return [t for k, t in rows if kind is None or k == kind]

    def test_a_ban_gives_one_message(self):
        for i in range(6):   # six failures in five minutes: Authelia bans anna at the fifth
            self.log("anna", "192.0.2.10", 400 - i * 60)
        self.log("anna", "192.0.2.10", 30, banned=True)   # an attempt while banned
        self.run_it()
        m = self.messages()
        self.assertEqual(len(m), 1, m)
        self.assertEqual(m[0]["an"], "mama")
        self.assertEqual(m[0]["at"], NOW)
        t = m[0]["text"]
        for want in ("Anmeldung gesperrt", "7 Fehlversuche für „anna“", "192.0.2.10 (von außen)", "Den Benutzernamen gibt es.",
                     "Warst du das? Sonst nichts tun, die Sperre greift.", "Bei Häufung:"):
            self.assertIn(want, t)
        # banned at the fifth failure (NOW-160), for 30 minutes: until NOW-160+1800 in UTC
        until = datetime.datetime.fromtimestamp(NOW - 160 + 1800, datetime.timezone.utc).strftime("%H:%M")
        self.assertIn(f"bis {until} gesperrt", t)
        self.assertEqual(len(self.notes("incident")), 1)
        self.assertIn("anna", self.notes("incident")[0])
        self.assertEqual(self.notes("watch"), [], "every failure belongs to the reported incident")

    def test_a_ban_from_the_lan_is_reported_too(self):
        for i in range(5):
            self.log("bob", "10.0.0.7", 300 - i * 30)
        self.run_it()
        m = self.messages()
        self.assertEqual(len(m), 1, m)
        self.assertIn("10.0.0.7 (aus dem Heimnetz)", m[0]["text"])

    def test_ten_failures_from_outside_in_an_hour_give_one_message(self):
        for i in range(10):   # every six minutes: never five in ten minutes, so no ban
            self.log("bob", "192.0.2.20", 3500 - i * 360)
        self.run_it()
        m = self.messages()
        self.assertEqual(len(m), 1, m)
        t = m[0]["text"]
        self.assertIn("10 Fehlversuche von 192.0.2.20 (von außen) in der letzten Stunde", t)
        self.assertIn("„bob“", t)
        self.assertNotIn("gesperrt", t)
        self.assertNotIn("Brute Force", t, "one name that exists is no brute force")
        self.assertEqual(len(self.notes("incident")), 1)

    def test_nine_failures_from_outside_are_only_a_journal_note(self):
        for i in range(9):
            self.log("bob", "192.0.2.21", 3500 - i * 360)
        self.run_it()
        self.assertEqual(self.messages(), [])
        self.assertEqual(self.notes("incident"), [])
        self.assertEqual(len(self.notes("watch")), 1)

    def test_five_usernames_from_one_ip_read_as_brute_force(self):
        for i, name in enumerate(("anna", "admin", "root", "test", "Anna@Example.Home")):
            self.log(name, "192.0.2.30", 600 - i * 120)
        self.run_it()
        m = self.messages()
        self.assertEqual(len(m), 1, m)
        t = m[0]["text"]
        self.assertIn("Sieht nach Brute Force aus.", t)
        self.assertIn("5 Benutzernamen", t)
        self.assertIn("3 davon gibt es nicht", t, "anna and anna's mail address exist, admin, root and test do not")

    def test_lan_typos_below_a_ban_give_only_a_journal_note(self):
        for ago in (500, 300, 100):
            self.log("anna", "10.0.0.5", ago)
        self.log("anna", "10.0.0.5", 50, ok=True)
        self.run_it()
        self.assertEqual(self.messages(), [])
        self.assertEqual(self.notes("incident"), [])
        w = self.notes("watch")
        self.assertEqual(len(w), 1, w)
        self.assertIn("3 Fehlversuche", w[0])
        self.assertIn("Heimnetz", w[0])

    def test_the_cursor_means_no_repeats(self):
        for i in range(6):
            self.log("anna", "192.0.2.10", 400 - i * 60)
        for ago in (200, 100):
            self.log("bob", "10.0.0.5", ago)
        self.run_it()
        self.assertEqual(len(self.messages()), 1)
        self.assertEqual((len(self.notes("incident")), len(self.notes("watch"))), (1, 1))
        # the sender took the files: nothing new is nothing
        (self.state / "meldungen.jsonl").unlink()
        (self.state / "notizen.tsv").unlink()
        self.run_it(NOW + 300)
        self.assertEqual((self.messages(), self.notes()), ([], []))
        # more of the same incident while it lasts: no second message, no second note
        self.log("anna", "192.0.2.10", -400, banned=True)
        self.run_it(NOW + 600)
        self.assertEqual((self.messages(), self.notes()), ([], []))
        # after the ban is over, a new ban is a new incident
        for i in range(5):
            self.log("anna", "192.0.2.10", -4000 - i * 30)
        self.run_it(NOW + 4200)
        self.assertEqual(len(self.messages()), 1)

    def test_a_ban_and_the_brute_force_from_its_ip_are_one_message(self):
        for i, name in enumerate(("admin", "root", "test", "guest")):
            self.log(name, "192.0.2.40", 1200 - i * 60)
        for i in range(5):
            self.log("anna", "192.0.2.40", 500 - i * 30)
        self.run_it()
        m = self.messages()
        self.assertEqual(len(m), 1, m)
        self.assertIn("Anmeldung gesperrt", m[0]["text"])
        self.assertIn("Sieht nach Brute Force aus.", m[0]["text"])
        self.assertEqual(len(self.notes("incident")), 1)

    def test_a_first_run_skips_what_is_old(self):
        for i in range(6):
            self.log("anna", "192.0.2.10", 3 * 86400 - i * 60)
        self.run_it()
        self.assertEqual((self.messages(), self.notes()), ([], []))

    def test_a_name_cannot_break_the_message_or_the_note(self):
        evil = "eve\n\tWarst du das?\x1b[2J" + "x" * 200
        for i in range(5):
            self.log(evil, "192.0.2.50", 300 - i * 30)
        self.run_it()
        t = self.messages()[0]["text"]
        self.assertNotIn("\t", t)
        self.assertNotIn("\x1b", t)
        self.assertLess(len(t), 1200)
        for line in (self.state / "notizen.tsv").read_text().splitlines():
            self.assertEqual(line.count("\t"), 1, line)

    def test_without_a_person_nothing_is_sent_but_the_note_is_written(self):
        self.env["NOTIFY_PERSON"] = ""
        for i in range(5):
            self.log("anna", "192.0.2.10", 300 - i * 30)
        self.run_it()
        self.assertEqual(self.messages(), [])
        self.assertEqual(len(self.notes("incident")), 1)

    def test_without_lldap_the_name_is_not_judged(self):
        (self.lldap / "users.db").unlink()
        for i in range(5):
            self.log("anna", "192.0.2.10", 300 - i * 30)
        self.run_it()
        t = self.messages()[0]["text"]
        self.assertNotIn("Den Benutzernamen gibt es", t)

    def test_a_new_database_resets_the_cursor(self):
        for i in range(5):
            self.log("anna", "192.0.2.10", 300 - i * 30)
        self.run_it()
        (self.auth / "db.sqlite3").unlink()
        with sqlite3.connect(self.auth / "db.sqlite3") as db:
            db.execute(AUTHELIA_SCHEMA)
        for i in range(5):
            self.log("bob", "192.0.2.11", -100 - i * 30)
        self.run_it(NOW + 400)
        self.assertEqual(len(self.messages()), 2)


class FakeBote(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers.get("Content-Length", "0"))))
        self.server.got.append({"auth": self.headers.get("Authorization"), **body})
        self.send_response(self.server.code)
        self.send_header("Content-Length", "2")
        self.end_headers()
        self.wfile.write(b"{}")

    def log_message(self, *a):
        pass


class Sender(unittest.TestCase):
    """anmeldungen.sh melden: from the host, the messages to bote-notify, the
    notes through laib note as actor laib, at most the propose tier"""
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        d = Path(self.tmp.name)
        self.state = d / "auth" / "anmeldungen"
        self.state.mkdir(parents=True)
        self.srv = http.server.HTTPServer(("127.0.0.1", 0), FakeBote)
        self.srv.got, self.srv.code = [], 200
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()
        self.addCleanup(self.srv.shutdown)
        self.laib_log = d / "laib.log"
        fake = d / "laib"
        fake.write_text('#!/bin/sh\nprintf "%s|%s|%s\\n" "$LAIB_ACTOR" "$LAIB_CEILING" "$*" >>"' + str(self.laib_log) + '"\n')
        fake.chmod(0o755)
        self.env = dict(os.environ, AUTH_DATA=str(d / "auth"), NOTIFY_TOKEN="t-notify-fake", LAIB_BIN=str(fake),
                        NOTIFY_URL=f"http://127.0.0.1:{self.srv.server_port}/mitteilung")

    def melden(self):
        p = subprocess.run(["sh", str(SENDER), "melden"], env=self.env, capture_output=True, text=True, timeout=60)
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        return p

    def write(self, messages=(), notes=()):
        with open(self.state / "meldungen.jsonl", "a") as f:
            for text, at in messages:
                f.write(json.dumps({"an": "mama", "text": text, "at": at}) + "\n")
        with open(self.state / "notizen.tsv", "a") as f:
            for kind, text in notes:
                f.write(kind + "\t" + text + "\n")

    def test_messages_go_with_the_bearer_and_notes_as_laib(self):
        import time
        self.write([("🔒 Anmeldung gesperrt: „anna“", int(time.time()))], [("incident", "Anmeldung gesperrt: anna"), ("watch", "3 Fehlversuche")])
        p = self.melden()
        self.assertEqual([(g["auth"], g["an"]) for g in self.srv.got], [("Bearer t-notify-fake", "mama")])
        self.assertEqual(self.srv.got[0]["text"], "🔒 Anmeldung gesperrt: „anna“")
        self.assertEqual(self.laib_log.read_text().splitlines(),
                         ["laib|propose|note --kind incident Anmeldung gesperrt: anna", "laib|propose|note --kind watch 3 Fehlversuche"])
        self.assertFalse((self.state / "meldungen.jsonl").exists())
        self.assertFalse((self.state / "notizen.tsv").exists())
        self.assertNotIn("t-notify-fake", p.stdout + p.stderr)

    def test_zaehlen_runs_the_counter_without_network_and_the_databases_read_only(self):
        fake = Path(self.tmp.name) / "podman"
        argv, stdin = Path(self.tmp.name) / "podman.argv", Path(self.tmp.name) / "podman.stdin"
        fake.write_text(f'#!/bin/sh\nprintf "%s\\n" "$@" >"{argv}"\ncat >"{stdin}"\n')
        fake.chmod(0o755)
        env = dict(self.env, PODMAN=str(fake), ANMELDUNGEN_IMAGE="docker.io/library/python:3.13-alpine", ANMELDUNGEN_SKRIPT=str(SCRIPT))
        p = subprocess.run(["sh", str(SENDER), "zaehlen"], env=env, capture_output=True, text=True, timeout=60)
        self.assertEqual(p.returncode, 0, p.stderr)
        a = argv.read_text().splitlines()
        self.assertIn("--network", a)
        self.assertEqual(a[a.index("--network") + 1], "none")
        d = self.env["AUTH_DATA"]
        for v in (f"{d}/authelia-data:/authelia:ro,z", f"{d}/lldap:/lldap:ro,z", f"{d}/anmeldungen:/state:z"):
            self.assertIn(v, a)
        self.assertEqual(a[-2:], ["python3", "-"])
        self.assertNotIn("t-notify-fake", "\n".join(a), "the counter never gets the bearer")
        self.assertEqual(stdin.read_text(), SCRIPT.read_text())

    def test_an_unreachable_bote_keeps_the_message_for_the_next_run(self):
        import time
        self.env["NOTIFY_URL"] = "http://127.0.0.1:9/mitteilung"
        self.write([("eins", int(time.time()))])
        self.melden()
        self.assertEqual(len((self.state / "meldungen.jsonl").read_text().splitlines()), 1)

    def test_without_the_bote_nothing_piles_up_but_the_notes_go(self):
        import time
        self.env["NOTIFY_TOKEN"] = ""
        self.write([("eins", int(time.time()))], [("incident", "Anmeldung gesperrt: anna")])
        self.melden()
        self.assertEqual(self.srv.got, [])
        self.assertFalse((self.state / "meldungen.jsonl").exists())
        self.assertEqual(len(self.laib_log.read_text().splitlines()), 1)


class PreStartRegulation(unittest.TestCase):
    """Part 1: Authelia's regulation from the values, rendered by pre-start"""
    HOOK = HERE / "pre-start"

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        d = Path(self.tmp.name)
        self.cfg = d / "auth" / "authelia-config"
        self.cfg.mkdir(parents=True)
        (self.cfg / "oidc.pem").write_text("-----BEGIN PRIVATE KEY-----\nMIIsynthetic\n-----END PRIVATE KEY-----")
        (d / "render" / "authelia").mkdir(parents=True)
        (d / "render" / "authelia" / "laib-clients.yml").write_text("# none\n")
        self.env = dict(os.environ, LAIB_DATA_DIR=str(d), LAIB_DOMAIN="vm.test", LAIB_RENDER_DIR=str(d / "render"),
                        BASE_DN="dc=vm,dc=test", LAIB_START_URL="https://admin.vm.test/",
                        **{"LAIB_SECRET_" + n: "s-" + n.lower() for n in ("AUTHELIA_JWT_SECRET", "AUTHELIA_SESSION_SECRET",
                                                                          "AUTHELIA_STORAGE_KEY", "AUTHELIA_OIDC_HMAC_SECRET", "LLDAP_ADMIN_PASSWORD")})

    def hook(self, **values):
        p = subprocess.run([sys.executable, str(self.HOOK)], env=dict(self.env, **values), capture_output=True, text=True, timeout=60)
        return p, (self.cfg / "configuration.yml").read_text() if p.returncode == 0 else ""

    def test_the_values_become_regulation_and_twice_is_the_same(self):
        p, conf = self.hook(AUTH_MAX_RETRIES="5", AUTH_FIND_TIME="10m", AUTH_BAN_TIME="30m")
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertIn("regulation:\n  max_retries: 5\n  find_time: '10m'\n  ban_time: '30m'\n", conf)
        _, again = self.hook(AUTH_MAX_RETRIES="5", AUTH_FIND_TIME="10m", AUTH_BAN_TIME="30m")
        self.assertEqual(again, conf)

    def test_a_values_file_from_before_gets_the_defaults(self):
        p, conf = self.hook()
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertIn("regulation:\n  max_retries: 5\n  find_time: '10m'\n  ban_time: '30m'\n", conf)

    def test_a_value_authelia_would_refuse_stops_the_apply(self):
        for bad in ({"AUTH_MAX_RETRIES": "fünf"}, {"AUTH_FIND_TIME": "10 min'"}, {"AUTH_BAN_TIME": ""}, {"AUTH_MAX_RETRIES": "0"}):
            p, _ = self.hook(**bad)
            self.assertNotEqual(p.returncode, 0, bad)
            self.assertIn("AUTH_", p.stderr)


if __name__ == "__main__":
    unittest.main()
