"""haus#174: file-share-versioning.py gives every Syncthing folder without
versioning simple versioning for 30 days, and Syncthing's folder defaults
the same, so a folder added later starts with it. A folder whose versioning
someone set stays as set. A second run changes nothing. A fake Syncthing
stands in; the API key goes into the header only, never into the output."""
import copy, json, os, subprocess, sys, tempfile, threading, unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent.parent / "file-share-versioning.py"
KEY = "-st-apikey-Vx"
CONFIG = f"""<configuration version="37">
    <gui enabled="true" tls="false">
        <address>127.0.0.1:8384</address>
        <apikey>{KEY}</apikey>
    </gui>
</configuration>
"""
NONE = {"type": "", "params": {}, "cleanupIntervalS": 3600, "fsPath": "", "fsType": "basic"}
STAGGERED = {"type": "staggered", "params": {"maxAge": "31536000"}, "cleanupIntervalS": 3600, "fsPath": "", "fsType": "basic"}


class Fake:
    def __init__(self):
        outer = self
        self.folders = {"default": {"id": "default", "label": "Default Folder", "versioning": copy.deepcopy(NONE)},
                        "fotos-handy": {"id": "fotos-handy", "label": "Fotos", "versioning": copy.deepcopy(STAGGERED)}}
        self.defaults = {"id": "", "versioning": copy.deepcopy(NONE)}
        self.patches = []

        class H(BaseHTTPRequestHandler):
            def log_message(self, *a): pass

            def reply(self, code, body=None):
                b = json.dumps(body if body is not None else {}).encode()
                self.send_response(code)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(b)))
                self.end_headers()
                self.wfile.write(b)

            def do_GET(self):
                if self.headers.get("X-API-Key") != KEY:
                    return self.reply(403, {"error": "forbidden"})
                if self.path == "/rest/config/folders":
                    return self.reply(200, list(outer.folders.values()))
                if self.path == "/rest/config/defaults/folder":
                    return self.reply(200, outer.defaults)
                self.reply(404)

            def do_PATCH(self):
                if self.headers.get("X-API-Key") != KEY:
                    return self.reply(403, {"error": "forbidden"})
                body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
                outer.patches.append((self.path, body))
                if self.path == "/rest/config/defaults/folder":
                    outer.defaults.update(body)
                elif self.path.startswith("/rest/config/folders/") and self.path.split("/")[-1] in outer.folders:
                    outer.folders[self.path.split("/")[-1]].update(body)
                else:
                    return self.reply(404)
                self.reply(200)

        self.srv = ThreadingHTTPServer(("127.0.0.1", 0), H)
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()
        self.url = f"http://127.0.0.1:{self.srv.server_address[1]}"


class Versioning(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.config = os.path.join(self.tmp, "config.xml")
        Path(self.config).write_text(CONFIG)
        self.st = Fake()
        self.addCleanup(self.st.srv.shutdown)

    def run_script(self):
        env = dict(os.environ, SYNCTHING_URL=self.st.url, SYNCTHING_CONFIG=self.config)
        return subprocess.run([sys.executable, str(SCRIPT)], env=env, capture_output=True, text=True, timeout=30)

    def test_unversioned_folders_and_the_defaults_get_30_days(self):
        r = self.run_script()
        self.assertEqual(r.returncode, 0, r.stderr)
        for v in (self.st.folders["default"]["versioning"], self.st.defaults["versioning"]):
            self.assertEqual(v["type"], "simple")
            self.assertEqual(v["params"]["cleanoutDays"], "30")
        self.assertNotIn(KEY, r.stdout + r.stderr)

    def test_a_versioning_someone_set_stays(self):
        self.run_script()
        self.assertEqual(self.st.folders["fotos-handy"]["versioning"], STAGGERED)
        self.assertNotIn("/rest/config/folders/fotos-handy", [p for p, _ in self.st.patches])

    def test_a_second_run_changes_nothing(self):
        self.run_script()
        n = len(self.st.patches)
        r = self.run_script()
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(len(self.st.patches), n, self.st.patches[n:])

    def test_a_wrong_key_fails_and_says_why(self):
        Path(self.config).write_text(CONFIG.replace(KEY, "other"))
        r = self.run_script()
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("403", r.stdout + r.stderr)
        self.assertEqual(self.st.patches, [])


if __name__ == "__main__":
    unittest.main()
