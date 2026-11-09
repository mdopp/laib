"""The Bote's Signal profile (owner, 2026-11-09): post-deploy sets name and picture through
signal-cli's daemon once, not again while nothing changed, again after the picture changed, and a
daemon that fails is one line and never a red deploy. The hook runs whole, against a fake daemon."""
import base64, json, os, shutil, subprocess, tempfile, threading, unittest
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

TPL = Path(__file__).resolve().parent.parent
ABOUT = "Eure Box zu Hause · example.org · App: https://github.com/mdopp/homes-android/releases/latest/download/app-release.apk"
CALLS, FAIL, ONCE = [], [], []   # FAIL: every answer; ONCE: the next answers, each once


class FakeDaemon(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers.get("Content-Length") or 0)))
        CALLS.append((self.path, body))
        if ONCE or FAIL:
            code = ONCE.pop(0) if ONCE else FAIL[0]
            self.send_response(code)
            out = b"{}" if code != 200 else json.dumps({"jsonrpc": "2.0", "id": 1, "error": {"code": -1, "message": "+490000 kaputt"}}).encode()
        else:
            self.send_response(200)
            out = json.dumps({"jsonrpc": "2.0", "id": 1, "result": {}}).encode()
        self.send_header("Content-Length", str(len(out)))
        self.end_headers()
        self.wfile.write(out)


