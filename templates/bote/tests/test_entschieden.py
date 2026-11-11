"""#1183 (owner, 2026-11-11; #1113 deleted it): the chat line that announced a
proposal stays and says how it ended. bote-notify notes which message it was
and its text (Signal: signal-cli's send timestamp; Telegram: hermes send
--json's message_id), and POST /entschieden edits the Inbox link away for a
line saying how it ended, within Signal's day, and reacts ✅ to an applied yes
and ❌ to a no; best effort. signal-cli's daemon and the Telegram API are
faked on loopback."""
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
    """signal-cli's /api/v1/rpc and Telegram's /bot<token>/<method>, in one"""
    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        self.server.calls.append((self.path, body))
        if self.server.fail:
            self.send_response(500); self.end_headers(); return
        if self.path == "/api/v1/rpc":
            out = {"jsonrpc": "2.0", "id": body["id"], "result": {"timestamp": 1762600000123, "results": []}}
            if body["method"] != "send" or "editTimestamp" in body["params"]:
                out["result"] = {"timestamp": 1762600009999, "results": []}
        else:
            out = {"ok": True, "result": True}
        b = json.dumps(out).encode()
        self.send_response(200); self.send_header("Content-Length", str(len(b))); self.end_headers(); self.wfile.write(b)

    def log_message(self, *a):
        pass


