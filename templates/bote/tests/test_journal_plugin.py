"""#591: who switched in Home Assistant through the Bote. HA's logbook names
the token's user („Bote“), never the person who wrote. The Hermes plugin
laib-journal (templates/bote/journal_plugin.py, placed by post-deploy as
plugins/laib-journal) learns the sender in pre_llm_call and, after a tool
call that switched something (an HA intent HA reports done for entities, the
Automat's automation_schalten), writes the line home-assistant's README
names into Laib's journal with laib_note and the Bote's token. Reads note
nothing, and a note that fails never fails the switch. Hermes calls the
hooks with keyword arguments; here they are called the same way against a
fake Laib MCP and a fake Home Assistant."""
import datetime, importlib.util, io, json, os, tempfile, threading, unittest
from unittest import mock
from contextlib import redirect_stdout
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

PLUGIN = Path(__file__).resolve().parent.parent / "journal_plugin.py"
NOTES = []
STATES = {}


class FakeLaib(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def do_POST(self):
        req = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        NOTES.append((self.headers.get("Authorization"), req["method"], req["params"]))
        body = json.dumps({"jsonrpc": "2.0", "id": req["id"], "result": {"content": [{"type": "text", "text": "notiert"}], "isError": False}}).encode()
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


class FakeHA(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def do_GET(self):
        entity = self.path.removeprefix("/api/states/")
        if self.headers.get("Authorization") != "Bearer ha-tok" or entity not in STATES:
            self.send_response(404)
            self.end_headers()
            return
        body = json.dumps(STATES[entity]).encode()
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


def serve(handler):
    srv = HTTPServer(("127.0.0.1", 0), handler)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    return srv


class Ctx:
    def __init__(self):
        self.hooks = {}

    def register_hook(self, name, fn):
        self.hooks[name] = fn


def done(*entities, response_type="action_done"):
    """HA's MCP answer to an intent tool (helpers/llm.py IntentResponseDict), as Hermes hands it on"""
    data = {"success": [{"name": e, "type": "entity", "id": e} for e in entities], "failed": []}
    return json.dumps({"result": json.dumps({"speech": {}, "response_type": response_type, "data": data})})


def state(context, seconds_ago=1):
    t = (datetime.datetime.now(datetime.timezone.utc) - datetime.timedelta(seconds=seconds_ago)).isoformat()
    return {"state": "on", "last_updated": t, "context": {"id": context, "user_id": "u-bote"}}


class JournalPlugin(unittest.TestCase):
    def setUp(self):
        NOTES.clear()
        STATES.clear()
        self.laib, self.ha = serve(FakeLaib), serve(FakeHA)
        self.home = tempfile.TemporaryDirectory()
        Path(self.home.name, "personen.json").write_text(json.dumps({"signal:+4900000002": "mama", "telegram:123": "papa"}))
        env = mock.patch.dict(os.environ)  # the other tests in this run see their own environment
        env.start()
        self.addCleanup(env.stop)
        os.environ.update(HERMES_HOME=self.home.name, LAIB_TOKEN="bote-laib-tok", HA_TOKEN="ha-tok",
                          LAIB_MCP_URL="http://127.0.0.1:%d/mcp" % self.laib.server_port, HA_URL="http://127.0.0.1:%d" % self.ha.server_port)
        spec = importlib.util.spec_from_file_location("laib_journal", PLUGIN)
        self.m = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(self.m)
        self.m._spawn = lambda fn, *a: fn(*a)  # the hook hands the note to a thread; here it runs inline
        self.ctx = Ctx()
        self.m.register(self.ctx)

    def tearDown(self):
        for srv in (self.laib, self.ha):
            srv.shutdown()
            srv.server_close()
        self.home.cleanup()

    def pre(self, sender, platform="signal", session="s-1"):
        self.ctx.hooks["pre_llm_call"](session_id=session, turn_id="t", user_message="Bambi an", sender_id=sender, platform=platform, model="m")

    def tool(self, name, result, session="s-1", status="ok", args=None):
        return self.ctx.hooks["post_tool_call"](tool_name=name, args=args or {}, result=result, session_id=session, turn_id="t",
                                                status=status, error_type=None, error_message=None, duration_ms=3)

    def texts(self):
        for auth, method, params in NOTES:
            self.assertEqual((auth, method, params["name"], params["arguments"]["kind"]), ("Bearer bote-laib-tok", "tools/call", "laib_note", "watch"))
        return [params["arguments"]["text"] for _, _, params in NOTES]

    def test_a_switch_from_signal_is_journaled_with_the_person_and_has_context(self):
        STATES["light.bambi"] = state("ctx-1")
        self.pre("+4900000002")
        self.assertIsNone(self.tool("mcp__ha__HassTurnOn", done("light.bambi"), args={"name": "Bambi"}))
        self.assertEqual(self.texts(), ["ha person=mama service=intent.HassTurnOn entity=light.bambi context=ctx-1"])

    def test_the_person_is_the_one_who_wrote_this_turn(self):
        STATES["light.kueche"] = state("ctx-2")
        self.pre("+4900000002", session="s-1")
        self.pre("123", platform="telegram", session="s-2")
        self.tool("mcp__ha__HassTurnOff", done("light.kueche"), session="s-2")
        self.pre("+4900000009", session="s-1")  # someone not in box.yml people
        self.tool("mcp__ha__HassTurnOff", done("light.kueche"), session="s-1")
        self.assertEqual(self.texts(), ["ha person=papa service=intent.HassTurnOff entity=light.kueche context=ctx-2",
                                        "ha person=- service=intent.HassTurnOff entity=light.kueche context=ctx-2"])

    def test_no_sender_is_person_dash_and_an_old_state_is_no_context(self):
        STATES["cover.rollo"] = state("ctx-alt", seconds_ago=3600)  # it was already closed: not this call's context
        self.tool("mcp__ha__HassSetPosition", done("cover.rollo", "cover.tuer"), session="api")
        self.assertEqual(self.texts(), ["ha person=- service=intent.HassSetPosition entity=cover.rollo,cover.tuer context=-"])

    def test_reads_errors_and_nothing_switched_note_nothing(self):
        self.pre("+4900000002")
        self.tool("mcp__ha__GetLiveContext", json.dumps({"result": json.dumps({"success": True, "result": "Bambi: on"})}))
        self.tool("mcp__ha__HassGetState", done("light.bambi", response_type="query_answer"))
        self.tool("mcp__ha__HassTurnOn", done())
        self.tool("mcp__ha__HassTurnOn", json.dumps({"error": "Error calling tool"}), status="error")
        self.tool("mcp__laib__laib_status", "{}")
        self.tool("mcp__automat__automationen", json.dumps({"result": "…"}))
        self.tool("mcp__ha__HassTurnOn", "kein json")
        self.assertEqual(NOTES, [])

    def test_the_automat_switching_an_automation_is_journaled(self):
        STATES["automation.licht_abends"] = state("ctx-3")
        self.pre("+4900000002")
        self.tool("mcp__automat__automation_schalten", json.dumps({"result": "automation.licht_abends: turn_off"}), args={"id": "licht_abends", "an": False})
        self.tool("mcp__automat__automation_schalten", json.dumps({"error": "id: nur a-z"}), status="error", args={"id": "X", "an": True})
        self.assertEqual(self.texts(), ["ha person=mama service=automation.turn_off entity=automation.licht_abends context=ctx-3"])

    def test_a_failed_note_never_fails_the_switch(self):
        os.environ.update(LAIB_MCP_URL="http://127.0.0.1:1/mcp", HA_URL="http://127.0.0.1:1")  # neither answers
        self.pre("+4900000002")
        out = io.StringIO()
        with redirect_stdout(out):
            for _ in range(2):
                self.assertIsNone(self.tool("mcp__ha__HassTurnOn", done("light.bambi")))
        self.assertEqual(out.getvalue().count("[laib-journal]"), 1, out.getvalue())  # said once, not per switch
        self.m._spawn = lambda fn, *a: (_ for _ in ()).throw(RuntimeError("no thread"))
        self.assertIsNone(self.tool("mcp__ha__HassTurnOn", done("light.bambi")))

    def test_no_token_registers_nothing(self):
        os.environ["LAIB_TOKEN"] = ""
        ctx = Ctx()
        self.m.register(ctx)
        self.assertEqual(ctx.hooks, {})


if __name__ == "__main__":
    unittest.main()
