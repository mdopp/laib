"""#360: auth's person tool (person.py) against a fake LLDAP (its GraphQL
and its login) and a fake Authelia (the reset-password identity start, which
writes the link into notification.txt as the file notifier does, and its
identity_verification table). The tool runs as laib runs it, as a process
with the hook's environment."""
import json, os, sqlite3, subprocess, sys, tempfile, threading, unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

TOOL = Path(__file__).resolve().parent.parent / "person.py"
ADMIN_PW = "lldap-admin-test"


class Fake:
    def __init__(self, data):
        self.data = data
        self.users = {"admin": {"id": "admin", "email": "admin@vm.test", "displayName": "Admin", "groups": ["admins"]},
                      "radicale": {"id": "radicale", "email": "radicale@vm.test", "displayName": "Radicale", "groups": ["lldap_strict_readonly"]}}
        self.groups = {"admins": 1, "family": 2, "lldap_strict_readonly": 3}
        self.resets, self.calls = [], []
        fake = self

        class H(BaseHTTPRequestHandler):
            def log_message(self, *a):
                pass

            def reply(self, obj, code=200):
                b = json.dumps(obj).encode()
                self.send_response(code)
                self.send_header("Content-Type", "application/json")
                self.end_headers()
                self.wfile.write(b)

            def do_POST(self):
                body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
                fake.calls.append((self.path, body, dict(self.headers)))
                if self.path == "/auth/simple/login":
                    return self.reply({"token": "t"}) if body["password"] == ADMIN_PW else self.reply({}, 401)
                if self.path == "/api/graphql":
                    assert self.headers["Authorization"] == "Bearer t"
                    return self.reply({"data": fake.gql(body["query"], body.get("variables", {}))})
                if self.path == "/api/reset-password/identity/start":
                    return self.reply(fake.reset(body["username"], self.headers))
                self.reply({}, 404)

        self.srv = ThreadingHTTPServer(("127.0.0.1", 0), H)
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()
        self.url = f"http://127.0.0.1:{self.srv.server_port}"
        db = sqlite3.connect(data / "db.sqlite3")
        db.execute("CREATE TABLE identity_verification (id INTEGER PRIMARY KEY, jti TEXT, iat DATETIME, exp DATETIME, username TEXT, action TEXT, consumed DATETIME)")
        db.commit()
        db.close()

    def gql(self, q, v):
        if q.startswith("{ users"):
            return {"users": [{"id": u["id"], "email": u["email"], "displayName": u["displayName"],
                               "groups": [{"displayName": g} for g in u["groups"]]} for u in self.users.values()]}
        if q.startswith("{ groups"):
            return {"groups": [{"id": i, "displayName": n} for n, i in self.groups.items()]}
        if "createUser" in q:
            u = v["u"]
            self.users[u["id"]] = {**u, "groups": []}
            return {"createUser": {"id": u["id"]}}
        if "addUserToGroup" in q:
            self.users[v["u"]]["groups"].append(next(n for n, i in self.groups.items() if i == v["g"]))
            return {"addUserToGroup": {"ok": True}}
        raise AssertionError(q)

    def reset(self, user, headers):
        assert headers["X-Forwarded-Host"] == "auth.vm.test" and headers["X-Forwarded-Proto"] == "https"
        self.resets.append(user)
        db = sqlite3.connect(self.data / "db.sqlite3")
        db.execute("INSERT INTO identity_verification (jti, iat, exp, username, action) VALUES (?, ?, ?, ?, 'ResetPassword')",
                   (f"j{len(self.resets)}", "2026-10-18 10:00:00.123456789+00:00", "2026-10-19 10:00:00.123456789+00:00", user))
        db.commit()
        db.close()
        (self.data / "notification.txt").write_text(f"Date: now\nRecipient: {{{user}}}\nBody: Hallo\n\nhttps://auth.vm.test/reset-password/step2?token=tok{len(self.resets)}\n\nRevoke: https://auth.vm.test/revoke/reset-password?id=x\n")
        return {"status": "OK"}

    def use_link(self, user):
        db = sqlite3.connect(self.data / "db.sqlite3")
        db.execute("UPDATE identity_verification SET consumed = '2026-10-18 11:00:00+00:00' WHERE username = ?", (user,))
        db.commit()
        db.close()


