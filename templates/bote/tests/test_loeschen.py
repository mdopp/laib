"""#1113 (owner, 2026-11-08): the chat line that announced a proposal goes for
everyone once it is decided. bote-notify notes which message it was (Signal:
signal-cli's send timestamp; Telegram: hermes send --json's message_id) and
POST /loeschen deletes it, within Signal's day and Telegram's 48 hours, best
effort. signal-cli's daemon and the Telegram API are faked on loopback."""
import importlib.util, json, os, sys, tempfile, threading, time, unittest, urllib.error, urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

HERE = Path(__file__).resolve().parent.parent          # templates/bote/
spec = importlib.util.spec_from_file_location("notify", HERE / "notify.py")
notify = importlib.util.module_from_spec(spec); spec.loader.exec_module(notify)

HERMES = """import json, os, sys
to = sys.argv[sys.argv.index("--to") + 1]
with open(os.environ["FAKE_LOG"], "a") as f:
    f.write(json.dumps({"argv": sys.argv[1:], "to": to, "text": sys.stdin.read()}) + "\\n")
if "--json" in sys.argv:
    print(json.dumps({"success": True, "platform": to.split(":")[0], "chat_id": to.split(":")[1], "message_id": "4711"}, indent=2))
else:
    print("sent")
"""


class Fake(BaseHTTPRequestHandler):
    """signal-cli's /api/v1/rpc and Telegram's /bot<token>/deleteMessage, in one"""
    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        self.server.calls.append((self.path, body))
        if self.server.fail:
            self.send_response(500); self.end_headers(); return
        if self.path == "/api/v1/rpc":
            out = {"jsonrpc": "2.0", "id": body["id"], "result": {"timestamp": 1762600000123, "results": []}}
            if body["method"] == "remoteDelete":
                out["result"] = {"timestamp": 1762600009999, "results": []}
        else:
            out = {"ok": True, "result": True}
        b = json.dumps(out).encode()
        self.send_response(200); self.send_header("Content-Length", str(len(b))); self.end_headers(); self.wfile.write(b)

    def log_message(self, *a):
        pass


