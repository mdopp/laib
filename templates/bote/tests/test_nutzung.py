"""#266: the Bote's usage counts, a Hermes plugin (templates/bote/nutzung.py,
placed by post-deploy as plugins/laib-nutzung). Hermes calls its hooks with
keyword arguments (hermes-agent model_tools.py post_tool_call,
agent/turn_finalizer.py transform_llm_output + post_llm_call); here they are
called the same way, and a fake usage-metrics records what arrives. Only
counts per (event, day) and the sentence of a turn the Bote could not help
with may leave: no sender, no number, no session."""
import datetime, importlib.util, io, json, os, sys, threading, time, unittest
from contextlib import redirect_stdout
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

PLUGIN = Path(__file__).resolve().parent.parent / "nutzung.py"
TOKEN = "um-tok-266"
SEEN = []


class FakeUsageMetrics(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers.get("Content-Length") or 0)))
        SEEN.append((self.path, self.headers.get("Authorization"), body))
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(b"{}")


class Ctx:
    def __init__(self):
        self.hooks = {}

    def register_hook(self, name, fn):
        self.hooks[name] = fn


def load(url):
    os.environ["USAGE_METRICS_URL"], os.environ["USAGE_METRICS_TOKEN"] = url, TOKEN
    spec = importlib.util.spec_from_file_location("laib_nutzung_%d" % time.monotonic_ns(), PLUGIN)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    mod.INTERVAL = 3600  # the test flushes by hand
    ctx = Ctx()
    mod.register(ctx)
    return mod, ctx.hooks


def turn(hooks, message, answer, turn_id, platform="signal"):
    """One turn as Hermes runs it: the output transform, then post_llm_call."""
    out = hooks["transform_llm_output"](response_text=answer, session_id="s-1", model="m", platform=platform, turn_id=turn_id)
    hooks["post_llm_call"](session_id="s-1", task_id="t", turn_id=turn_id, user_message=message,
                           assistant_response=out or answer, conversation_history=[], model="m", platform=platform)
    return out


class Nutzung(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.srv = HTTPServer(("127.0.0.1", 0), FakeUsageMetrics)
        threading.Thread(target=cls.srv.serve_forever, daemon=True).start()
        cls.url = "http://127.0.0.1:%d" % cls.srv.server_port

    @classmethod
    def tearDownClass(cls):
        cls.srv.shutdown()

    def setUp(self):
        SEEN.clear()
        self.day = datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%d")

    def ingested(self):
        return {b["event"]: b["count"] for p, _, b in SEEN if p == "/ingest"}

    def test_tool_calls_are_counted_per_tool(self):
        mod, hooks = load(self.url)
        for name in ["mcp__haushalt__kalender", "mcp__haushalt__kalender", "mcp__ha__HassTurnOn", "mcp__persona__eingabe", "memory"]:
            hooks["post_tool_call"](tool_name=name, args={"q": "Anna"}, result="geheim", session_id="s-1", status="ok")
        mod.flush()
        self.assertEqual(self.ingested(), {"werkzeug.haushalt.kalender": 2, "werkzeug.ha.hassturnon": 1, "werkzeug.memory": 1},
                         "the persona router sees every message by rule; its count would be the turn count")
        for path, auth, body in SEEN:
            self.assertEqual(auth, "Bearer " + TOKEN)
            self.assertEqual(body, {"app": "bote", "event": body["event"], "day": self.day, "count": body["count"]})

    def test_an_unanswered_turn_sends_its_sentence_without_the_sender(self):
        mod, hooks = load(self.url)
        out = turn(hooks, "[Anna Beispiel] Wann kommt  der\nMüll?", "[unbeantwortet] Das weiß ich leider nicht.", "turn-1")
        self.assertEqual(out, "Das weiß ich leider nicht.", "the marker must never reach the person")
        self.assertIsNone(turn(hooks, "Mach das Licht an", "Erledigt.", "turn-2"), "an answered turn is left as it is")
        mod.flush()
        self.assertEqual(self.ingested(), {"zug": 2, "unbeantwortet": 1})
        misses = [(auth, b) for p, auth, b in SEEN if p == "/miss"]
        self.assertEqual(misses, [("Bearer " + TOKEN, {"source": "bote", "text": "Wann kommt der Müll?", "day": self.day})])
        sent = json.dumps([b for _, _, b in SEEN], ensure_ascii=False)
        for who in ["Anna", "Beispiel", "s-1", "turn-1", "signal", "geheim"]:
            self.assertNotIn(who, sent, "nothing about who asked, or how, may leave")

    def test_group_and_backfill_prefixes_are_stripped(self):
        mod, hooks = load(self.url)
        turn(hooks, "Kanal: Familie\n\n[New message]\n[Fri 2026-10-16 08:00:00 CEST] [Ben] Wer hat Geburtstag?", "[unbeantwortet]", "t-3")
        turn(hooks, [{"type": "text", "text": "[Mia] Was kostet Strom?"}, {"type": "image_url", "image_url": {"url": "data:"}}], "Weiß ich nicht. [unbeantwortet]", "t-4")
        mod.flush()
        self.assertEqual(sorted(b["text"] for p, _, b in SEEN if p == "/miss"), ["Was kostet Strom?", "Wer hat Geburtstag?"])

    def test_a_marker_alone_still_answers_in_words(self):
        mod, hooks = load(self.url)
        out = turn(hooks, "Hallo?", "  [unbeantwortet]  ", "t-5")
        self.assertTrue(out and "[unbeantwortet]" not in out, "Hermes keeps the text unchanged when a transform returns empty")

    def test_a_long_sentence_is_cut_to_what_usage_metrics_takes(self):
        mod, hooks = load(self.url)
        turn(hooks, "ä" * 400, "[unbeantwortet]", "t-6")
        mod.flush()
        (text,) = [b["text"] for p, _, b in SEEN if p == "/miss"]
        self.assertEqual(len(text), 280)

    def test_cron_turns_are_not_the_family_asking(self):
        mod, hooks = load(self.url)
        turn(hooks, "Morgenbericht", "[unbeantwortet]", "t-7", platform="cron")
        mod.flush()
        self.assertEqual(SEEN, [])

    def test_usage_metrics_down_drops_the_counts_and_says_so_once(self):
        mod, hooks = load("http://127.0.0.1:9")  # nothing listens on discard
        hooks["post_tool_call"](tool_name="memory")
        log = io.StringIO()
        with redirect_stdout(log):
            started = time.monotonic()
            mod.flush()
            hooks["post_tool_call"](tool_name="memory")
            mod.flush()
            self.assertLess(time.monotonic() - started, 5)
        self.assertEqual(log.getvalue().count("usage-metrics"), 1, log.getvalue())
        mod.URL = self.url
        mod.flush()
        self.assertEqual(SEEN, [], "what was dropped is not sent later")

    def test_hooks_never_raise_on_odd_input(self):
        mod, hooks = load(self.url)
        hooks["post_tool_call"]()
        hooks["post_tool_call"](tool_name=None)
        self.assertIsNone(hooks["transform_llm_output"](response_text=None))
        hooks["post_llm_call"](user_message=None)
        hooks["post_llm_call"](turn_id="x", user_message=42)

    def test_without_url_or_token_nothing_is_registered(self):
        os.environ.pop("USAGE_METRICS_TOKEN", None)
        spec = importlib.util.spec_from_file_location("laib_nutzung_off", PLUGIN)
        mod = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(mod)
        ctx = Ctx()
        mod.register(ctx)
        self.assertEqual(ctx.hooks, {})


if __name__ == "__main__":
    unittest.main()
