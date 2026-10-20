"""#266, #471: laib-nutzung and laib-persona under Hermes' own plugin loader,
pre_llm_call collector and turn finalizer, not a fake: run with the image's
Python and HERMES_HOME pointing at what post-deploy rendered (config.yaml
enabling both plugins, plugins/laib-*). argv[1]: Hermes' source dir
(/opt/hermes in the image). The CI job runs it in the Hermes image the
template pins (values.example.yml), so a hook Hermes renames shows up here
before counts stop or the persona's screens stop reaching the person."""
import json, logging, os, sys, threading, types
from http.server import BaseHTTPRequestHandler, HTTPServer

SEEN = []


class FakeUsageMetrics(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def do_POST(self):
        SEEN.append((self.path, json.loads(self.rfile.read(int(self.headers["Content-Length"])))))
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b"{}")


class FakePersona(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def do_POST(self):
        req = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        line = req["params"]["arguments"]["eingabe"]
        text = "WOPR\n\n1 SPIELE\n\nJOSHUA>" if line == "Menü" else "WEITER: -\nPROMPT: JOSHUA>"
        body = json.dumps({"jsonrpc": "2.0", "id": req["id"], "result": {"content": [{"type": "text", "text": text}]}}).encode()
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


srv = HTTPServer(("127.0.0.1", 0), FakeUsageMetrics)
threading.Thread(target=srv.serve_forever, daemon=True).start()
os.environ["USAGE_METRICS_URL"] = "http://127.0.0.1:%d" % srv.server_port
psrv = HTTPServer(("127.0.0.1", 0), FakePersona)
threading.Thread(target=psrv.serve_forever, daemon=True).start()
os.environ["PERSONA_MCP_URL"] = "http://127.0.0.1:%d/mcp" % psrv.server_port
sys.path.insert(0, sys.argv[1])

from hermes_cli import plugins  # noqa: E402
from model_tools import _emit_post_tool_call_hook  # noqa: E402  the call site of every tool call
from agent.turn_finalizer import _apply_output_hooks  # noqa: E402  transform_llm_output, then post_llm_call
from agent.turn_context import _collect_pre_llm_call_context  # noqa: E402  pre_llm_call's context into the user turn

plugins.discover_plugins(force=True)
for name in ["mcp__haushalt__kalender", "mcp__persona__eingabe", "mcp__haushalt__kalender"]:
    _emit_post_tool_call_hook(function_name=name, function_args={"q": "Anna"}, result="{}", session_id="s-9", turn_id="t1")


def turn(turn_id, message, answer):
    agent = types.SimpleNamespace(session_id="s-9", model="gemma", platform="signal", _persist_disabled=False)
    return _apply_output_hooks(agent, answer, logging.getLogger("proof"), platform="signal", effective_task_id="task",
                               turn_id=turn_id, original_user_message=message, messages=[])[0]


def pre(turn_id, message):
    agent = types.SimpleNamespace(session_id="s-9", model="gemma", platform="signal", _persist_disabled=False, _user_id="+4917000", _parent_session_id="")
    return _collect_pre_llm_call_context(agent, effective_task_id="task", turn_id=turn_id, original_user_message=message, messages=[], conversation_history=[])


assert pre("t1", "[Anna] Wann kommt der Müll?").startswith("Persona:"), "laib-persona's routing did not reach the user turn"
a = turn("t1", "[Anna] Wann kommt der Müll?", "[unbeantwortet] Das weiß ich nicht.")
pre("t2", "Licht an")
b = turn("t2", "Licht an", "JOSHUA>\nErledigt.")
# #471: a screen turn is the engine's screen, verbatim, whatever the model wrote
assert "Bildschirm" in pre("t3", "Menü")
c = turn("t3", "Menü", "Hier ist das Menü: 1. Spiele, 2. Geräte")
assert c == "```\nWOPR\n\n1 SPIELE\n\nJOSHUA>\n```", c
mod = next((m for m in list(sys.modules.values()) if getattr(m, "MARK", None) == "[unbeantwortet]"), None)
assert mod is not None, "Hermes did not load laib-nutzung (plugins.enabled, plugins/laib-nutzung)"
mod.flush()
for s in SEEN:
    print("sent:", s)
# #510: at the top level the prompt line the model copied is dropped
assert (a, b) == ("Das weiß ich nicht.", "Erledigt."), (a, b)
got = {body["event"]: body["count"] for path, body in SEEN if path == "/ingest"}
assert got == {"werkzeug.haushalt.kalender": 2, "zug": 3, "unbeantwortet": 1}, got
assert [body for path, body in SEEN if path == "/miss"] == [{"source": "bote", "text": "Wann kommt der Müll?", "day": SEEN[0][1]["day"]}], SEEN
assert "Anna" not in json.dumps(SEEN, ensure_ascii=False) and "s-9" not in json.dumps(SEEN)
print("hermes plugin proof ok")
