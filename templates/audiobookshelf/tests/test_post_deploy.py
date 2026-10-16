"""#255: Audiobookshelf's OIDC settings, set from Laib's secret on every
apply, against a fake ABS that keeps ABS's two habits.

ServiceBay retired ABS because its OIDC secret lives in ABS's own database
and drifted from Authelia's (#1725 there). The root cause has a second half:
ABS builds its OIDC client once and keeps it in memory
(server/auth/OidcAuthStrategy.js getClient), so a secret PATCHed while
openid stays on is stored but not used until the method is switched off and
on (MiscController.updateAuthSettings calls unuse/init only on a change of
authActiveAuthMethods). The fake does the same."""
import json, os, subprocess, sys, threading, unittest
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

HOOK = Path(__file__).resolve().parent.parent / "post-deploy"
OIDC, ROOT = "laib-abs-oidc-7Hq", "-root-pw-Zt"
ABS = {}


def fresh(**over):
    ABS.clear()
    ABS.update(init=False, libraries=[], patches=0, client=None, settings={
        "authActiveAuthMethods": ["local"], "authOpenIDIssuerURL": None, "authOpenIDAuthorizationURL": None,
        "authOpenIDTokenURL": None, "authOpenIDUserInfoURL": None, "authOpenIDJwksURL": None, "authOpenIDLogoutURL": None,
        "authOpenIDClientID": None, "authOpenIDClientSecret": None, "authOpenIDTokenSigningAlgorithm": "RS256",
        "authOpenIDButtonText": "Login with OpenId", "authOpenIDAutoLaunch": False, "authOpenIDAutoRegister": False,
        "authOpenIDMatchExistingBy": None, "authOpenIDMobileRedirectURIs": ["audiobookshelf://oauth"],
        "authOpenIDGroupClaim": "", "authOpenIDAdvancedPermsClaim": "", "authOpenIDSubfolderForRedirectURLs": None,
        "authLoginCustomMessage": None})
    ABS["settings"].update(over)


class FakeABS(BaseHTTPRequestHandler):
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
        if self.path == "/status":
            return self.reply(200, {"isInit": ABS["init"]})
        if not self.authed():
            return self.reply(401)
        if self.path == "/api/libraries":
            return self.reply(200, {"libraries": [{"name": n} for n in ABS["libraries"]]})
        if self.path == "/api/auth-settings":
            return self.reply(200, ABS["settings"])
        self.reply(404)

    def do_POST(self):
        b = self.body()
        if self.path == "/init":
            ABS["init"] = True
            return self.reply(200)
        if self.path == "/login":
            if (b.get("username"), b.get("password")) != ("root", ROOT):
                return self.reply(401)
            return self.reply(200, {"user": {"token": "legacy-1", "accessToken": "access-1"}})
        if self.path == "/api/libraries" and self.authed():
            ABS["libraries"].append(b["name"])
            return self.reply(200, {})
        self.reply(404)

    def do_PATCH(self):
        b = self.body()
        if self.path != "/api/auth-settings" or not self.authed():
            return self.reply(404)
        ABS["patches"] += 1
        s, before = ABS["settings"], list(ABS["settings"]["authActiveAuthMethods"])
        for k in s:
            if k in b:
                s[k] = b[k]
        on = "openid" in s["authActiveAuthMethods"]
        if on and "openid" not in before:
            ABS["client"] = s["authOpenIDClientSecret"]      # init(): a client from the settings
        elif not on:
            ABS["client"] = None                              # unuse(): the cached client is dropped
        self.reply(200, {"updated": True})


class PostDeploy(unittest.TestCase):
    def setUp(self):
        self.srv = HTTPServer(("127.0.0.1", 0), FakeABS)
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()
        self.addCleanup(self.srv.shutdown)

    def run_hook(self):
        env = dict(os.environ, ABS_URL=f"http://127.0.0.1:{self.srv.server_port}", LAIB_DOMAIN="vm.test",
                   OIDC_SECRET=OIDC, LAIB_SECRET_ABS_ROOT_PASSWORD=ROOT)
        p = subprocess.run([sys.executable, str(HOOK)], env=env, capture_output=True, text=True, timeout=60)
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertNotIn(OIDC, p.stdout + p.stderr, "the OIDC secret is in the hook's output, which goes to the journal")
        return p

    def test_a_fresh_abs_gets_the_family_login(self):
        fresh()
        self.run_hook()
        s = ABS["settings"]
        self.assertEqual(sorted(s["authActiveAuthMethods"]), ["local", "openid"], "ABS's own login must stay as the fallback")
        self.assertEqual((s["authOpenIDIssuerURL"], s["authOpenIDClientID"], s["authOpenIDClientSecret"]),
                         ("https://auth.vm.test", "audiobookshelf", OIDC))
        self.assertEqual(s["authOpenIDAuthorizationURL"], "https://auth.vm.test/api/oidc/authorization")
        self.assertEqual(s["authOpenIDTokenURL"], "https://auth.vm.test/api/oidc/token")
        self.assertEqual(s["authOpenIDUserInfoURL"], "https://auth.vm.test/api/oidc/userinfo")
        self.assertEqual(s["authOpenIDJwksURL"], "https://auth.vm.test/jwks.json")
        self.assertTrue(s["authOpenIDAutoRegister"])
        self.assertFalse(s["authOpenIDAutoLaunch"])
        self.assertEqual(s["authOpenIDSubfolderForRedirectURLs"], "", "unset, ABS builds its callback as /undefined/auth/openid/callback")
        self.assertEqual(s["authOpenIDGroupClaim"], "", "a group claim makes ABS refuse every user not in admin/user/guest")
        self.assertEqual(ABS["client"], OIDC)
        self.assertEqual(ABS["libraries"], ["Hörbücher", "Podcasts"])

    def test_a_drifted_secret_reaches_the_client_abs_uses_and_a_second_run_changes_nothing(self):
        fresh()
        self.run_hook()
        ABS["settings"]["authOpenIDClientSecret"] = ABS["client"] = "an-older-one"   # a restore of an older ABS database
        self.run_hook()
        self.assertEqual(ABS["settings"]["authOpenIDClientSecret"], OIDC)
        self.assertEqual(ABS["client"], OIDC, "ABS stored the new secret but keeps logging in with the old one")
        patches = ABS["patches"]
        self.run_hook()
        self.assertEqual(ABS["patches"], patches, "unchanged settings were written again")


if __name__ == "__main__":
    unittest.main()
