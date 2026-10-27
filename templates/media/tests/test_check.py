"""#785: media-check.py writes Jellyfin's detail line for the Box page: what the
library holds and who has an account, measured through Jellyfin's API as the
admin, the session logged out again. A fake Jellyfin (the routes and shapes of
Jellyfin 10.10/12) stands in; a Jellyfin that refuses or does not answer gives
"nicht gemessen" and why, never a number."""
import json, os, subprocess, sys, tempfile, threading, unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent.parent / "media-check.py"
PW = "-admin-pw-Qx"
LDAP = "Jellyfin.Plugin.LDAP_Auth.LdapAuthenticationProviderPlugin"
DEFAULT = "Jellyfin.Server.Implementations.Users.DefaultAuthenticationProvider"


class Fake:
    def __init__(self, counts=None, users=None, sessions=None, refuse=False):
        self.counts = counts if counts is not None else {"MovieCount": 412, "SeriesCount": 38, "EpisodeCount": 1900, "SongCount": 2737, "AlbumCount": 210}
        self.users = users if users is not None else [
            {"Name": "admin", "Policy": {"IsAdministrator": True, "AuthenticationProviderId": DEFAULT}},
            {"Name": "haus", "Policy": {"IsHidden": True, "AuthenticationProviderId": DEFAULT}},
            {"Name": "homeassistant", "Policy": {"AuthenticationProviderId": DEFAULT}},
        ] + [{"Name": n, "Policy": {"AuthenticationProviderId": LDAP}} for n in ("lea", "max", "mia", "tom")]
        self.sessions = sessions or []
        self.refuse, self.seen, self.logged_out = refuse, [], False
        outer = self

        class H(BaseHTTPRequestHandler):
            def log_message(self, *a): pass

            def reply(self, code, body=None):
                b = json.dumps(body).encode() if body is not None else b""
                self.send_response(code); self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(b))); self.end_headers(); self.wfile.write(b)

            def authed(self):
                return 'Token="tok-1"' in (self.headers.get("Authorization") or "")

            def do_POST(self):
                outer.seen.append(("POST", self.path, self.headers.get("Authorization")))
                body = json.loads(self.rfile.read(int(self.headers.get("Content-Length") or 0)) or b"{}")
                if self.path == "/Users/AuthenticateByName":
                    if outer.refuse or body != {"Username": "admin", "Pw": PW} or 'Client="laib"' not in (self.headers.get("Authorization") or ""):
                        return self.reply(401)
                    return self.reply(200, {"AccessToken": "tok-1", "User": {"Name": "admin"}})
                if self.path == "/Sessions/Logout" and self.authed():
                    outer.logged_out = True
                    return self.reply(204)
                self.reply(404)

            def do_GET(self):
                outer.seen.append(("GET", self.path, self.headers.get("Authorization")))
                if not self.authed():
                    return self.reply(401)
                if self.path == "/Items/Counts":
                    return self.reply(200, outer.counts)
                if self.path == "/Users":
                    return self.reply(200, outer.users)
                if self.path.startswith("/Sessions"):
                    return self.reply(200, outer.sessions)
                self.reply(404)

        self.srv = ThreadingHTTPServer(("127.0.0.1", 0), H)
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()
        self.url = f"http://127.0.0.1:{self.srv.server_address[1]}"


def run(url, pw=PW):
    d = tempfile.mkdtemp()
    env = {"PATH": os.environ["PATH"], "JELLYFIN_URL": url, "JELLYFIN_ADMIN": "admin", "JELLYFIN_PASSWORD": pw, "STATUS_DIR": d}
    r = subprocess.run([sys.executable, "-"], input=SCRIPT.read_text(), text=True, capture_output=True, env=env, timeout=60)
    with open(os.path.join(d, "laib-status.json")) as f:
        return r, json.load(f)


class Check(unittest.TestCase):
    def test_library_and_family_accounts(self):
        j = Fake()
        r, st = run(j.url)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(st["detail"], "412 Filme, 38 Serien, 2.737 Lieder · 4 Konten der Familie")
        self.assertRegex(st["at"], r"^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$")
        self.assertEqual(st["findings"], [])
        self.assertTrue(j.logged_out, "the check's session is logged out again")
        self.assertNotIn(PW, r.stdout + r.stderr + json.dumps(st))

    def test_what_plays_now(self):
        j = Fake(sessions=[{"UserName": "lea", "NowPlayingItem": {"Name": "x"}}, {"UserName": "max"}])
        _, st = run(j.url)
        self.assertEqual(st["detail"], "412 Filme, 38 Serien, 2.737 Lieder · 4 Konten der Familie · läuft gerade auf 1 Gerät")

    def test_refused_login_is_not_measured(self):
        j = Fake(refuse=True)
        r, st = run(j.url)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertTrue(st["detail"].startswith("nicht gemessen: Jellyfin lässt den Admin nicht herein"), st["detail"])
        self.assertNotRegex(st["detail"], r"\d+ Filme")
        self.assertTrue(st["findings"][0]["unmeasured"])

    def test_jellyfin_down_is_not_measured(self):
        _, st = run("http://127.0.0.1:9")
        self.assertTrue(st["detail"].startswith("nicht gemessen: Jellyfin antwortet nicht"), st["detail"])
        self.assertTrue(st["findings"][0]["unmeasured"])


if __name__ == "__main__":
    unittest.main()
