"""#254: immich's post-deploy re-sets Immich's OAuth settings from Laib's
secret on every apply, against a fake Immich API.

ServiceBay PUT /api/system-config with an admin login on every deploy and
repaired a secret that drifted in the database (#1556 there). Here the hook
needs the admin's password once: it mints an API key limited to the system
config and hands it back (`laib-secret:`), so a later password change in the
app does not lock it out. A secret left in Immich's database by ServiceBay
is simply overwritten: no carry-over of the old OIDC secret."""
import json, os, subprocess, threading, unittest
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

HOOK = Path(__file__).resolve().parent.parent / "post-deploy"
OIDC = "laib-oidc-Zt7"
PW = "-admin-pw-4k"
IMMICH = {}


def fresh():
    IMMICH.clear()
    IMMICH.update(admin=None, keys={}, puts=0, config={"oauth": {"enabled": False, "issuerUrl": "", "clientId": "", "clientSecret": "",
                  "timeout": 30000, "buttonText": "Login with OAuth", "autoRegister": True, "autoLaunch": False,
                  "tokenEndpointAuthMethod": "client_secret_post", "profileSigningAlgorithm": "none", "signingAlgorithm": "RS256",
                  "scope": "openid email profile", "mobileOverrideEnabled": False, "mobileRedirectUri": ""},
                  "storageTemplate": {"enabled": True, "template": "{{y}}/{{MM}}"}})


class FakeImmich(BaseHTTPRequestHandler):
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

    def who(self):
        """the permissions of the caller: an admin session has all, a key its own"""
        if self.headers.get("x-api-key") in IMMICH["keys"]:
            return IMMICH["keys"][self.headers["x-api-key"]]
        if self.headers.get("Authorization") == "Bearer session-1":
            return ["all"]
        return None

    def do_GET(self):
        if self.path == "/api/server/ping":
            return self.reply(200, {"res": "pong"})
        if self.path == "/api/server/config":
            return self.reply(200, {"isInitialized": IMMICH["admin"] is not None, "isOnboarded": False})
        if self.path == "/api/system-config":
            p = self.who()
            if p is None or not ({"all", "systemConfig.read"} & set(p)):
                return self.reply(401, {"message": "Invalid API key"})
            return self.reply(200, IMMICH["config"])
        self.reply(404)

    def do_POST(self):
        b = self.body()
        if self.path == "/api/auth/admin-sign-up":
            if IMMICH["admin"]:
                return self.reply(400, {"message": "The server already has an admin"})
            IMMICH["admin"] = (b["email"], b["password"])
            return self.reply(201, {"id": "u1", "email": b["email"]})
        if self.path == "/api/auth/login":
            if IMMICH["admin"] != (b.get("email"), b.get("password")):
                return self.reply(401, {"message": "Incorrect email or password"})
            return self.reply(201, {"accessToken": "session-1", "userEmail": b["email"], "isAdmin": True})
        if self.path == "/api/api-keys":
            if self.who() != ["all"]:
                return self.reply(401, {})
            secret = f"key-{len(IMMICH['keys']) + 1}"
            IMMICH["keys"][secret] = b["permissions"]
            return self.reply(201, {"secret": secret, "id": secret, "name": b.get("name"), "permissions": b["permissions"]})
        self.reply(404)

    def do_PUT(self):
        b = self.body()
        if self.path == "/api/system-config":
            p = self.who()
            if p is None or not ({"all", "systemConfig.update"} & set(p)):
                return self.reply(401, {"message": "Missing required permission: systemConfig.update"})
            IMMICH["config"], IMMICH["puts"] = b, IMMICH["puts"] + 1
            return self.reply(200, b)
        self.reply(404)


