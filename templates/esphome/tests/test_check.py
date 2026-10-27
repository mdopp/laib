"""#785: esphome-check.py writes ESPHome's detail line for the Box page: the
devices the dashboard has, and which of them were built with an older ESPHome
than the dashboard's own (an update is ready), from the dashboard's GET
/devices. A fake dashboard (the shape of esphome/dashboard ListDevicesHandler)
stands in; a silent dashboard is "nicht gemessen", never a number."""
import json, os, subprocess, sys, tempfile, threading, unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent.parent / "esphome-check.py"


def dev(name, deployed, current="2026.10.1"):
    return {"name": name, "friendly_name": name.title(), "configuration": name + ".yaml", "deployed_version": deployed, "current_version": current}


class Fake:
    def __init__(self, body, code=200):
        outer = self
        self.body, self.code, self.seen = body, code, []

        class H(BaseHTTPRequestHandler):
            def log_message(self, *a): pass

            def do_GET(self):
                outer.seen.append(self.path)
                b = json.dumps(outer.body).encode() if self.path == "/devices" else b""
                self.send_response(outer.code if self.path == "/devices" else 404)
                self.send_header("Content-Type", "application/json"); self.send_header("Content-Length", str(len(b)))
                self.end_headers(); self.wfile.write(b)

        self.srv = ThreadingHTTPServer(("127.0.0.1", 0), H)
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()
        self.url = f"http://127.0.0.1:{self.srv.server_address[1]}"


def run(url):
    d = tempfile.mkdtemp()
    env = {"PATH": os.environ["PATH"], "ESPHOME_URL": url, "STATUS_DIR": d}
    r = subprocess.run([sys.executable, "-"], input=SCRIPT.read_text(), text=True, capture_output=True, env=env, timeout=60)
    with open(os.path.join(d, "laib-status.json")) as f:
        return r, json.load(f)


class Check(unittest.TestCase):
    def test_one_device_with_an_update(self):
        r, st = run(Fake({"configured": [dev("voice", "2026.10.0")], "importable": []}).url)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(st["detail"], "1 Gerät eingerichtet, 1 mit Update bereit (ESPHome 2026.10.1)")
        self.assertEqual(st["findings"], [])

    def test_current_never_built_and_found(self):
        body = {"configured": [dev("a", "2026.10.1"), dev("b", None), dev("c", "2026.10.1")],
                "importable": [{"name": "neu", "package_import_url": "github://x"}]}
        _, st = run(Fake(body).url)
        self.assertEqual(st["detail"], "3 Geräte eingerichtet, alle aktuell · 1 neues im Netz gefunden")

    def test_none(self):
        _, st = run(Fake({"configured": [], "importable": []}).url)
        self.assertEqual(st["detail"], "noch kein Gerät eingerichtet")

    def test_silent_or_odd_dashboard(self):
        _, st = run("http://127.0.0.1:9")
        self.assertTrue(st["detail"].startswith("nicht gemessen: ESPHome antwortet nicht"), st["detail"])
        self.assertTrue(st["findings"][0]["unmeasured"])
        _, st = run(Fake({"nope": 1}).url)
        self.assertTrue(st["detail"].startswith("nicht gemessen:"), st["detail"])


if __name__ == "__main__":
    unittest.main()
