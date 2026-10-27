"""#785: immich-check.py writes Immich's detail line for the Box page: how many
photos and videos, how much space, how many accounts, from
/api/server/statistics as the admin, the session logged out again. A fake
Immich (the routes and shapes of Immich 1.13x/2.x) stands in; a refused login
or a silent Immich gives "nicht gemessen" and why, never a number."""
import json, os, subprocess, sys, tempfile, threading, unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent.parent / "immich-check.py"
PW = "-immich-pw-Rk"


class Fake:
    def __init__(self, refuse=False, stats=None):
        self.refuse, self.logged_out = refuse, False
        self.stats = stats or {"photos": 18300, "videos": 1200, "usage": 412 * 10**9,
                               "usageByUser": [{"userName": n, "photos": 1, "videos": 0, "usage": 1} for n in ("a", "b", "c")]}
        outer = self

        class H(BaseHTTPRequestHandler):
            def log_message(self, *a): pass

            def reply(self, code, body=None):
                b = json.dumps(body).encode() if body is not None else b""
                self.send_response(code); self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(b))); self.end_headers(); self.wfile.write(b)

            def authed(self):
                return self.headers.get("Authorization") == "Bearer tok-9"

            def do_POST(self):
                body = json.loads(self.rfile.read(int(self.headers.get("Content-Length") or 0)) or b"{}")
                if self.path == "/api/auth/login":
                    if outer.refuse or body != {"email": "admin@example.org", "password": PW}:
                        return self.reply(401, {"message": "Incorrect email or password"})
                    return self.reply(201, {"accessToken": "tok-9", "isAdmin": True})
                if self.path == "/api/auth/logout" and self.authed():
                    outer.logged_out = True
                    return self.reply(200, {"successful": True})
                self.reply(404)

            def do_GET(self):
                if not self.authed():
                    return self.reply(401)
                if self.path == "/api/server/statistics":
                    return self.reply(200, outer.stats)
                self.reply(404)

        self.srv = ThreadingHTTPServer(("127.0.0.1", 0), H)
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()
        self.url = f"http://127.0.0.1:{self.srv.server_address[1]}"


def run(url, email=""):
    d = tempfile.mkdtemp()
    env = {"PATH": os.environ["PATH"], "IMMICH_URL": url, "ADMIN_EMAIL": email, "DOMAIN": "example.org",
           "IMMICH_PASSWORD": PW, "STATUS_DIR": d}
    r = subprocess.run([sys.executable, "-"], input=SCRIPT.read_text(), text=True, capture_output=True, env=env, timeout=60)
    with open(os.path.join(d, "laib-status.json")) as f:
        return r, json.load(f)


class Check(unittest.TestCase):
    def test_statistics(self):
        im = Fake()
        r, st = run(im.url)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(st["detail"], "18.300 Fotos, 1.200 Videos · 412 GB · 3 Konten")
        self.assertEqual(st["findings"], [])
        self.assertRegex(st["at"], r"^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$")
        self.assertTrue(im.logged_out, "the check's session is logged out again")
        self.assertNotIn(PW, r.stdout + r.stderr + json.dumps(st))

    def test_small_library_in_mb(self):
        im = Fake(stats={"photos": 3, "videos": 0, "usage": 5 * 10**6, "usageByUser": [{"userName": "a"}]})
        _, st = run(im.url)
        self.assertEqual(st["detail"], "3 Fotos, 0 Videos · 5 MB · 1 Konto")

    def test_refused_login_is_not_measured(self):
        _, st = run(Fake(refuse=True).url)
        self.assertTrue(st["detail"].startswith("nicht gemessen: Immich lässt den Admin admin@example.org nicht herein"), st["detail"])
        self.assertNotIn("Fotos", st["detail"])
        self.assertTrue(st["findings"][0]["unmeasured"])

    def test_down_is_not_measured(self):
        _, st = run("http://127.0.0.1:9")
        self.assertTrue(st["detail"].startswith("nicht gemessen: Immich antwortet nicht"), st["detail"])
        self.assertTrue(st["findings"][0]["unmeasured"])


if __name__ == "__main__":
    unittest.main()