class SignalProfile(unittest.TestCase):
    def setUp(self):
        CALLS.clear(), FAIL.clear(), ONCE.clear()
        self.srv = HTTPServer(("127.0.0.1", 0), FakeDaemon)
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()
        self.addCleanup(self.srv.server_close)
        self.addCleanup(self.srv.shutdown)
        d = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, d)
        self.data, self.tpl = Path(d, "data"), Path(d, "tpl")
        shutil.copytree(TPL, self.tpl, ignore=shutil.ignore_patterns("tests", "__pycache__"))

    def deploy(self, **values):
        env = dict(os.environ, LAIB_DATA_DIR=str(self.data), LAIB_RENDER_DIR=str(self.data) + "/none", MODEL_URL="http://127.0.0.1:1/v1",
                   SIGNAL_ACCOUNT="+490000", MODEL_NAME="m", THINKING_MODEL="t", LAIB_SECRET_BOTE_LAIB_TOKEN="x",
                   SIGNAL_RPC_URL=f"http://127.0.0.1:{self.srv.server_port}", SIGNAL_PROFILE_TRIES="2", SIGNAL_PROFILE_WAIT="0", PROFILE_NAME="Homes", PROFILE_AVATAR="homes-avatar.png", PROFILE_ABOUT=ABOUT,
                   HAUSHALT_MCP_URL="", FRAGE_URL="", PAPERLESS_URL="")
        env.update(values)
        p = subprocess.run(["bash", str(self.tpl / "post-deploy")], env=env, capture_output=True, text=True, timeout=60)
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        return p.stdout

    def test_set_once_not_again_unchanged_and_again_after_the_picture_changed(self):
        out = self.deploy()
        self.assertEqual(len(CALLS), 1, out)
        path, body = CALLS[0]
        self.assertEqual(path, "/api/v1/rpc")
        self.assertEqual(body["method"], "updateProfile")
        p = body["params"]
        self.assertEqual((p["account"], p["givenName"], p["familyName"]), ("+490000", "Homes", ""))
        self.assertEqual(base64.b64decode(p["avatar"].split(",", 1)[1]), (TPL / "homes-avatar.png").read_bytes())
        self.assertTrue(p["avatar"].startswith("data:image/png;base64,"))
        self.assertIn("signal-profil: gesetzt", out)

        out = self.deploy()
        self.assertEqual(len(CALLS), 1, "unchanged, yet set again: " + out)
        self.assertNotIn("signal-profil", out)

        (self.tpl / "homes-avatar.png").write_bytes((TPL / "homes-avatar.png").read_bytes() + b"\0")
        self.deploy()
        self.assertEqual(len(CALLS), 2, "the picture changed, the profile did not")

        self.deploy(PROFILE_NAME="Joshua")
        self.assertEqual(len(CALLS), 3, "the name changed, the profile did not")
        self.assertEqual(CALLS[-1][1]["params"]["givenName"], "Joshua")

    def test_the_description_set_once_and_again_when_it_changed(self):
        # owner, 2026-11-09: „eine Beschreibung, die auf dopp.cloud verweist“
        out = self.deploy()
        self.assertEqual(CALLS[0][1]["params"]["about"], ABOUT)
        self.assertIn("Beschreibung", out)
        self.deploy()
        self.assertEqual(len(CALLS), 1, "unchanged, yet set again")
        self.deploy(PROFILE_ABOUT="Eure Box zu Hause · example.org")
        self.assertEqual(len(CALLS), 2, "the description changed, the profile did not")
        self.assertEqual(CALLS[-1][1]["params"]["about"], "Eure Box zu Hause · example.org")
        self.deploy(PROFILE_NAME="", PROFILE_AVATAR="", PROFILE_ABOUT="Nur das")   # the description alone is enough
        self.assertEqual(CALLS[-1][1]["params"], {"account": "+490000", "about": "Nur das"})

    def test_a_long_description_keeps_signals_140_by_whole_parts(self):
        long = "Eure Box zu Hause · " + "d" * 60 + ".example.org · App: https://github.com/mdopp/homes-android/releases/latest/download/app-release.apk"
        self.deploy(PROFILE_ABOUT=long)
        self.assertEqual(CALLS[0][1]["params"]["about"], "Eure Box zu Hause · " + "d" * 60 + ".example.org")
        self.deploy(PROFILE_ABOUT="x" * 200)
        self.assertEqual(CALLS[-1][1]["params"]["about"], "x" * 140)

    def test_the_shipped_description_fits_signal_for_a_usual_domain(self):
        line = next(l for l in (TPL / "values.example.yml").read_text().splitlines() if l.startswith("PROFILE_ABOUT:"))
        about = line.split('"')[1].replace("${DOMAIN}", "dopp.cloud")
        self.assertLessEqual(len(about), 140)
        self.assertIn("dopp.cloud", about)
        self.assertIn("homes-android/releases/latest", about)

    def test_a_failing_daemon_is_one_line_never_red_and_tried_again(self):
        for status in (500, 200):   # an HTTP error, and signal-cli's JSON-RPC error
            FAIL[:] = [status]
            out = self.deploy()
            self.assertIn("signal-profil: nicht gesetzt", out)
            self.assertNotIn("+490000", out, "the error names the number")
        FAIL.clear()
        self.deploy()
        self.assertEqual(len(CALLS), 4, "an HTTP error is tried twice, signal-cli's no once; nothing noted, so the next apply tries again")
        self.srv.shutdown()
        self.srv.server_close()
        self.deploy(PROFILE_NAME="Anders")   # no daemon at all
        self.assertEqual(len(CALLS), 4)

    def test_a_daemon_still_starting_is_waited_for(self):
        ONCE[:] = [500]   # the first answer fails, as right after apply (2026-11-09: ConnectionResetError)
        out = self.deploy()
        self.assertIn("signal-profil: gesetzt", out)
        self.assertEqual(len(CALLS), 2)

    def test_off_without_signal_or_without_both_values(self):
        self.deploy(PROFILE_NAME="", PROFILE_AVATAR="", PROFILE_ABOUT="")
        self.deploy(SIGNAL_ACCOUNT="", DISCORD_ALLOWED_USERS="1", LAIB_SECRET_BOTE_DISCORD_TOKEN="t")
        self.assertEqual(CALLS, [])

    def test_only_the_picture_leaves_the_name(self):
        self.deploy(PROFILE_NAME="")
        self.assertNotIn("givenName", CALLS[0][1]["params"])
        self.assertIn("avatar", CALLS[0][1]["params"])

    def test_the_shipped_picture_is_a_square_png_signal_takes(self):
        b = (TPL / "homes-avatar.png").read_bytes()
        self.assertEqual(b[:8], b"\x89PNG\r\n\x1a\n")
        w, h = int.from_bytes(b[16:20], "big"), int.from_bytes(b[20:24], "big")
        self.assertEqual(w, h)
        self.assertLessEqual(w, 640)
        self.assertIn("PROFILE_AVATAR: homes-avatar.png", (TPL / "values.example.yml").read_text())


if __name__ == "__main__":
    unittest.main()