class PostDeploy(unittest.TestCase):
    def setUp(self):
        fresh()
        self.srv = HTTPServer(("127.0.0.1", 0), FakeImmich)
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()
        self.addCleanup(self.srv.shutdown)

    def run_hook(self, key="", **extra):
        env = dict(os.environ, IMMICH_URL=f"http://127.0.0.1:{self.srv.server_port}", LAIB_DOMAIN="vm.test", OIDC_SECRET=OIDC,
                   LAIB_SECRET_IMMICH_ADMIN_PASSWORD=PW, LAIB_SECRET_IMMICH_API_KEY=key, **extra)
        return subprocess.run([str(HOOK)], env=env, capture_output=True, text=True, timeout=60)

    def oauth(self):
        return IMMICH["config"]["oauth"]

    def test_a_fresh_immich_gets_an_admin_a_key_and_the_family_login(self):
        p = self.run_hook(ADMIN_EMAIL="admin@vm.test")
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertEqual(IMMICH["admin"], ("admin@vm.test", PW))
        self.assertIn("laib-secret: IMMICH_API_KEY=key-1", p.stdout.splitlines())
        self.assertEqual(sorted(IMMICH["keys"]["key-1"]), ["systemConfig.read", "systemConfig.update"], "the key may do more than the system config")
        o = self.oauth()
        self.assertTrue(o["enabled"])
        self.assertEqual((o["issuerUrl"], o["clientId"], o["clientSecret"]), ("https://auth.vm.test", "immich", OIDC))
        self.assertEqual(o["tokenEndpointAuthMethod"], "client_secret_basic", "Laib registers the client with client_secret_basic (#251)")
        self.assertTrue(o["autoRegister"])
        self.assertFalse(o["autoLaunch"], "Immich's own login stays visible as the fallback")
        self.assertEqual(o["timeout"], 30000, "a field the hook does not own was not kept")
        self.assertEqual(IMMICH["config"]["storageTemplate"]["template"], "{{y}}/{{MM}}")
        self.assertNotIn(OIDC, p.stdout + p.stderr, "the OIDC secret is in the hook's output, which goes to the journal")
        self.assertNotIn(PW, p.stdout + p.stderr)

    def test_a_migrated_immich_gets_laibs_secret_and_a_second_run_changes_nothing(self):
        IMMICH["admin"] = ("anna@example.org", PW)                 # ServiceBay's admin, its password carried
        IMMICH["config"]["oauth"].update(enabled=True, issuerUrl="https://auth.vm.test", clientId="immich", clientSecret="servicebay-old")
        p = self.run_hook(ADMIN_EMAIL="anna@example.org")
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertEqual(self.oauth()["clientSecret"], OIDC, "the secret ServiceBay left in Immich's database is still there")
        key = next(l.split("=", 1)[1] for l in p.stdout.splitlines() if l.startswith("laib-secret: IMMICH_API_KEY="))
        IMMICH["admin"] = ("anna@example.org", "changed-in-the-app")  # the admin changes her password in Immich
        puts = IMMICH["puts"]
        p = self.run_hook(key=key, ADMIN_EMAIL="anna@example.org")
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertEqual(IMMICH["puts"], puts, "an unchanged config was written again")
        self.assertNotIn("laib-secret:", p.stdout, "a working key was replaced")
        IMMICH["config"]["oauth"]["clientSecret"] = "drifted"          # a restore of an older database
        p = self.run_hook(key=key, ADMIN_EMAIL="anna@example.org")
        self.assertEqual((p.returncode, self.oauth()["clientSecret"]), (0, OIDC), p.stdout + p.stderr)

    def test_no_key_and_a_refused_login_is_red_and_says_what_to_set(self):
        IMMICH["admin"] = ("anna@example.org", "something-else")
        p = self.run_hook(ADMIN_EMAIL="anna@example.org")
        self.assertNotEqual(p.returncode, 0, "the family login was not configured, yet the hook is green")
        self.assertIn("IMMICH_ADMIN_PASSWORD", p.stderr)
        self.assertIn("ADMIN_EMAIL", p.stderr)
        self.assertEqual(IMMICH["puts"], 0)


if __name__ == "__main__":
    unittest.main()