class Person(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        d = Path(self.tmp.name)
        self.cfg, self.data = d / "auth" / "authelia-config", d / "auth" / "authelia-data"
        self.cfg.mkdir(parents=True)
        self.data.mkdir(parents=True)
        (self.cfg / "configuration.yml").write_text("notifier:\n  template_path: '/config/templates'\n  filesystem:\n    filename: '/data/notification.txt'\n")
        self.fake = Fake(self.data)
        self.addCleanup(self.fake.srv.server_close)
        self.addCleanup(self.fake.srv.shutdown)
        self.env = dict(os.environ, LAIB_DATA_DIR=str(d), LAIB_DOMAIN="vm.test", LAIB_SECRET_LLDAP_ADMIN_PASSWORD=ADMIN_PW,
                        LAIB_LLDAP_URL=self.fake.url, LAIB_AUTHELIA_URL=self.fake.url)

    def run_tool(self, *args, code=0):
        p = subprocess.run([sys.executable, str(TOOL), *args], env=self.env, capture_output=True, text=True, timeout=60)
        self.assertEqual(p.returncode, code, p.stdout + p.stderr)
        self.assertNotIn(ADMIN_PW, p.stdout + p.stderr)
        return json.loads(p.stdout.strip().splitlines()[-1])

    def invite(self, user="anna", first="Anna", last="Muster", email="anna@vm.test", group="-", resend="0", code=0):
        return self.run_tool("invite", user, first, last, email, group, resend, code=code)

    def test_invite_without_smtp_returns_the_link(self):
        r = self.invite()
        self.assertEqual(r, {"user": "anna", "name": "Anna Muster", "email": "anna@vm.test", "mailed": False,
                             "link": "https://auth.vm.test/reset-password/step2?token=tok1", "expires": "2026-10-19T10:00:00Z"})
        anna = self.fake.users["anna"]
        self.assertEqual((anna["firstName"], anna["lastName"], anna["displayName"], anna["groups"]), ("Anna", "Muster", "Anna Muster", ["family"]))
        self.assertNotIn("password", json.dumps([c[1] for c in self.fake.calls if c[0] == "/api/graphql"]), "no password through Laib")
        self.assertEqual(self.fake.resets, ["anna"])

    def test_invite_with_smtp_is_mailed_and_returns_no_link(self):
        (self.cfg / "configuration.yml").write_text("notifier:\n  disable_startup_check: true\n  template_path: '/config/templates'\n  smtp:\n    address: 'smtp://x:1'\n")
        r = self.invite(group="admins")
        self.assertTrue(r["mailed"])
        self.assertNotIn("link", r)
        self.assertEqual(self.fake.users["anna"]["groups"], ["admins"])

    def test_an_existing_user_or_email_is_refused_with_a_sentence(self):
        self.assertIn("gibt es schon", self.invite(user="radicale", email="r@vm.test", code=3)["error"])
        self.assertIn("gehört schon admin", self.invite(email="ADMIN@vm.test", code=3)["error"])
        self.assertIn("Benutzername", self.invite(user="Anna Muster", code=3)["error"])
        self.assertIn("Mailadresse", self.invite(email="anna", code=3)["error"])
        self.assertIn("Gruppe", self.invite(group="wheel", code=3)["error"])
        self.assertEqual(self.fake.resets, [])
        self.assertNotIn("anna", self.fake.users)

    def test_resend_keeps_the_group_and_sends_a_new_link(self):
        self.invite(group="admins")
        r = self.invite(resend="1")
        self.assertEqual(r["link"], "https://auth.vm.test/reset-password/step2?token=tok2")
        self.assertEqual(self.fake.users["anna"]["groups"], ["admins"])
        self.assertEqual(self.fake.resets, ["anna", "anna"])
        self.assertIn("nicht anna@other.test", self.invite(email="anna@other.test", resend="1", code=3)["error"])

    def test_pending_until_the_link_is_used(self):
        self.assertEqual(self.run_tool("pending"), [])
        self.invite()
        self.invite(user="ben", first="Ben", last="", email="ben@vm.test")
        rows = self.run_tool("pending")
        self.assertEqual([(p["user"], p["name"], p["email"]) for p in rows], [("anna", "Anna Muster", "anna@vm.test"), ("ben", "Ben", "ben@vm.test")])
        self.assertTrue(all(p["invited_at"].endswith("Z") for p in rows))
        self.fake.use_link("anna")
        self.assertEqual([p["user"] for p in self.run_tool("pending")], ["ben"])
        del self.fake.users["ben"]   # removed in LLDAP: no longer waiting
        self.assertEqual(self.run_tool("pending"), [])

    def test_lldap_down_creates_nothing(self):
        self.env["LAIB_SECRET_LLDAP_ADMIN_PASSWORD"] = "wrong"
        self.assertIn("LLDAP", self.invite(code=3)["error"])
        self.assertNotIn("anna", self.fake.users)


if __name__ == "__main__":
    unittest.main()