class Entschieden(unittest.TestCase):
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

    NOTICE = "Freigabe #17: whoami bekommt ein neues Image · https://laib.e2e.home/inbox#17"

    def test_signal_applied_notice_loses_its_link_and_gets_a_tick(self):
        self.assertEqual(self.post("/mitteilung", {"an": "martin", "text": self.NOTICE, "freigabe": 17}),
                         (200, {"ok": True, "kanal": "signal"}))
        path, send = self.fake.calls[0]
        self.assertEqual((path, send["method"], send["params"]),
                         ("/api/v1/rpc", "send", {"account": "+491", "message": self.NOTICE, "recipient": ["+4917012345678"]}))
        self.assertEqual(self.hermes_calls(), [], "the notice went once, not also through Hermes")
        self.assertEqual((self.book()["17"]["id"], self.book()["17"]["text"]), ("1762600000123", self.NOTICE))
        self.assertEqual(os.stat(self.cfg.sent).st_mode & 0o777, 0o600, "the note names the number")
        self.lines.clear()
        self.assertEqual(self.post("/entschieden", {"freigabe": 17, "status": "approved", "outcome": "effective", "neu": 0}),
                         (200, {"ok": True, "geaendert": True, "markiert": True}))
        (_, edit), (_, react) = self.fake.calls[1:]
        self.assertEqual((edit["method"], edit["params"]),
                         ("send", {"account": "+491", "message": "Freigabe #17: whoami bekommt ein neues Image\nFreigegeben und umgesetzt.",
                                   "editTimestamp": 1762600000123, "recipient": ["+4917012345678"]}))
        self.assertEqual((react["method"], react["params"]),
                         ("sendReaction", {"account": "+491", "emoji": "✅", "targetAuthor": "+491", "targetTimestamp": 1762600000123,
                                           "recipient": ["+4917012345678"]}))
        self.assertEqual(self.lines, ["entschieden: #17 signal geaendert markiert"])
        self.assertEqual(self.book(), {}, "marked is forgotten")
        self.assertEqual(self.post("/entschieden", {"freigabe": 17, "status": "approved"}), (200, {"ok": True, "geaendert": False, "markiert": False}))
        self.assertEqual(len(self.fake.calls), 3, "nothing is marked twice")
        self.assertFalse(any("+4917012345678" in l for l in self.lines), self.lines)

    def test_each_end_has_its_line_and_only_a_yes_or_no_a_mark(self):
        self.cfg.per_person = self.cfg.total = 50
        for status, outcome, neu, line, emoji in [
                ("rejected", "rejected", 0, "Abgelehnt.", "❌"),
                ("rejected", "later", 0, "Abgelehnt.", "❌"),
                ("approved", "rolled-back", 0, "Freigegeben, aber es hat nicht geklappt: die Box ist wieder wie vorher, was schiefging, steht in der Inbox.", None),
                ("stale", "", 20, "Veraltet, nicht umgesetzt: neu vorgeschlagen als #20.", None),
                ("stale", "", 0, "Veraltet, nicht umgesetzt.", None),
                ("approved", "", 0, "Freigegeben und umgesetzt.", "✅"),
                ("", "", 0, "Nicht mehr offen.", None)]:
            with self.subTest(status=status, outcome=outcome):
                self.fake.calls.clear()
                self.post("/mitteilung", {"an": "martin", "text": self.NOTICE, "freigabe": 17})
                body = {"freigabe": 17, "status": status, "outcome": outcome, "neu": neu} if status else {"freigabe": 17}
                self.assertEqual(self.post("/entschieden", body), (200, {"ok": True, "geaendert": True, "markiert": emoji is not None}))
                methods = [(c[1]["method"], c[1]["params"].get("message") or c[1]["params"].get("emoji")) for c in self.fake.calls[1:]]
                self.assertEqual(methods, [("send", "Freigabe #17: whoami bekommt ein neues Image\n" + line)] + ([("sendReaction", emoji)] if emoji else []))

    def test_other_messages_still_go_through_hermes_and_are_not_noted(self):
        self.assertEqual(self.post("/mitteilung", {"an": "martin", "text": "Laib: wieder grün"})[0], 200)
        self.assertEqual(self.fake.calls, [])
        self.assertEqual(len(self.hermes_calls()), 1)
        self.assertNotIn("--json", self.hermes_calls()[0]["argv"])
        self.assertEqual(self.book(), {})
        for bad in (True, "17", -1, 0):  # never a free id
            self.post("/mitteilung", {"an": "martin", "text": "x", "freigabe": bad})
        self.assertEqual(self.book(), {})

    def test_telegram_notice_is_edited_and_gets_the_nearest_mark_a_bot_may_set(self):
        self.assertEqual(self.post("/mitteilung", {"an": "papa", "text": "Freigabe #5: zwei · https://laib.e2e.home/inbox#5", "freigabe": 5})[0], 200)
        self.assertIn("--json", self.hermes_calls()[0]["argv"])
        self.assertEqual(self.book()["5"]["id"], "4711")
        self.assertEqual(self.post("/entschieden", {"freigabe": 5, "status": "rejected", "outcome": "rejected", "neu": 0}),
                         (200, {"ok": True, "geaendert": True, "markiert": True}))
        self.assertEqual(self.fake.calls, [
            ("/botbot-geheim/editMessageText", {"chat_id": "456", "message_id": 4711, "text": "Freigabe #5: zwei\nAbgelehnt."}),
            ("/botbot-geheim/setMessageReaction", {"chat_id": "456", "message_id": 4711, "reaction": [{"type": "emoji", "emoji": "👎"}]})])
        self.assertFalse(any("bot-geheim" in l for l in self.lines), "the bot token is never logged")

    def test_past_signals_day_only_the_reaction_goes(self):
        self.post("/mitteilung", {"an": "martin", "text": self.NOTICE, "freigabe": 17})
        book = self.book(); book["17"]["at"] = time.time() - 24 * 3600 - 60
        Path(self.cfg.sent).write_text(json.dumps(book))
        self.assertEqual(self.post("/entschieden", {"freigabe": 17, "status": "approved", "outcome": "applied"}),
                         (200, {"ok": True, "geaendert": False, "markiert": True}))
        self.assertEqual([c[1]["method"] for c in self.fake.calls], ["send", "sendReaction"], "no edit past the day")

    def test_past_keep_nothing_is_sent(self):
        self.post("/mitteilung", {"an": "martin", "text": self.NOTICE, "freigabe": 17})
        book = self.book(); book["17"]["at"] = time.time() - notify.KEEP - 60
        Path(self.cfg.sent).write_text(json.dumps(book))
        self.assertEqual(self.post("/entschieden", {"freigabe": 17, "status": "approved"}), (200, {"ok": True, "geaendert": False, "markiert": False}))
        self.assertEqual([c[1]["method"] for c in self.fake.calls], ["send"])

    def test_a_note_from_before_without_its_text_gets_its_mark_only(self):
        self.post("/mitteilung", {"an": "martin", "text": self.NOTICE, "freigabe": 17})
        book = self.book(); del book["17"]["text"]
        Path(self.cfg.sent).write_text(json.dumps(book))
        self.assertEqual(self.post("/entschieden", {"freigabe": 17, "status": "rejected"}), (200, {"ok": True, "geaendert": False, "markiert": True}))

    def test_a_failure_is_one_line_and_a_200(self):
        self.post("/mitteilung", {"an": "martin", "text": self.NOTICE, "freigabe": 17})
        self.fake.fail = True
        self.lines.clear()
        self.assertEqual(self.post("/entschieden", {"freigabe": 17, "status": "approved"}), (200, {"ok": True, "geaendert": False, "markiert": False}))
        self.assertEqual(self.lines, ["entschieden: #17 signal nichts, fehlgeschlagen: HTTPError"])
        self.assertEqual(self.post("/entschieden", {"freigabe": "x"})[0], 400)
        self.assertEqual(self.post("/loeschen", {"freigabe": 17})[0], 404, "nothing deletes a notice any more")

    def test_a_cut_notice_loses_its_cut_link_too(self):
        self.assertEqual(notify.unlinked("Freigabe #3: lang · https://laib.e2e.home/inb…"), "Freigabe #3: lang")
        self.assertEqual(notify.unlinked("Freigabe #3: a · b ohne Link"), "Freigabe #3: a · b ohne Link")

    def test_signal_cli_away_the_notice_still_goes_through_hermes(self):
        self.fake.fail = True
        self.assertEqual(self.post("/mitteilung", {"an": "martin", "text": self.NOTICE, "freigabe": 17})[0], 200)
        self.assertEqual(len(self.hermes_calls()), 1, "the owner hears of it all the same")
        self.assertEqual(self.book(), {}, "without an id there is nothing to mark later")

    def test_without_the_folder_the_notice_goes_all_the_same(self):
        os.rmdir(self.tmp / "gesendet")
        self.assertEqual(self.post("/mitteilung", {"an": "martin", "text": self.NOTICE, "freigabe": 17})[0], 200)
        self.assertTrue(any(l.startswith("mitteilung: #17 nicht vorgemerkt") for l in self.lines), self.lines)


if __name__ == "__main__":
    unittest.main()
