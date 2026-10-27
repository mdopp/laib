"""#785: adguard-check.py writes AdGuard Home's detail line for the Box page:
how many DNS queries in the statistics' own window and how many it blocked,
from /control/stats with the admin's basic auth. The window is the one
AdGuard reports (time_units × the series' length), never assumed. A fake
AdGuard (the shapes of openapi/openapi.yaml) stands in; a refused login or a
silent AdGuard gives "nicht gemessen" and why, never a number."""
import base64, json, os, subprocess, sys, tempfile, threading, unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent.parent / "adguard-check.py"
PW = "-adguard-pw-Dn"


def stats(units="hours", n=24, queries=12345, blocked=1234):
    return {"time_units": units, "num_dns_queries": queries, "num_blocked_filtering": blocked,
            "num_replaced_safebrowsing": 0, "num_replaced_parental": 0, "avg_processing_time": 0.0123,
            "dns_queries": [0] * n, "blocked_filtering": [0] * n}


class Fake:
    def __init__(self, body=None, refuse=False):
        self.body, self.refuse = body or stats(), refuse
        outer, want = self, "Basic " + base64.b64encode(f"admin:{PW}".encode()).decode()

        class H(BaseHTTPRequestHandler):
            def log_message(self, *a): pass

            def do_GET(self):
                if outer.refuse or self.headers.get("Authorization") != want:
                    code, b = 401, b""
                elif self.path == "/control/stats":
                    code, b = 200, json.dumps(outer.body).encode()
                else:
                    code, b = 404, b""
                self.send_response(code); self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(b))); self.end_headers(); self.wfile.write(b)

        self.srv = ThreadingHTTPServer(("127.0.0.1", 0), H)
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()
        self.url = f"http://127.0.0.1:{self.srv.server_address[1]}"


def run(url):
    d = tempfile.mkdtemp()
    env = {"PATH": os.environ["PATH"], "ADGUARD_URL": url, "ADGUARD_USER": "admin", "ADGUARD_PASSWORD": PW, "STATUS_DIR": d}
    r = subprocess.run([sys.executable, "-"], input=SCRIPT.read_text(), text=True, capture_output=True, env=env, timeout=60)
    with open(os.path.join(d, "laib-status.json")) as f:
        return r, json.load(f)


class Check(unittest.TestCase):
    def test_last_24_hours(self):
        r, st = run(Fake().url)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(st["detail"], "in den letzten 24 h: 12.345 Anfragen, 1.234 blockiert (10 %)")
        self.assertEqual(st["findings"], [])
        self.assertNotIn(PW, r.stdout + r.stderr + json.dumps(st))

    def test_days_window(self):
        _, st = run(Fake(stats("days", 7, 90000, 4500)).url)
        self.assertEqual(st["detail"], "in den letzten 7 Tagen: 90.000 Anfragen, 4.500 blockiert (5 %)")

    def test_no_queries(self):
        _, st = run(Fake(stats(queries=0, blocked=0)).url)
        self.assertEqual(st["detail"], "in den letzten 24 h: keine Anfragen")

    def test_refused_login_is_not_measured(self):
        _, st = run(Fake(refuse=True).url)
        self.assertTrue(st["detail"].startswith("nicht gemessen: AdGuard lässt den Admin nicht herein"), st["detail"])
        self.assertNotIn("Anfragen", st["detail"])
        self.assertTrue(st["findings"][0]["unmeasured"])

    def test_down_is_not_measured(self):
        _, st = run("http://127.0.0.1:9")
        self.assertTrue(st["detail"].startswith("nicht gemessen: AdGuard antwortet nicht"), st["detail"])


if __name__ == "__main__":
    unittest.main()
