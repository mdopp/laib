"""#785: bote-check.py writes the Bote's detail line for the Box page: whether
Signal works (signal-cli's daemon answers /api/v1/check; it runs only with a
linked account), which other channels are switched on, and with usage-metrics'
token the last 7 days' conversations and unanswered turns (the bote app's
events zug and unbeantwortet). Fakes stand in for signal-cli and usage-metrics;
no phone number appears anywhere, a part not measured says so."""
import json, os, subprocess, sys, tempfile, threading, unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent.parent / "bote-check.py"
TOKEN = "-um-token-Bt"
NUMBER = "+4915112345678"


def serve(handler):
    srv = ThreadingHTTPServer(("127.0.0.1", 0), handler)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    return f"http://127.0.0.1:{srv.server_address[1]}"


def handler(route):
    class H(BaseHTTPRequestHandler):
        def log_message(self, *a): pass

        def do_GET(self):
            code, body = route(self)
            b = json.dumps(body).encode() if body is not None else b""
            self.send_response(code); self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(b))); self.end_headers(); self.wfile.write(b)
    return H


def signal(code=200):
    return serve(handler(lambda h: (code if h.path == "/api/v1/check" else 404, None)))


def usage(events=(("zug", 42), ("unbeantwortet", 3), ("werkzeug.ha", 9))):
    def route(h):
        if h.headers.get("Authorization") != "Bearer " + TOKEN:
            return 401, {"error": "unauthorized"}
        if h.path != "/summary?days=7":
            return 400, {"error": "nope"}
        apps = [{"app": "bote", "total": sum(c for _, c in events), "events": [{"event": e, "count": c} for e, c in events]}] if events else []
        return 200, {"days": 7, "total": 0, "apps": apps + [{"app": "haus", "total": 5, "events": [{"event": "zug", "count": 5}]}], "knownApps": []}
    return serve(handler(route))


def run(signal_url="http://127.0.0.1:9", usage_url="http://127.0.0.1:9", token=TOKEN, **channels):
    d = tempfile.mkdtemp()
    env = {"PATH": os.environ["PATH"], "SIGNAL_URL": signal_url, "USAGE_URL": usage_url, "USAGE_TOKEN": token, "STATUS_DIR": d}
    env.update({k: v for k, v in channels.items()})
    r = subprocess.run([sys.executable, "-"], input=SCRIPT.read_text(), text=True, capture_output=True, env=env, timeout=60)
    with open(os.path.join(d, "laib-status.json")) as f:
        return r, json.load(f)


class Check(unittest.TestCase):
    def test_signal_and_the_week(self):
        r, st = run(signal(), usage(), CH_SIGNAL=NUMBER)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(st["detail"], "Signal verbunden · letzte 7 Tage: 42 Gespräche, 3 unbeantwortet")
        self.assertEqual(st["findings"], [])
        self.assertNotIn(NUMBER, r.stdout + r.stderr + json.dumps(st))
        self.assertNotIn(TOKEN, json.dumps(st))

    def test_other_channels_are_named_as_switched_on(self):
        _, st = run(signal(), usage(events=()), CH_SIGNAL=NUMBER, CH_MATRIX="https://m.example.org", CH_TELEGRAM="123", CH_DISCORD="")
        self.assertEqual(st["detail"], "Signal verbunden · Matrix, Telegram eingeschaltet · letzte 7 Tage: keine Gespräche")

    def test_signal_silent_is_yellow(self):
        _, st = run(signal(503), usage(), CH_SIGNAL=NUMBER)
        self.assertTrue(st["detail"].startswith("Signal antwortet nicht"), st["detail"])
        self.assertEqual(st["findings"][0]["severity"], "gelb")
        self.assertNotIn(NUMBER, json.dumps(st))

    def test_without_signal_it_is_not_asked(self):
        _, st = run("http://127.0.0.1:9", usage(), CH_MATRIX="https://m.example.org")
        self.assertEqual(st["detail"], "Matrix eingeschaltet · letzte 7 Tage: 42 Gespräche, 3 unbeantwortet")

    def test_usage_without_token_or_refused(self):
        _, st = run(signal(), usage(), token="", CH_SIGNAL=NUMBER)
        self.assertEqual(st["detail"], "Signal verbunden")
        _, st = run(signal(), usage(), token="wrong", CH_SIGNAL=NUMBER)
        self.assertEqual(st["detail"], "Signal verbunden · Gespräche nicht gemessen (HTTP 401)")
        self.assertTrue(st["findings"][0]["unmeasured"])


if __name__ == "__main__":
    unittest.main()
