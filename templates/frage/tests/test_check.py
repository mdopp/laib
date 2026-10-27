"""#785: frage-check.py writes Frage's detail line for the Box page: one
SearXNG search an hour, as Frage itself asks (format=json, pod-local), and what
came back: how many results, which search engines stayed silent. A fake
SearXNG stands in; a search without results is a gelb finding, a SearXNG that
refuses or does not answer is "nicht gemessen", never a number."""
import json, os, subprocess, sys, tempfile, threading, unittest, urllib.parse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent.parent / "frage-check.py"


class Fake:
    def __init__(self, results=24, silent=(("google", "timeout"), ("brave", "Suspended: too many requests")), code=200):
        self.results, self.silent, self.code, self.seen = results, silent, code, []
        outer = self

        class H(BaseHTTPRequestHandler):
            def log_message(self, *a): pass

            def do_GET(self):
                u = urllib.parse.urlsplit(self.path)
                outer.seen.append((u.path, dict(urllib.parse.parse_qsl(u.query))))
                body = {"query": "x", "results": [{"url": f"https://e.org/{i}", "title": "t"} for i in range(outer.results)],
                        "unresponsive_engines": [list(s) for s in outer.silent]} if u.path == "/search" else {}
                b = json.dumps(body).encode()
                self.send_response(outer.code if u.path == "/search" else 404)
                self.send_header("Content-Type", "application/json"); self.send_header("Content-Length", str(len(b)))
                self.end_headers(); self.wfile.write(b)

        self.srv = ThreadingHTTPServer(("127.0.0.1", 0), H)
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()
        self.url = f"http://127.0.0.1:{self.srv.server_address[1]}"


def run(url):
    d = tempfile.mkdtemp()
    env = {"PATH": os.environ["PATH"], "SEARX_URL": url, "STATUS_DIR": d}
    r = subprocess.run([sys.executable, "-"], input=SCRIPT.read_text(), text=True, capture_output=True, env=env, timeout=60)
    with open(os.path.join(d, "laib-status.json")) as f:
        return r, json.load(f)


class Check(unittest.TestCase):
    def test_results_and_silent_engines(self):
        s = Fake()
        r, st = run(s.url)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(st["detail"], "Websuche antwortet: 24 Treffer, 2 Suchmaschinen still (google, brave)")
        self.assertEqual(st["findings"], [])
        self.assertEqual(len(s.seen), 1, "one search per run")
        self.assertEqual(s.seen[0][1].get("format"), "json")

    def test_all_engines_answer(self):
        _, st = run(Fake(silent=()).url)
        self.assertEqual(st["detail"], "Websuche antwortet: 24 Treffer")

    def test_no_results_is_yellow(self):
        _, st = run(Fake(results=0).url)
        self.assertEqual(st["detail"], "Websuche findet nichts: 2 Suchmaschinen still (google, brave)")
        self.assertEqual(st["findings"][0]["severity"], "gelb")

    def test_refused_and_silent(self):
        _, st = run(Fake(code=403).url)
        self.assertTrue(st["detail"].startswith("nicht gemessen: SearXNG verweigert die Suche (HTTP 403)"), st["detail"])
        self.assertTrue(st["findings"][0]["unmeasured"])
        _, st = run("http://127.0.0.1:9")
        self.assertTrue(st["detail"].startswith("nicht gemessen: SearXNG antwortet nicht"), st["detail"])


if __name__ == "__main__":
    unittest.main()
