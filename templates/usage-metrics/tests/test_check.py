"""#785: usage-metrics-check.py writes usage-metrics' detail line for the Box
page: what was counted in the last 7 days and from which apps (GET
/summary?days=7), and how many sentences went unanswered (GET /misses?days=7),
with the service's own bearer. A fake usage-metrics (the README's contract)
stands in; refused or silent is "nicht gemessen", never a number."""
import json, os, subprocess, sys, tempfile, threading, unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent.parent / "usage-metrics-check.py"
TOKEN = "-um-token-Pz"


class Fake:
    def __init__(self, apps=None, misses=3):
        self.apps = apps if apps is not None else [{"app": "bote", "total": 234, "events": []}, {"app": "haus", "total": 1000, "events": []}]
        self.misses, self.seen = misses, []
        outer = self

        class H(BaseHTTPRequestHandler):
            def log_message(self, *a): pass

            def do_GET(self):
                outer.seen.append(self.path)
                if self.headers.get("Authorization") != "Bearer " + TOKEN:
                    body, code = {"error": "unauthorized"}, 401
                elif self.path == "/summary?days=7":
                    body, code = {"days": 7, "from": "2026-10-21", "total": sum(a["total"] for a in outer.apps), "apps": outer.apps, "knownApps": ["bote", "haus"]}, 200
                elif self.path == "/misses?days=7":
                    body, code = {"days": 7, "from": "2026-10-21", "total": outer.misses, "misses": []}, 200
                else:
                    body, code = {"error": "nope"}, 400
                b = json.dumps(body).encode()
                self.send_response(code); self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(b))); self.end_headers(); self.wfile.write(b)

        self.srv = ThreadingHTTPServer(("127.0.0.1", 0), H)
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()
        self.url = f"http://127.0.0.1:{self.srv.server_address[1]}"


def run(url, token=TOKEN):
    d = tempfile.mkdtemp()
    env = {"PATH": os.environ["PATH"], "USAGE_URL": url, "USAGE_TOKEN": token, "STATUS_DIR": d}
    r = subprocess.run([sys.executable, "-"], input=SCRIPT.read_text(), text=True, capture_output=True, env=env, timeout=60)
    with open(os.path.join(d, "laib-status.json")) as f:
        return r, json.load(f)


class Check(unittest.TestCase):
    def test_counts_apps_and_misses(self):
        r, st = run(Fake().url)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(st["detail"], "letzte 7 Tage: 1.234 Zählungen aus bote, haus · 3 unbeantwortete Fragen")
        self.assertEqual(st["findings"], [])
        self.assertNotIn(TOKEN, r.stdout + r.stderr + json.dumps(st))

    def test_nothing_counted(self):
        _, st = run(Fake(apps=[], misses=0).url)
        self.assertEqual(st["detail"], "letzte 7 Tage: nichts gezählt · keine unbeantworteten Fragen")

    def test_refused_and_silent(self):
        _, st = run(Fake().url, token="wrong")
        self.assertTrue(st["detail"].startswith("nicht gemessen: usage-metrics lehnt den Schlüssel ab"), st["detail"])
        self.assertTrue(st["findings"][0]["unmeasured"])
        _, st = run("http://127.0.0.1:9")
        self.assertTrue(st["detail"].startswith("nicht gemessen: usage-metrics antwortet nicht"), st["detail"])


if __name__ == "__main__":
    unittest.main()
