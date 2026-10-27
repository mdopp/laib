"""#785: vaultwarden-check.py writes Vaultwarden's detail line for the Box
page: how many accounts, how many with a second factor, from the admin API
(POST /admin with the admin token, then GET /admin/users). A fake Vaultwarden
(the routes and shapes of src/api/admin.rs) stands in; a refused token or a
silent Vaultwarden gives "nicht gemessen" and why, never a number."""
import json, os, subprocess, sys, tempfile, threading, unittest, urllib.parse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent.parent / "vaultwarden-check.py"
TOKEN = "-admin-token-Vw"


class Fake:
    def __init__(self, users=None, refuse=False, redirect=False):
        self.users = users if users is not None else [
            {"email": "a@x", "userEnabled": True, "twoFactorEnabled": True},
            {"email": "b@x", "userEnabled": True, "twoFactorEnabled": False},
            {"email": "c@x", "userEnabled": True, "twoFactorEnabled": True},
            {"email": "d@x", "userEnabled": False, "twoFactorEnabled": False},
        ]
        self.refuse, self.redirect = refuse, redirect
        outer = self

        class H(BaseHTTPRequestHandler):
            def log_message(self, *a): pass

            def reply(self, code, body=None, cookie=None):
                b = json.dumps(body).encode() if body is not None else b"<html></html>"
                self.send_response(code); self.send_header("Content-Type", "application/json" if body is not None else "text/html")
                if cookie:
                    self.send_header("Set-Cookie", cookie)
                self.send_header("Content-Length", str(len(b))); self.end_headers(); self.wfile.write(b)

            def do_POST(self):
                form = dict(urllib.parse.parse_qsl(self.rfile.read(int(self.headers.get("Content-Length") or 0)).decode()))
                if self.path == "/admin":
                    if outer.refuse or form.get("token") != TOKEN:
                        return self.reply(401)
                    if outer.redirect:  # Vaultwarden's own answer: 303 to the admin page, the cookie on it
                        self.send_response(303); self.send_header("Location", "/admin"); self.send_header("Content-Length", "0")
                        self.send_header("Set-Cookie", "VW_ADMIN=jwt-1; Path=/admin; HttpOnly"); self.end_headers(); return
                    return self.reply(200, cookie="VW_ADMIN=jwt-1; Path=/admin; HttpOnly; SameSite=Strict")
                self.reply(404)

            def do_GET(self):
                if "VW_ADMIN=jwt-1" not in (self.headers.get("Cookie") or ""):
                    return self.reply(401)
                if self.path == "/admin/users":
                    return self.reply(200, outer.users)
                self.reply(404)

        self.srv = ThreadingHTTPServer(("127.0.0.1", 0), H)
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()
        self.url = f"http://127.0.0.1:{self.srv.server_address[1]}"


def run(url):
    d = tempfile.mkdtemp()
    env = {"PATH": os.environ["PATH"], "VAULTWARDEN_URL": url, "ADMIN_TOKEN": TOKEN, "STATUS_DIR": d}
    r = subprocess.run([sys.executable, "-"], input=SCRIPT.read_text(), text=True, capture_output=True, env=env, timeout=60)
    with open(os.path.join(d, "laib-status.json")) as f:
        return r, json.load(f)


class Check(unittest.TestCase):
    def test_accounts_and_second_factor(self):
        r, st = run(Fake().url)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(st["detail"], "3 Konten, 2 mit zweitem Faktor · 1 gesperrt")
        self.assertEqual(st["findings"], [])
        self.assertNotIn(TOKEN, r.stdout + r.stderr + json.dumps(st))

    def test_login_redirect(self):
        _, st = run(Fake(redirect=True).url)
        self.assertEqual(st["detail"], "3 Konten, 2 mit zweitem Faktor · 1 gesperrt")

    def test_one_account(self):
        _, st = run(Fake(users=[{"email": "a@x", "userEnabled": True, "twoFactorEnabled": False}]).url)
        self.assertEqual(st["detail"], "1 Konto, 0 mit zweitem Faktor")

    def test_refused_token_is_not_measured(self):
        _, st = run(Fake(refuse=True).url)
        self.assertTrue(st["detail"].startswith("nicht gemessen: Vaultwarden nimmt das Admin-Token nicht an"), st["detail"])
        self.assertNotIn("Konten", st["detail"])
        self.assertTrue(st["findings"][0]["unmeasured"])

    def test_down_is_not_measured(self):
        _, st = run("http://127.0.0.1:9")
        self.assertTrue(st["detail"].startswith("nicht gemessen: Vaultwarden antwortet nicht"), st["detail"])


if __name__ == "__main__":
    unittest.main()
