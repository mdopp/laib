"""#266: laib-nutzung under Hermes' own plugin loader and turn finalizer, not
a fake: run with the stock image's Python and HERMES_HOME pointing at what
post-deploy rendered (config.yaml enabling the plugin, plugins/laib-nutzung).
argv[1]: Hermes' source dir (/opt/hermes in the image). The CI job runs it
in nousresearch/hermes-agent:latest, the image the box auto-updates to, so a
hook Hermes renames shows up here before counts silently stop."""
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


srv = HTTPServer(("127.0.0.1", 0), FakeUsageMetrics)
threading.Thread(target=srv.serve_forever, daemon=True).start()
os.environ["USAGE_METRICS_URL"] = "http://127.0.0.1:%d" % srv.server_port
sys.path.insert(0, sys.argv[1])

from hermes_cli import plugins  # noqa: E402
from model_tools import _emit_post_tool_call_hook  # noqa: E402  the call site of every tool call
from agent.turn_finalizer import _apply_output_hooks  # noqa: E402  transform_llm_output, then post_llm_call

plugins.discover_plugins(force=True)
for name in ["mcp__haushalt__kalender", "mcp__persona__eingabe", "mcp__haushalt__kalender"]:
    _emit_post_tool_call_hook(function_name=name, function_args={"q": "Anna"}, result="{}", session_id="s-9", turn_id="t1")


def turn(turn_id, message, answer):
    agent = types.SimpleNamespace(session_id="s-9", model="gemma", platform="signal", _persist_disabled=False)
    return _apply_output_hooks(agent, answer, logging.getLogger("proof"), platform="signal", effective_task_id="task",
                               turn_id=turn_id, original_user_message=message, messages=[])[0]


a = turn("t1", "[Anna] Wann kommt der Müll?", "[unbeantwortet] Das weiß ich nicht.")
b = turn("t2", "Licht an", "Erledigt.")
mod = next((m for m in list(sys.modules.values()) if getattr(m, "MARK", None) == "[unbeantwortet]"), None)
assert mod is not None, "Hermes did not load laib-nutzung (plugins.enabled, plugins/laib-nutzung)"
mod.flush()
for s in SEEN:
    print("sent:", s)
assert (a, b) == ("Das weiß ich nicht.", "Erledigt."), (a, b)
got = {body["event"]: body["count"] for path, body in SEEN if path == "/ingest"}
assert got == {"werkzeug.haushalt.kalender": 2, "zug": 2, "unbeantwortet": 1}, got
assert [body for path, body in SEEN if path == "/miss"] == [{"source": "bote", "text": "Wann kommt der Müll?", "day": SEEN[0][1]["day"]}], SEEN
assert "Anna" not in json.dumps(SEEN, ensure_ascii=False) and "s-9" not in json.dumps(SEEN)
print("hermes plugin proof ok")
