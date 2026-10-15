"""#209: auth's post-deploy sets LLDAP passwords through `laib-exec:` lines
that run /app/lldap_set_password. Laib generates secrets as base64url, so one
in 64 starts with "-", and `--password -6…` is read by lldap's parser as an
unknown option: the set fails and the account stays without a password. The
hook runs here against a fake LLDAP on 127.0.0.1:17170 (its GraphQL API)."""
import json, os, socket, subprocess, sys, threading, unittest
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

HOOK = Path(__file__).resolve().parent.parent / "post-deploy"
STATE = {"users": [], "groups": []}


class FakeLLDAP(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers["Content-Length"])) or b"{}")
        if self.path == "/auth/simple/login":
            out = {"token": "t"}
        else:
            q = body["query"]
            if q.startswith("{ groups"):
                out = {"data": {"groups": [{"id": i, "displayName": g} for i, g in enumerate(STATE["groups"])]}}
            elif "createGroup" in q:
                STATE["groups"].append(body["variables"]["n"])
                out = {"data": {"createGroup": {"id": len(STATE["groups"]) - 1}}}
            elif q.startswith("{ users"):
                out = {"data": {"users": [{"id": u} for u in STATE["users"]]}}
            elif "createUser" in q:
                STATE["users"].append(body["variables"]["u"]["id"])
                out = {"data": {"createUser": {"id": body["variables"]["u"]["id"]}}}
            elif "user(userId" in q:
                out = {"data": {"user": {"groups": []}}}
            else:
                out = {"data": {"addUserToGroup": {"ok": True}}}
        data = json.dumps(out).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)


SECRETS = {"LLDAP_ADMIN_PASSWORD": "-adm1n_x", "OPERATOR_PASSWORD": "-6op_pw", "RADICALE_LDAP_PASSWORD": "--rad-pw"}


class PostDeploy(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        try:
            cls.srv = HTTPServer(("127.0.0.1", 17170), FakeLLDAP)
        except OSError:
            raise unittest.SkipTest("127.0.0.1:17170 is taken")
        threading.Thread(target=cls.srv.serve_forever, daemon=True).start()

    @classmethod
    def tearDownClass(cls):
        cls.srv.shutdown()

    def run_hook(self):
        env = dict(os.environ, LAIB_DOMAIN="vm.test", OPERATOR_USER="core", OPERATOR_EMAIL="core@vm.test",
                   **{"LAIB_SECRET_" + k: v for k, v in SECRETS.items()})
        p = subprocess.run([sys.executable, str(HOOK)], env=env, capture_output=True, text=True, timeout=60)
        self.assertEqual(p.returncode, 0, p.stderr)
        return [l[len("laib-exec: "):] for l in p.stdout.splitlines() if l.startswith("laib-exec: ")]

    def assert_no_value_reads_as_option(self, line):
        # laib splits the line on whitespace (internal/apply/apply.go); clap reads a
        # word that starts with "-" as an option unless it is "--name=value"
        for word in line.split()[2:]:
            if word.startswith("-"):
                self.assertRegex(word, r"^--[a-z][a-z-]*(=.*)?$", f"{word!r} would be read as an option: {line}")
        for s in SECRETS.values():
            self.assertNotIn(" " + s, " " + line, f"the password {s!r} stands alone on argv: {line}")

    def test_passwords_starting_with_a_dash_stay_values(self):
        STATE["users"], STATE["groups"] = [], []
        lines = self.run_hook()
        self.assertEqual(len(lines), 2, lines)   # the operator's and radicale's password
        for l in lines:
            self.assert_no_value_reads_as_option(l)

    def test_radicales_password_is_set_again_when_the_account_exists(self):
        # a set that failed once (the account exists, the password does not) is not final:
        # radicale's password is a service secret, so every run sets it again
        STATE["users"], STATE["groups"] = ["core", "radicale"], ["admins", "family", "lldap_strict_readonly"]
        lines = self.run_hook()
        self.assertTrue(any("--username=radicale" in l or "--username radicale" in l for l in lines), lines)
        self.assertFalse(any("core" in l.split() or "--username=core" in l for l in lines),
                         f"the operator's own password must not be reset once the account exists: {lines}")


if __name__ == "__main__":
    unittest.main()
