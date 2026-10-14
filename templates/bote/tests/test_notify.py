"""#177, ADR 0022 §3: bote-notify takes a message for a person over HTTP with
a bearer and delivers it with Hermes' own sender, preferred channel first.
`hermes send` is faked by an injectable command that records each call."""
import importlib.util, json, os, sys, tempfile, threading, unittest, urllib.error, urllib.request
from pathlib import Path

HERE = Path(__file__).resolve().parent.parent          # templates/bote/
spec = importlib.util.spec_from_file_location("notify", HERE / "notify.py")
notify = importlib.util.module_from_spec(spec); spec.loader.exec_module(notify)

FAKE = """import json, os, sys
to = sys.argv[sys.argv.index("--to") + 1]
text = sys.stdin.read()
with open(os.environ["FAKE_LOG"], "a") as f:
    f.write(json.dumps({"argv": sys.argv[1:], "to": to, "text": text, "home": os.environ.get("HERMES_HOME", "")}) + "\\n")
sys.exit(1 if to in os.environ.get("FAKE_FAIL", "").split(",") else 0)
"""


class Notify(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp())
        (self.tmp / "fake.py").write_text(FAKE)
        self.people = self.tmp / "people.json"
        self.people.write_text(json.dumps({"mama": ["signal:+4917012345678", "telegram:123"], "papa": ["telegram:456"]}))
        self.src = self.tmp / "hermes"; self.src.mkdir()
        (self.src / ".env").write_text("SIGNAL_ACCOUNT=+491\n")
        (self.src / "config.yaml").write_text("model: {}\n")
        (self.src / "SOUL.md").write_text("not needed for sending\n")
        os.environ["FAKE_LOG"] = str(self.tmp / "calls.jsonl")
        os.environ.pop("FAKE_FAIL", None)
        self.lines = []
        self.cfg = notify.Config(token="s3cret", people=str(self.people),
                                 send=[sys.executable, str(self.tmp / "fake.py"), "send"],
                                 hermes_src=str(self.src), hermes_home=str(self.tmp / "home"),
                                 max_text=40, max_body=512, per_person=3, total=5,
                                 log=self.lines.append)
        self.srv = notify.server(self.cfg, ("127.0.0.1", 0))
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()
        self.url = f"http://127.0.0.1:{self.srv.server_port}/mitteilung"

    def tearDown(self):
        self.srv.shutdown(); self.srv.server_close()

    def post(self, body, token="s3cret", raw=None, path="/mitteilung"):
        data = raw if raw is not None else json.dumps(body).encode()
        headers = {"Content-Type": "application/json"}
        if token is not None:
            headers["Authorization"] = "Bearer " + token
        req = urllib.request.Request(self.url.replace("/mitteilung", path), data=data, headers=headers)
        try:
            with urllib.request.urlopen(req, timeout=10) as r:
                return r.status, json.loads(r.read() or b"{}")
        except urllib.error.HTTPError as e:
            return e.code, json.loads(e.read() or b"{}")

    def calls(self):
        p = self.tmp / "calls.jsonl"
        return [json.loads(l) for l in p.read_text().splitlines()] if p.exists() else []

    def test_the_bearer_is_required(self):
        for token in (None, "", "wrong", "s3cretx"):
            self.assertEqual(self.post({"an": "mama", "text": "hallo"}, token=token)[0], 401, token)
        self.assertEqual(self.calls(), [])

    def test_without_a_configured_token_nothing_is_sent(self):
        self.srv.cfg.token = ""
        self.assertEqual(self.post({"an": "mama", "text": "hallo"}, token="")[0], 401)
        self.assertEqual(self.calls(), [])

    def test_an_unknown_person_is_refused_and_never_a_free_target(self):
        for an in ("oma", "signal:+4917012345678", "telegram:123", ""):
            self.assertEqual(self.post({"an": an, "text": "hallo"})[0], 404, an)
        self.assertEqual(self.calls(), [])

    def test_delivers_on_the_first_channel_with_hermes_send(self):
        code, body = self.post({"an": "Mama", "text": "Waschmaschine fertig"})
        self.assertEqual(code, 200, body)
        calls = self.calls()
        self.assertEqual([c["to"] for c in calls], ["signal:+4917012345678"])
        self.assertEqual(calls[0]["text"], "Waschmaschine fertig")
        self.assertEqual(calls[0]["argv"][:3], ["send", "--to", "signal:+4917012345678"])
        self.assertEqual(body["kanal"], "signal")

    def test_falls_back_to_the_next_channel_in_order(self):
        os.environ["FAKE_FAIL"] = "signal:+4917012345678"
        code, body = self.post({"an": "mama", "text": "hallo"})
        self.assertEqual(code, 200, body)
        self.assertEqual([c["to"] for c in self.calls()], ["signal:+4917012345678", "telegram:123"])
        self.assertEqual(body["kanal"], "telegram")
        os.environ["FAKE_FAIL"] = "signal:+4917012345678,telegram:123"
        self.assertEqual(self.post({"an": "mama", "text": "hallo"})[0], 502)

    def test_hermes_reads_a_private_copy_of_its_config(self):
        self.post({"an": "papa", "text": "hallo"})
        home = Path(self.calls()[0]["home"])
        self.assertEqual(home, self.tmp / "home")
        self.assertEqual((home / ".env").read_text(), "SIGNAL_ACCOUNT=+491\n")
        self.assertTrue((home / "config.yaml").exists())
        self.assertFalse((home / "SOUL.md").exists())

    def test_caps_and_refusals(self):
        self.assertEqual(self.post(None, raw=b"x" * 600)[0], 413)
        self.assertEqual(self.post(None, raw=b"not json")[0], 400)
        self.assertEqual(self.post({"an": "mama"})[0], 400)
        self.assertEqual(self.post({"an": "mama", "text": "  "})[0], 400)
        self.assertEqual(self.post({"an": "mama", "text": 5})[0], 400)
        # Hermes would attach a local file named after MEDIA: (send_message_tool)
        self.assertEqual(self.post({"an": "mama", "text": "sieh MEDIA:/opt/data/.env"})[0], 400)
        self.assertEqual(self.post({"an": "mama", "text": "x"}, path="/anderes")[0], 404)
        self.assertEqual(self.calls(), [])
        code, _ = self.post({"an": "mama", "text": "a" * 100})
        self.assertEqual(code, 200)
        self.assertEqual(len(self.calls()[0]["text"]), 40)

    def test_rate_limit_per_person_and_in_total(self):
        codes = [self.post({"an": "mama", "text": "x"})[0] for _ in range(4)]
        self.assertEqual(codes, [200, 200, 200, 429])
        codes = [self.post({"an": "papa", "text": "x"})[0] for _ in range(3)]
        self.assertEqual(codes, [200, 200, 429])  # five in total per minute

    def test_the_log_names_the_person_and_a_short_prefix_only(self):
        self.post({"an": "mama", "text": "Die Waschmaschine ist fertig, bitte ausräumen"})
        line = "\n".join(self.lines)
        self.assertIn("an=mama", line)
        self.assertIn("kanal=signal", line)
        self.assertNotIn("ausräumen", line)
        self.assertNotIn("+4917012345678", line)


if __name__ == "__main__":
    unittest.main()
