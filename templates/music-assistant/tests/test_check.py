"""#785: music-assistant-check.py writes Music Assistant's detail line for the Box
page: its speakers, which play now, and its titles, asked through MA's command
API (POST /api) with the token post-deploy minted. A fake MA stands in;
without the token or with MA silent the line says "nicht gemessen", never a number."""
import json, os, subprocess, sys, tempfile, threading, unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent.parent / "music-assistant-check.py"
TOKEN = "-ma-token-Yv"


class Fake:
    def __init__(self, players=None, tracks=24878):
        self.players = players if players is not None else [{"player_id": f"p{i}", "state": "playing" if i < 2 else "idle"} for i in range(8)]
        self.tracks, self.seen = tracks, []
        outer = self

        class H(BaseHTTPRequestHandler):
            def log_message(self, *a): pass

            def do_POST(self):
                body = json.loads(self.rfile.read(int(self.headers.get("Content-Length") or 0)))
                outer.seen.append(body["command"])
                if self.headers.get("Authorization") != "Bearer " + TOKEN:
                    out, code = {"error": "unauthorized"}, 401
                else:
                    out, code = {"players/all": outer.players, "music/tracks/count": outer.tracks}.get(body["command"]), 200
                b = json.dumps(out).encode()
                self.send_response(code); self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(b))); self.end_headers(); self.wfile.write(b)

        self.srv = ThreadingHTTPServer(("127.0.0.1", 0), H)
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()
        self.url = f"http://127.0.0.1:{self.srv.server_address[1]}"


def run(url, token=TOKEN):
    d = tempfile.mkdtemp()
    env = {"PATH": os.environ["PATH"], "MA_URL": url, "MA_TOKEN": token, "STATUS_DIR": d}
    r = subprocess.run([sys.executable, "-"], input=SCRIPT.read_text(), text=True, capture_output=True, env=env, timeout=60)
    with open(os.path.join(d, "laib-status.json")) as f:
        return r, json.load(f)


class Check(unittest.TestCase):
    def test_speakers_playing_and_titles(self):
        ma = Fake()
        r, st = run(ma.url)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(st["detail"], "8 Lautsprecher, 2 spielen gerade · 24.878 Titel")
        self.assertEqual(st["findings"], [])
        self.assertEqual(sorted(ma.seen), ["music/tracks/count", "players/all"], "it only reads")

    def test_nothing_playing(self):
        _, st = run(Fake(players=[{"player_id": "a", "state": "idle"}], tracks=3).url)
        self.assertEqual(st["detail"], "1 Lautsprecher · 3 Titel")

    def test_without_token(self):
        _, st = run(Fake().url, token="")
        self.assertEqual(st["detail"], "nicht gemessen: noch kein Zugang")
        self.assertTrue(st["findings"][0]["unmeasured"])

    def test_refused_and_silent(self):
        _, st = run(Fake().url, token="wrong")
        self.assertTrue(st["detail"].startswith("nicht gemessen: Music Assistant lässt den Zugang nicht herein"), st["detail"])
        _, st = run("http://127.0.0.1:9")
        self.assertTrue(st["detail"].startswith("nicht gemessen: Music Assistant antwortet nicht"), st["detail"])
        self.assertNotIn("Titel", st["detail"])


if __name__ == "__main__":
    unittest.main()
