"""#521: Paperless' mail inbox from values. paperless-mail.py setup makes or
takes over the IMAP account and the rule through Paperless' API and is
idempotent; check writes laib's status from Paperless' own connection test.
Against a fake Paperless API, never a real one."""
import base64, json, os, subprocess, sys, tempfile, threading, unittest
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent.parent / "paperless-mail.py"
AUTH = "Basic " + base64.b64encode(b"admin:adminpw").decode()


class Fake:
    """Paperless' mail endpoints: accounts and rules in memory, the test answers ok/refused"""

    def __init__(self, accounts=None, rules=None, test_ok=True):
        self.accounts, self.rules, self.test_ok, self.calls, self.tested = accounts or [], rules or [], test_ok, [], []
        fake = self

        class H(BaseHTTPRequestHandler):
            def log_message(self, *a):
                pass

            def send(self, code, body):
                b = json.dumps(body).encode()
                self.send_response(code)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(b)))
                self.end_headers()
                self.wfile.write(b)

            def handle_any(self, method):
                if self.headers.get("Authorization") != AUTH:
                    return self.send(401, {"detail": "no"})
                n = int(self.headers.get("Content-Length") or 0)
                body = json.loads(self.rfile.read(n)) if n else None
                path = self.path.split("?")[0]
                fake.calls.append((method, path, body))
                coll = {"/api/mail_accounts/": fake.accounts, "/api/mail_rules/": fake.rules}
                if path == "/api/mail_accounts/test/":
                    fake.tested.append(body)
                    return self.send(200, {"success": True}) if fake.test_ok else self.send(400, "Unable to connect to server")
                if path in coll and method == "GET":
                    return self.send(200, {"count": len(coll[path]), "next": None, "results": coll[path]})
                if path in coll and method == "POST":
                    obj = {**body, "id": len(coll[path]) + 1}
                    if path == "/api/mail_accounts/":
                        obj["password"] = "**********"
                    coll[path].append(obj)
                    return self.send(201, obj)
                for base, items in coll.items():
                    if path.startswith(base) and method == "PATCH":
                        obj = next(o for o in items if o["id"] == int(path[len(base):].strip("/")))
                        obj.update(body)
                        if base == "/api/mail_accounts/":
                            obj["password"] = "**********"
                        return self.send(200, obj)
                self.send(404, {})

            def do_GET(self):
                self.handle_any("GET")

            def do_POST(self):
                self.handle_any("POST")

            def do_PATCH(self):
                self.handle_any("PATCH")

        self.srv = HTTPServer(("127.0.0.1", 0), H)
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()
        self.url = "http://127.0.0.1:%d" % self.srv.server_port

    def writes(self):
        return [c for c in self.calls if c[0] != "GET" and c[1] != "/api/mail_accounts/test/"]


MAIL = {"PAPERLESS_MAIL_HOST": "imap.strato.de", "PAPERLESS_MAIL_PORT": "993", "PAPERLESS_MAIL_SECURITY": "ssl",
        "PAPERLESS_MAIL_USER": "webmaster@example.org", "PAPERLESS_MAIL_FILTER_TO": "dokumente@example.org", "PAPERLESS_MAIL_FOLDER": "INBOX"}