class Loeschen(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp())
        self.fake = ThreadingHTTPServer(("127.0.0.1", 0), Fake)
        self.fake.calls, self.fake.fail = [], False
        threading.Thread(target=self.fake.serve_forever, daemon=True).start()
        fake_url = f"http://127.0.0.1:{self.fake.server_port}"
        (self.tmp / "hermes.py").write_text(HERMES)
        (self.tmp / "people.json").write_text(json.dumps({"martin": ["signal:+4917012345678"], "papa": ["telegram:456"]}))
        self.src = self.tmp / "hermes"; self.src.mkdir()
        (self.src / ".env").write_text(f"SIGNAL_HTTP_URL={fake_url}\nSIGNAL_ACCOUNT=+491\nTELEGRAM_BOT_TOKEN=bot-geheim\n")
        (self.tmp / "gesendet").mkdir()
        os.environ["FAKE_LOG"] = str(self.tmp / "calls.jsonl")
        self.lines = []
        self.cfg = notify.Config(token="s3cret", people=str(self.tmp / "people.json"),
                                 send=[sys.executable, str(self.tmp / "hermes.py"), "send"],
                                 hermes_src=str(self.src), hermes_home=str(self.tmp / "home"),
                                 sent=str(self.tmp / "gesendet" / "freigaben.json"), telegram_api=fake_url,
                                 channel_timeout=3, deadline=5, log=self.lines.append)
        self.srv = notify.server(self.cfg, ("127.0.0.1", 0))
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()

    def tearDown(self):
        self.srv.shutdown(); self.srv.server_close(); self.fake.shutdown(); self.fake.server_close()

    def post(self, path, body):
        req = urllib.request.Request(f"http://127.0.0.1:{self.srv.server_port}{path}", data=json.dumps(body).encode(),
                                     headers={"Content-Type": "application/json", "Authorization": "Bearer s3cret"})
        try:
            with urllib.request.urlopen(req, timeout=10) as r:
                return r.status, json.loads(r.read())
        except urllib.error.HTTPError as e:
            return e.code, json.loads(e.read() or b"{}")

    def book(self):
        p = Path(self.cfg.sent)
        return json.loads(p.read_text()) if p.exists() else {}

    def hermes_calls(self):
        p = self.tmp / "calls.jsonl"
        return [json.loads(l) for l in p.read_text().splitlines()] if p.exists() else []

    def test_signal_notice_is_sent_by_signal_cli_noted_and_deleted_for_everyone(self):
        self.assertEqual(self.post("/mitteilung", {"an": "martin", "text": "Freigabe #17: eins", "freigabe": 17}),
                         (200, {"ok": True, "kanal": "signal"}))
        path, send = self.fake.calls[0]
        self.assertEqual((path, send["method"], send["params"]),
                         ("/api/v1/rpc", "send", {"account": "+491", "message": "Freigabe #17: eins", "recipient": ["+4917012345678"]}))
        self.assertEqual(self.hermes_calls(), [], "the notice went once, not also through Hermes")
        self.assertEqual(self.book()["17"]["id"], "1762600000123")
        self.assertEqual(os.stat(self.cfg.sent).st_mode & 0o777, 0o600, "the note names the number")
        self.assertEqual(self.post("/loeschen", {"freigabe": 17}), (200, {"ok": True, "geloescht": True}))
        _, rd = self.fake.calls[1]
        self.assertEqual((rd["method"], rd["params"]),
                         ("remoteDelete", {"account": "+491", "targetTimestamp": 1762600000123, "recipient": ["+4917012345678"]}))
        self.assertEqual(self.book(), {}, "deleted is forgotten")
        self.assertEqual(self.post("/loeschen", {"freigabe": 17}), (200, {"ok": True, "geloescht": False}))
        self.assertEqual(len(self.fake.calls), 2, "nothing is deleted twice")
        self.assertFalse(any("+4917012345678" in l for l in self.lines), self.lines)

    def test_other_messages_still_go_through_hermes_and_are_not_noted(self):
        self.assertEqual(self.post("/mitteilung", {"an": "martin", "text": "Laib: wieder grün"})[0], 200)
        self.assertEqual(self.fake.calls, [])
        self.assertEqual(len(self.hermes_calls()), 1)
        self.assertNotIn("--json", self.hermes_calls()[0]["argv"])
        self.assertEqual(self.book(), {})
        for bad in (True, "17", -1, 0):  # never a free id
            self.post("/mitteilung", {"an": "martin", "text": "x", "freigabe": bad})
        self.assertEqual(self.book(), {})

    def test_telegram_notice_is_noted_from_hermes_json_and_deleted_by_the_bot(self):
        self.assertEqual(self.post("/mitteilung", {"an": "papa", "text": "Freigabe #5: zwei", "freigabe": 5})[0], 200)
        self.assertIn("--json", self.hermes_calls()[0]["argv"])
        self.assertEqual(self.book()["5"]["id"], "4711")
        self.assertEqual(self.post("/loeschen", {"freigabe": 5}), (200, {"ok": True, "geloescht": True}))
        self.assertEqual(self.fake.calls, [("/botbot-geheim/deleteMessage", {"chat_id": "456", "message_id": 4711})])
        self.assertFalse(any("bot-geheim" in l for l in self.lines), "the bot token is never logged")

    def test_past_signals_day_nothing_is_sent(self):
        self.post("/mitteilung", {"an": "martin", "text": "Freigabe #17: eins", "freigabe": 17})
        book = self.book(); book["17"]["at"] = time.time() - 24 * 3600 - 60
        Path(self.cfg.sent).write_text(json.dumps(book))
        self.assertEqual(self.post("/loeschen", {"freigabe": 17}), (200, {"ok": True, "geloescht": False}))
        self.assertEqual([c[1]["method"] for c in self.fake.calls], ["send"], "no remoteDelete past the day")

    def test_a_failure_is_one_line_and_a_200(self):
        self.post("/mitteilung", {"an": "martin", "text": "Freigabe #17: eins", "freigabe": 17})
        self.fake.fail = True
        self.lines.clear()
        self.assertEqual(self.post("/loeschen", {"freigabe": 17}), (200, {"ok": True, "geloescht": False}))
        self.assertEqual(self.lines, ["loeschen: #17 signal fehlgeschlagen: HTTPError"])
        self.assertEqual(self.post("/loeschen", {"freigabe": "x"})[0], 400)

    def test_signal_cli_away_the_notice_still_goes_through_hermes(self):
        self.fake.fail = True
        self.assertEqual(self.post("/mitteilung", {"an": "martin", "text": "Freigabe #17: eins", "freigabe": 17})[0], 200)
        self.assertEqual(len(self.hermes_calls()), 1, "the owner hears of it all the same")
        self.assertEqual(self.book(), {}, "without an id there is nothing to delete later")

    def test_without_the_folder_the_notice_goes_all_the_same(self):
        os.rmdir(self.tmp / "gesendet")
        self.assertEqual(self.post("/mitteilung", {"an": "martin", "text": "Freigabe #17: eins", "freigabe": 17})[0], 200)
        self.assertTrue(any(l.startswith("mitteilung: #17 nicht vorgemerkt") for l in self.lines), self.lines)


if __name__ == "__main__":
    unittest.main()
