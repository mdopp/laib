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


KEY = "api-key-Pq7"


def hermes(answers, delay=0.0):
    """a fake Hermes API server: per sentence (answer, tools), streamed as SSE"""
    import time as _t

    class H(BaseHTTPRequestHandler):
        def log_message(self, *a): pass

        def do_POST(self):
            body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            if self.headers.get("Authorization") != "Bearer " + KEY:
                self.send_response(401); self.end_headers(); return
            text, tools = answers.get(body["messages"][-1]["content"], ("", []))
            _t.sleep(delay)
            self.send_response(200); self.send_header("Content-Type", "text/event-stream"); self.end_headers()
            for t in tools:
                self.wfile.write(f"event: hermes.tool.progress\ndata: {json.dumps({'tool': t, 'status': 'running'})}\n\n".encode())
            self.wfile.write(f"data: {json.dumps({'choices': [{'delta': {'content': text}}]})}\n\ndata: [DONE]\n\n".encode())
    return serve(H) + "/v1"


GOOD = {"Hallo": ("Hallo, hier ist Joshua. Zum Beispiel: …", []), "Hilfe": ("Zum Beispiel: „Licht an“", []),
        "Wie hoch ist die Zugspitze?": ("Die Zugspitze ist 2.962 m hoch (Quelle: wikipedia).", ["mcp__frage__frage"]),
        "Was ist die Hauptstadt von Australien?": ("Canberra.", ["mcp__frage__frage"]),
        "Wer hat die Relativitätstheorie entwickelt?": ("Albert Einstein.", ["mcp__frage__frage"]),
        "Wie heißt der Hund meines Nachbarn?": ("Das weiß ich nicht.", []), "Welche Lichter sind gerade an?": ("Keine Lichter sind an.", ["mcp__ha__GetLiveContext"]),
        "Wie spät ist es?": ("Es ist 05:31.", []), "Erzähl mir einen kurzen Witz.": ("Treffen sich zwei …", []), "Danke!": ("Gern!", [])}


def probe(api, **env):
    d = tempfile.mkdtemp()
    e = {"PATH": os.environ["PATH"], "PROBEN": "1", "API_URL": api, "BOTE_API_KEY": KEY, "STATUS_DIR": d, "SIGNAL_URL": "http://127.0.0.1:9",
         "USAGE_URL": "http://127.0.0.1:9", "USAGE_TOKEN": ""}
    e.update(env)
    r = subprocess.run([sys.executable, "-"], input=SCRIPT.read_text(), text=True, capture_output=True, env=e, timeout=120)
    with open(os.path.join(d, "laib-status.json")) as f:
        return r, json.load(f)


class Proben(unittest.TestCase):
    """#862: ten fixed sentences to Hermes' API server nightly: a fact calls frage and names the fact,
    the unknown is said to be unknown, each reply within the limit and without error text; one line in
    the unit's journal, the result on the Box page's line, a failed probe a yellow finding."""

    def test_ten_good_answers_are_one_line_and_green(self):
        r, st = probe(hermes(GOOD))
        self.assertEqual(r.returncode, 0, r.stderr)
        line = [l for l in r.stdout.splitlines() if l.startswith("Bote-Proben:")]
        self.assertEqual(len(line), 1, r.stdout)
        self.assertIn("10 von 10", line[0])
        self.assertIn("Proben 10 von 10", st["detail"])
        self.assertEqual(st["findings"], [])
        self.assertNotIn(KEY, r.stdout + json.dumps(st))

    def test_a_fact_from_memory_a_made_up_answer_and_an_error_are_named(self):
        bad = dict(GOOD)
        bad["Wie hoch ist die Zugspitze?"] = ("Die Zugspitze ist 2.962 m hoch.", [])   # from memory, no frage
        bad["Wie heißt der Hund meines Nachbarn?"] = ("Er heißt Bello.", [])         # made up
        bad["Danke!"] = ("⚠️ The AI model service isn't reachable", [])
        r, st = probe(hermes(bad))
        self.assertIn("Proben 7 von 10", st["detail"])
        found = " | ".join(f["detail"] for f in st["findings"] if f["severity"] == "gelb")
        self.assertIn("„Zugspitze“: kein frage-Werkzeug", found)
        self.assertIn("„Unbekannt“: sagt nicht", found)
        self.assertIn("„Danke“: Fehlertext", found)

    def test_a_slow_reply_fails_its_limit(self):
        r, st = probe(hermes(GOOD, delay=0.3), PROBE_LIMIT="0.1")
        self.assertIn("Proben 0 von 10", st["detail"])
        self.assertIn("zu langsam", st["findings"][0]["detail"])

    def test_no_hermes_is_unmeasured_not_red(self):
        r, st = probe("http://127.0.0.1:9/v1")
        self.assertIn("Proben nicht gemessen", st["detail"])
        self.assertTrue(st["findings"][0]["unmeasured"])
        self.assertEqual(st["findings"][0]["severity"], "info")

    def test_the_unit_runs_nightly_with_the_key_in_the_environment(self):
        unit = (SCRIPT.parent / "bote-proben.service").read_text()
        self.assertIn("-e PROBEN=1 -e BOTE_API_KEY ", unit)
        self.assertIn("Environment=BOTE_API_KEY=${PROBE_KEY}", unit)
        self.assertIn("OnCalendar=*-*-* 05:30", (SCRIPT.parent / "bote-proben.timer").read_text())


if __name__ == "__main__":
    unittest.main()