class PaperlessMail(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        os.makedirs(os.path.join(self.tmp, "paperless", "data"))

    def run_setup(self, fake, values, password="mailpw"):
        env = {"PATH": os.environ["PATH"], "PAPERLESS_API": fake.url, "ADMIN_USER": "admin", "LAIB_DATA_DIR": self.tmp,
               "LAIB_SECRET_PAPERLESS_ADMIN_PASSWORD": "adminpw", "LAIB_SECRET_PAPERLESS_MAIL_PASSWORD": password, **values}
        return subprocess.run([sys.executable, str(SCRIPT), "setup"], env=env, capture_output=True, text=True, timeout=60)

    def status(self):
        with open(os.path.join(self.tmp, "paperless", "data", "laib-status.json")) as f:
            return json.load(f)

    def test_makes_the_account_and_the_rule_then_changes_nothing(self):
        fake = Fake()
        r = self.run_setup(fake, MAIL)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(len(fake.accounts), 1)
        acc = fake.accounts[0]
        self.assertEqual((acc["imap_server"], acc["imap_port"], acc["imap_security"], acc["username"]), ("imap.strato.de", 993, 2, "webmaster@example.org"))
        posted = next(c[2] for c in fake.calls if c[:2] == ("POST", "/api/mail_accounts/"))
        self.assertEqual(posted["password"], "mailpw")
        rule = fake.rules[0]
        # the owner's decision (#521): only mail to dokumente@, attachments only, flag after processing
        self.assertEqual((rule["account"], rule["filter_to"], rule["attachment_type"], rule["consumption_scope"], rule["action"]), (1, "dokumente@example.org", 1, 1, 4))
        self.assertEqual((rule["assign_title_from"], rule["assign_owner_from_rule"], rule["folder"]), (1, False, "INBOX"))
        self.assertNotIn("mailpw", r.stdout + r.stderr)
        self.assertEqual(self.status()["findings"], [])
        self.assertIn("dokumente@example.org", self.status()["detail"])
        # again: the same account and rule, the rule untouched, the password set again (it cannot be compared)
        fake.calls.clear()
        r = self.run_setup(fake, MAIL)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual((len(fake.accounts), len(fake.rules)), (1, 1))
        self.assertEqual([c[:2] for c in fake.writes()], [("PATCH", "/api/mail_accounts/1/")])
        self.assertIn("Regel unverändert", r.stdout)

    def test_takes_over_the_hand_made_ones(self):
        # paperless-mail.sh's account and rule (2026-10-21): other names, the rule marking read
        fake = Fake(accounts=[{"id": 7, "name": "Strato", "imap_server": "IMAP.strato.de", "imap_port": 993, "imap_security": 2,
                               "username": "Webmaster@example.org", "password": "**********", "character_set": "UTF-8", "account_type": 1, "is_token": False}],
                    rules=[{"id": 3, "name": "dokumente", "account": 7, "filter_to": "dokumente@example.org", "action": 3, "enabled": True}])
        r = self.run_setup(fake, MAIL)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual((len(fake.accounts), len(fake.rules)), (1, 1))
        self.assertEqual(fake.rules[0]["action"], 4)
        self.assertEqual(fake.accounts[0]["imap_server"], "imap.strato.de")

    def test_without_a_host_nothing_changes(self):
        fake = Fake(accounts=[{"id": 1, "imap_server": "x", "username": "y"}])
        r = self.run_setup(fake, {"PAPERLESS_MAIL_HOST": ""}, password="")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(fake.writes(), [])
        self.assertEqual(self.status(), {**self.status(), "detail": "", "findings": []})

    def test_a_host_without_a_password_is_refused(self):
        r = self.run_setup(Fake(), MAIL, password="")
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("PAPERLESS_MAIL_PASSWORD", r.stderr)

    def test_a_refused_login_is_a_red_finding(self):
        fake = Fake(test_ok=False)
        r = self.run_setup(fake, MAIL)
        self.assertEqual(r.returncode, 0, r.stderr)   # the account is written; the mailbox's trouble is diag's, not apply's
        st = self.status()
        self.assertEqual(st["findings"][0]["severity"], "rot")
        self.assertIn("webmaster@example.org auf imap.strato.de", st["findings"][0]["detail"])
        self.assertIn("laib secret set PAPERLESS_MAIL_PASSWORD", st["findings"][0]["fix"])
        # Paperless tests with its stored password: the id and the mask, never the secret
        self.assertEqual((fake.tested[-1]["id"], fake.tested[-1]["password"]), (1, "**********"))

    def test_check_inside_the_container(self):
        # the account exists, the rule is gone: red, and what to do
        fake = Fake(accounts=[{"id": 1, "imap_server": "imap.strato.de", "username": "webmaster@example.org", "imap_port": 993}])
        out = os.path.join(self.tmp, "st.json")
        env = {"PATH": os.environ["PATH"], "PAPERLESS_API": fake.url, "PAPERLESS_ADMIN_USER": "admin", "PAPERLESS_ADMIN_PASSWORD": "adminpw",
               "LAIB_STATUS_FILE": out, **MAIL}
        r = subprocess.run([sys.executable, "-", "check"], input=SCRIPT.read_text(), env=env, capture_output=True, text=True, timeout=60)
        self.assertEqual(r.returncode, 0, r.stderr)
        with open(out) as f:
            st = json.load(f)
        self.assertEqual(st["findings"][0]["severity"], "rot")
        self.assertIn("die Regel", st["findings"][0]["detail"])
        self.assertEqual(fake.writes(), [])   # check only looks


if __name__ == "__main__":
    unittest.main()
