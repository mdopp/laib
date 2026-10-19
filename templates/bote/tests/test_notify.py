"""#177, ADR 0022 §3: bote-notify takes a message for a person over HTTP with
a bearer and delivers it with Hermes' own sender, preferred channel first.
`hermes send` is faked by an injectable command that records each call."""
import importlib.util, json, os, sys, tempfile, threading, unittest, urllib.error, urllib.request
from pathlib import Path

HERE = Path(__file__).resolve().parent.parent          # templates/bote/
spec = importlib.util.spec_from_file_location("notify", HERE / "notify.py")
notify = importlib.util.module_from_spec(spec); spec.loader.exec_module(notify)

FAKE = """import json, os, sys, time
to = sys.argv[sys.argv.index("--to") + 1]
text = sys.stdin.read()
home = os.environ.get("HERMES_HOME", "")
env = open(os.path.join(home, ".env")).read() if home and os.path.exists(os.path.join(home, ".env")) else None
with open(os.environ["FAKE_LOG"], "a") as f:
    f.write(json.dumps({"argv": sys.argv[1:], "to": to, "text": text, "home": home, "env": env,
                        "token": "NOTIFY_TOKEN" in os.environ}) + "\\n")
if to in os.environ.get("FAKE_SLEEP", "").split(","):
    time.sleep(5)
if to in os.environ.get("FAKE_FAIL", "").split(","):
    print(f"hermes send: delivery to {to} failed", file=sys.stderr)  # Hermes names the target
    sys.exit(1)
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
        os.environ.pop("FAKE_FAIL", None); os.environ.pop("FAKE_SLEEP", None)
        os.environ["NOTIFY_TOKEN"] = "s3cret"   # as in the container; never handed to Hermes
        self.lines = []
        self.cfg = notify.Config(token="s3cret", people=str(self.people),
                                 send=[sys.executable, str(self.tmp / "fake.py"), "send"],
                                 hermes_src=str(self.src), hermes_home=str(self.tmp / "home"),
                                 max_text=40, max_body=512, per_person=3, total=5,
                                 channel_timeout=1, deadline=3,
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
        self.assertEqual((home / ".env").stat().st_mode & 0o777, 0o600)
        self.assertTrue((home / "config.yaml").exists())
        self.assertFalse((home / "SOUL.md").exists())
        self.assertFalse(self.calls()[0]["token"], "the bearer reached Hermes' environment")

    def test_parallel_sends_never_see_a_half_written_config(self):
        self.srv.cfg.per_person = self.srv.cfg.total = 100
        (self.src / ".env").write_text("SIGNAL_ACCOUNT=+491\n" * 2000)
        threads = [threading.Thread(target=self.post, args=({"an": "papa", "text": "x"},)) for _ in range(12)]
        [t.start() for t in threads]; [t.join() for t in threads]
        envs = [c["env"] for c in self.calls()]
        self.assertEqual(len(envs), 12)
        self.assertTrue(all(e == "SIGNAL_ACCOUNT=+491\n" * 2000 for e in envs), "a send saw a truncated .env")

    def test_a_hanging_channel_times_out_and_the_next_is_tried(self):
        os.environ["FAKE_SLEEP"] = "signal:+4917012345678"
        code, body = self.post({"an": "mama", "text": "hallo"})
        self.assertEqual((code, body.get("kanal")), (200, "telegram"))
        self.assertEqual(notify.Handler.timeout, 10)  # a client that sends nothing is dropped

    def test_one_deadline_for_all_channels(self):
        import time
        os.environ["FAKE_SLEEP"] = "signal:+4917012345678,telegram:123"
        self.srv.cfg.channel_timeout, self.srv.cfg.deadline = 2, 2.5
        t = time.monotonic()
        self.assertEqual(self.post({"an": "mama", "text": "hallo"})[0], 502)
        self.assertLess(time.monotonic() - t, 4)

    def test_caps_and_refusals(self):
        self.assertEqual(self.post(None, raw=b"x" * 600)[0], 413)
        self.assertEqual(self.post(None, raw=b"not json")[0], 400)
        self.assertEqual(self.post({"an": "mama"})[0], 400)
        self.assertEqual(self.post({"an": "mama", "text": "  "})[0], 400)
        self.assertEqual(self.post({"an": "mama", "text": 5})[0], 400)
        # Hermes would attach a local file named after MEDIA: (send_message_tool)
        self.assertEqual(self.post({"an": "mama", "text": "sieh MEDIA:/opt/data/.env"})[0], 400)
        self.assertEqual(self.calls(), [])
        self.assertEqual(self.post({"an": "papa", "text": "Social media: neu"})[0], 200)  # Hermes' marker is MEDIA:, case-sensitive
        (self.tmp / "calls.jsonl").unlink()
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

    def test_a_failed_channel_is_logged_without_its_target(self):
        os.environ["FAKE_FAIL"] = "signal:+4917012345678"
        self.post({"an": "mama", "text": "hallo"})
        line = "\n".join(self.lines)
        self.assertIn("signal", line)
        self.assertIn("rc=1", line)
        self.assertNotIn("+4917012345678", line)
        self.assertNotIn("123", line)


    def test_delivers_on_matrix_and_telegram_through_hermes_send(self):
        # #184: every configured channel is a `hermes send` target, Matrix and Telegram as much as Signal
        self.people.write_text(json.dumps({"lena": ["matrix:!lena:example.org", "telegram:789"]}))
        (self.src / ".env").write_text("MATRIX_HOMESERVER=https://hs.example.org\nMATRIX_ACCESS_TOKEN=t\nMATRIX_E2EE_MODE=off\nTELEGRAM_BOT_TOKEN=b\n")
        code, body = self.post({"an": "lena", "text": "hallo"})
        self.assertEqual((code, body.get("kanal")), (200, "matrix"), body)
        self.assertEqual(self.calls()[0]["argv"][:3], ["send", "--to", "matrix:!lena:example.org"])
        self.assertIn("MATRIX_E2EE_MODE=off", self.calls()[0]["env"])
        os.environ["FAKE_FAIL"] = "matrix:!lena:example.org"
        code, body = self.post({"an": "lena", "text": "hallo"})
        self.assertEqual((code, body.get("kanal")), (200, "telegram"), body)
        self.assertEqual(self.calls()[-1]["argv"][:3], ["send", "--to", "telegram:789"])

    def test_health_answers_and_asks_signal_cli_only_with_signal(self):
        # #184: the pod's health is bote-notify, which every Bote has; with Signal it also asks signal-cli
        import http.server

        def health():
            try:
                with urllib.request.urlopen(self.url.replace("/mitteilung", "/health"), timeout=5) as r:
                    return r.status
            except urllib.error.HTTPError as e:
                return e.code
        (self.src / ".env").write_text("TELEGRAM_BOT_TOKEN=b\n")
        self.assertEqual(health(), 200)
        (self.src / ".env").write_text("SIGNAL_HTTP_URL=http://127.0.0.1:1\nSIGNAL_ACCOUNT=+491\n")
        self.assertEqual(health(), 503)

        # #469: Hermes and the relay speak signal-cli's own daemon (daemon --http), whose
        # health is /api/v1/check; bbernhard's REST API (/v1/about only) is not it
        for path, want in (("/api/v1/check", 200), ("/v1/about", 503)):
            class Daemon(http.server.BaseHTTPRequestHandler):
                answers = path

                def do_GET(self):
                    self.send_response(200 if self.path == self.answers else 404); self.end_headers(); self.wfile.write(b"{}")

                def log_message(self, *a):
                    pass
            d = http.server.HTTPServer(("127.0.0.1", 0), Daemon)
            threading.Thread(target=d.serve_forever, daemon=True).start()
            try:
                (self.src / ".env").write_text(f"SIGNAL_HTTP_URL=http://127.0.0.1:{d.server_port}\nSIGNAL_ACCOUNT=+491\n")
                self.assertEqual(health(), want, path)
            finally:
                d.shutdown(); d.server_close()
        self.assertEqual(self.calls(), [])

if __name__ == "__main__":
    unittest.main()
