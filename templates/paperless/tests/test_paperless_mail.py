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

    def __init__(self, accounts=None, rules=None, test_ok=True, tasks=None):
        self.accounts, self.rules, self.test_ok, self.calls, self.tested = accounts or [], rules or [], test_ok, [], []
        self.tasks, self.queries, self.processed = tasks or [], [], []
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
                path, _, query = self.path.partition("?")
                fake.calls.append((method, path, body))
                fake.queries.append((path, query, self.headers.get("Accept", "")))
                coll = {"/api/mail_accounts/": fake.accounts, "/api/mail_rules/": fake.rules}
                if path == "/api/mail_accounts/test/":
                    fake.tested.append(body)
                    return self.send(200, {"success": True}) if fake.test_ok else self.send(400, "Unable to connect to server")
                if path == "/api/tasks/" and method == "GET":
                    # API version 9 (Paperless 2.x, and 3.x for old clients): a plain list, uppercase states
                    if "version=9" not in self.headers.get("Accept", ""):
                        return self.send(406, {"detail": "the probe must pin version 9"})
                    ack = "acknowledged=false" in query
                    return self.send(200, [t for t in fake.tasks if not (ack and t["acknowledged"])])
                if path.startswith("/api/mail_accounts/") and path.endswith("/process/") and method == "POST":
                    fake.processed.append(int(path.split("/")[3]))
                    return self.send(200, {"result": "OK"})
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
        return [c for c in self.calls if c[0] != "GET" and c[1] != "/api/mail_accounts/test/" and not c[1].endswith("/process/")]


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

    def test_without_a_host_the_api_is_not_asked(self):
        # a box moved from ServiceBay keeps ServiceBay's admin: without an inbox the apply must not
        # depend on the one laib knows (the rehearsal's paperless step, train 28)
        fake = Fake()
        r = self.run_setup(fake, {"PAPERLESS_MAIL_HOST": ""}, password="")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(fake.calls, [])
        env = {"PATH": os.environ["PATH"], "PAPERLESS_API": fake.url, "ADMIN_USER": "admin", "LAIB_DATA_DIR": self.tmp,
               "LAIB_SECRET_PAPERLESS_ADMIN_PASSWORD": "wrong"}
        r = subprocess.run([sys.executable, str(SCRIPT), "setup"], env=env, capture_output=True, text=True, timeout=60)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(self.status()["findings"], [])

    def test_a_refused_admin_is_a_finding_not_a_red_apply(self):
        fake = Fake()
        env = {"PATH": os.environ["PATH"], "PAPERLESS_API": fake.url, "ADMIN_USER": "admin", "LAIB_DATA_DIR": self.tmp,
               "LAIB_SECRET_PAPERLESS_ADMIN_PASSWORD": "wrong", "LAIB_SECRET_PAPERLESS_MAIL_PASSWORD": "mailpw", **MAIL}
        r = subprocess.run([sys.executable, str(SCRIPT), "setup"], env=env, capture_output=True, text=True, timeout=60)
        self.assertEqual(r.returncode, 0, r.stderr)   # Paperless runs; only the inbox waits
        st = self.status()
        self.assertEqual(st["findings"][0]["severity"], "rot")
        self.assertIn("lehnt den Admin admin ab (401)", st["findings"][0]["detail"])
        self.assertIn("laib secret set PAPERLESS_ADMIN_PASSWORD", st["findings"][0]["fix"])
        self.assertNotIn("wrong", r.stdout + r.stderr + json.dumps(st))
        # the check inside the container says the same, not "antwortet nicht"
        out = os.path.join(self.tmp, "st.json")
        env = {"PATH": os.environ["PATH"], "PAPERLESS_API": fake.url, "PAPERLESS_ADMIN_USER": "admin", "PAPERLESS_ADMIN_PASSWORD": "wrong",
               "LAIB_STATUS_FILE": out, **MAIL}
        r = subprocess.run([sys.executable, "-", "check"], input=SCRIPT.read_text(), env=env, capture_output=True, text=True, timeout=60)
        self.assertEqual(r.returncode, 0, r.stderr)
        with open(out) as f:
            self.assertIn("lehnt den Admin admin ab", json.load(f)["findings"][0]["detail"])

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

    def run_check(self, fake, values):
        out = os.path.join(self.tmp, "st.json")
        env = {"PATH": os.environ["PATH"], "PAPERLESS_API": fake.url, "PAPERLESS_ADMIN_USER": "admin", "PAPERLESS_ADMIN_PASSWORD": "adminpw",
               "LAIB_STATUS_FILE": out, **values}
        r = subprocess.run([sys.executable, "-", "check"], input=SCRIPT.read_text(), env=env, capture_output=True, text=True, timeout=60)
        self.assertEqual(r.returncode, 0, r.stderr)
        with open(out) as f:
            return json.load(f)

    def test_a_failed_consume_is_a_yellow_finding(self):
        # 2026-10-22: a mail was fetched, its attachment not filed (PermissionError in the scratch
        # dir); the inbox check stayed green. A failed consume Paperless still lists is yellow.
        accounts = [{"id": 1, "name": "x", "imap_server": "imap.strato.de", "username": "webmaster@example.org", "imap_port": 993,
                     "imap_security": 2, "character_set": "UTF-8", "account_type": 1, "is_token": False}]
        rules = [{"id": 1, "account": 1, "filter_to": "dokumente@example.org", "enabled": True, "name": "r"}]
        failed = {"id": 9, "task_name": "consume_file", "task_file_name": "Rechnung_1117583.pdf", "status": "FAILURE",
                  "result": "[Errno 13] Permission denied: '/tmp/paperless/paperless-mail-43qxltum/Rechnung_1117583.pdf'",
                  "acknowledged": False, "date_created": "2026-10-22T14:50:30Z"}
        fine = [{**failed, "id": 10, "status": "SUCCESS", "task_file_name": "ok.pdf"},
                {**failed, "id": 11, "acknowledged": True, "task_file_name": "dismissed.pdf"},
                {**failed, "id": 12, "task_name": "train_classifier", "task_file_name": None}]
        st = self.run_check(Fake(accounts=accounts, rules=rules, tasks=[failed] + fine), MAIL)
        self.assertEqual([f["severity"] for f in st["findings"]], ["gelb"])
        self.assertIn("Rechnung_1117583.pdf", st["findings"][0]["detail"])
        self.assertIn("Permission denied", st["findings"][0]["detail"])
        self.assertNotIn("ok.pdf", json.dumps(st))
        self.assertNotIn("dismissed.pdf", json.dumps(st))
        # and the probe can be green: the same box without the failure
        self.assertEqual(self.run_check(Fake(accounts=accounts, rules=rules, tasks=fine), MAIL)["findings"], [])
        # a failed consume from the consume folder counts too, also without a mail inbox
        st = self.run_check(Fake(tasks=[failed]), {"PAPERLESS_MAIL_HOST": ""})
        self.assertEqual([f["severity"] for f in st["findings"]], ["gelb"])

    def test_setup_fetches_once_through_paperless_worker(self):
        # a fetch run by hand as root (python3 manage.py mail_fetcher under podman exec) writes the
        # attachment into a 0700 root dir the worker (uid 1000) cannot read: setup asks Paperless'
        # API to fetch now, so the worker fetches and nobody has a reason to
        fake = Fake()
        r = self.run_setup(fake, MAIL)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(fake.processed, [1])

    def test_laibs_exec_never_runs_as_root(self):
        unit = (SCRIPT.parent / "paperless-check.service").read_text()
        execs = [l for l in unit.splitlines() if l.startswith("ExecStart=") and "podman exec" in l]
        self.assertTrue(execs)
        for l in execs:
            self.assertIn(" --user paperless ", l)


if __name__ == "__main__":
    unittest.main()
