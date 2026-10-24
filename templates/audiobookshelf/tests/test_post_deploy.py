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
    ABS.update(init=False, libraries=[], patches=0, client=None, server={"storeMetadataWithItem": False}, users=[], keys=[], n=0, settings={
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

    def key(self):
        return next((k for k in ABS["keys"] if self.headers.get("Authorization") == "Bearer " + k["apiKey"]), None)

    def do_DELETE(self):
        if not self.authed() or not self.path.startswith("/api/api-keys/"):
            return self.reply(404)
        ABS["keys"] = [k for k in ABS["keys"] if k["id"] != self.path.rsplit("/", 1)[1]]
        self.reply(200, {})

    def do_GET(self):
        if self.path == "/status":
            return self.reply(200, {"isInit": ABS["init"]})
        if self.path == "/api/me" and self.key():
            u = next(u for u in ABS["users"] if u["id"] == self.key()["userId"])
            return self.reply(200, {"id": u["id"], "username": u["username"], "type": u["type"]})
        if not self.authed():
            return self.reply(401)
        if self.path == "/api/libraries":
            return self.reply(200, {"libraries": ABS["libraries"]})
        if self.path == "/api/users":
            return self.reply(200, {"users": ABS["users"]})
        if self.path == "/api/api-keys":
            return self.reply(200, {"apiKeys": [{k: v for k, v in x.items() if k != "apiKey"} for x in ABS["keys"]]})
        if self.path == "/api/auth-settings":
            return self.reply(200, ABS["settings"])
        self.reply(404)

    def do_POST(self):
        b = self.body()
        if self.path == "/init":
            ABS["init"] = True
            self.send_response(200)                      # real ABS answers "OK", text, not JSON (vm-install run 38897720430)
            self.send_header("Content-Type", "text/plain")
            self.send_header("Content-Length", "2")
            self.end_headers()
            return self.wfile.write(b"OK")
        if self.path == "/login":
            if (b.get("username"), b.get("password")) != ("root", ROOT):
                return self.reply(401)
            return self.reply(200, {"user": {"token": "legacy-1", "accessToken": "access-1"}, "serverSettings": dict(ABS["server"])})
        if not self.authed():
            return self.reply(401)
        if self.path == "/api/libraries":
            ABS["libraries"].append({"id": f"lib-{len(ABS['libraries'])}", "name": b["name"], "mediaType": b["mediaType"],
                                     "provider": b.get("provider", "google"), "folders": [{"id": "fo-x", "fullPath": f["fullPath"]} for f in b["folders"]]})
            return self.reply(200, ABS["libraries"][-1])
        if self.path == "/api/users":
            if any(u["username"] == b["username"] for u in ABS["users"]):
                return self.reply(400)
            ABS["users"].append({"id": f"usr-{b['username']}", "username": b["username"], "type": b.get("type", "user"), "permissions": b.get("permissions", {})})
            return self.reply(200, {"user": ABS["users"][-1]})
        if self.path == "/api/api-keys":
            ABS["n"] += 1
            k = {"id": f"key-{ABS['n']}", "name": b["name"], "userId": b["userId"], "isActive": b.get("isActive"), "apiKey": f"abs-api-key-{ABS['n']}"}
            ABS["keys"].append(k)
            return self.reply(200, {"apiKey": k})
        self.reply(404)

    def do_PATCH(self):
        b = self.body()
        if self.path == "/api/settings" and self.authed():
            ABS["server"].update(b)
            return self.reply(200, {"success": True, "serverSettings": ABS["server"]})
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

    def run_hook(self, ma_key=""):
        env = dict(os.environ, ABS_URL=f"http://127.0.0.1:{self.srv.server_port}", LAIB_DOMAIN="vm.test",
                   OIDC_SECRET=OIDC, LAIB_SECRET_ABS_ROOT_PASSWORD=ROOT, LAIB_SECRET_ABS_MA_TOKEN=ma_key)
        p = subprocess.run([sys.executable, str(HOOK)], env=env, capture_output=True, text=True, timeout=60)
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.handed = dict(l[len("laib-secret: "):].split("=", 1) for l in p.stdout.splitlines() if l.startswith("laib-secret: "))
        rest = "\n".join(l for l in (p.stdout + p.stderr).splitlines() if not l.startswith("laib-secret: "))
        for v in (OIDC, ROOT, *self.handed.values()):
            self.assertNotIn(v, rest, "a secret is in the hook's output, which goes to the journal")
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
        libs = {l["name"]: l for l in ABS["libraries"]}
        self.assertEqual(sorted(libs), ["Hörbücher & Hörspiele", "Podcasts"])
        self.assertEqual((libs["Hörbücher & Hörspiele"]["mediaType"], libs["Hörbücher & Hörspiele"]["folders"][0]["fullPath"],
                          libs["Hörbücher & Hörspiele"]["provider"]), ("book", "/audiobooks", "audible.de"))
        self.assertEqual((libs["Podcasts"]["mediaType"], libs["Podcasts"]["folders"][0]["fullPath"]), ("podcast", "/podcasts"))
        # #646: metadata.json and the cover go next to the files, the audio files stay as they are
        self.assertTrue(ABS["server"]["storeMetadataWithItem"])

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


    def test_music_assistant_gets_an_account_and_a_key_of_its_own(self):
        # #646: Music Assistant plays the books and podcasts on the speakers. Its own ABS user, no admin,
        # and an API key handed back to Laib (music-assistant declares it: the same apply connects MA)
        fresh()
        self.run_hook()
        (u,) = [u for u in ABS["users"] if u["username"] == "musicassistant"]
        self.assertEqual(u["type"], "user")
        self.assertFalse(u["permissions"].get("update") or u["permissions"].get("delete") or u["permissions"].get("upload"))
        key = self.handed.get("ABS_MA_TOKEN")
        self.assertTrue(key, "no key for Music Assistant")
        self.assertEqual([(k["userId"], k["isActive"]) for k in ABS["keys"]], [(u["id"], True)])
        self.run_hook(ma_key=key)
        self.assertEqual((self.handed, len(ABS["keys"])), ({}, 1), "a working key was replaced")
        self.run_hook(ma_key="a-key-abs-does-not-know")
        self.assertIn("ABS_MA_TOKEN", self.handed)
        self.assertEqual([k["apiKey"] for k in ABS["keys"]], [self.handed["ABS_MA_TOKEN"]], "the old key of that name still works")
        self.assertEqual(len([u for u in ABS["users"] if u["username"] == "musicassistant"]), 1)

    def test_libraries_the_owner_made_are_kept_by_their_folder(self):
        fresh()
        ABS["init"] = True
        ABS["libraries"] = [{"id": "own", "name": "Meine Bücher", "mediaType": "book", "provider": "google", "folders": [{"fullPath": "/audiobooks"}]}]
        self.run_hook()
        self.assertEqual(sorted(l["name"] for l in ABS["libraries"]), ["Meine Bücher", "Podcasts"])


if __name__ == "__main__":
    unittest.main()
