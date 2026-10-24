"""#266, #471, #520, #521, #591: laib-nutzung, laib-persona, laib-ablage and laib-journal under Hermes' own plugin loader,
pre_llm_call collector and turn finalizer, not a fake: run with the image's
Python and HERMES_HOME pointing at what post-deploy rendered (config.yaml
enabling both plugins, plugins/laib-*). argv[1]: Hermes' source dir
(/opt/hermes in the image). The CI job runs it in the Hermes image the
template pins (values.example.yml), so a hook Hermes renames shows up here
before counts stop or the persona's screens stop reaching the person."""
import json, logging, os, sys, threading, types
from http.server import BaseHTTPRequestHandler, HTTPServer

SEEN = []
LINES = []  # what the persona engine was asked


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
        LINES.append(line)
        text = {"Menü": "WOPR\n\n1 SPIELE\n\nJOSHUA>", "Ist das Licht an?": "WEITER: ha\nPROMPT: JOSHUA/GERÄTE>"}.get(line, "WEITER: -\nPROMPT: JOSHUA>")
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
# #591: laib-journal notes who switched in HA to Laib's MCP, the context id from HA's state
NOTES = []
ASKED = []  # what Home Assistant's conversation API was asked


class FakeLaib(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def do_GET(self):  # Home Assistant's /api/states/<id>: changed just now, by this call's context
        import datetime
        body = json.dumps({"state": "on", "last_updated": datetime.datetime.now(datetime.timezone.utc).isoformat(), "context": {"id": "ctx-proof"}}).encode()
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_POST(self):
        req = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        if self.path == "/api/conversation/process":   # #520: HA's own intents, first; HA switches "Bambi an" and "Mach Bambi aus"
            ASKED.append((self.headers.get("Authorization"), req))
            hit = req["text"] in ("Bambi an", "Mach Bambi aus")
            res = ({"response_type": "action_done", "speech": {"plain": {"speech": "Eingeschaltet" if req["text"] == "Bambi an" else "Ausgeschaltet"}},
                    "data": {"targets": [], "success": [{"name": "Bambi", "type": "entity", "id": "light.bambi"}], "failed": []}} if hit else
                   {"response_type": "error", "speech": {"plain": {"speech": "Entschuldigung, das habe ich nicht verstanden"}}, "data": {"code": "no_intent_match"}})
            body = json.dumps({"response": res, "conversation_id": None}).encode()
        else:
            NOTES.append((self.headers.get("Authorization"), req["params"]))
            body = json.dumps({"jsonrpc": "2.0", "id": req["id"], "result": {"content": [{"type": "text", "text": "ok"}]}}).encode()
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


lsrv = HTTPServer(("127.0.0.1", 0), FakeLaib)
threading.Thread(target=lsrv.serve_forever, daemon=True).start()
os.environ.update(LAIB_MCP_URL="http://127.0.0.1:%d/mcp" % lsrv.server_port, HA_URL="http://127.0.0.1:%d" % lsrv.server_port,
                  LAIB_TOKEN="bote-proof", HA_TOKEN="ha-proof")
with open(os.path.join(os.environ["HERMES_HOME"], "personen.json"), "w") as f:
    f.write('{"signal:+4917000": "anna"}')
sys.path.insert(0, sys.argv[1])

from hermes_cli import plugins  # noqa: E402
from model_tools import _emit_post_tool_call_hook  # noqa: E402  the call site of every tool call
from agent.turn_finalizer import _apply_output_hooks  # noqa: E402  transform_llm_output, then post_llm_call
from agent.turn_context import _collect_pre_llm_call_context  # noqa: E402  pre_llm_call's context into the user turn
from agent.prompt_builder import format_steer_marker  # noqa: E402  Hermes' own steer frame, as it writes it (#520)

plugins.discover_plugins(force=True)
for name in ["mcp__haushalt__kalender", "mcp__persona__eingabe", "mcp__haushalt__kalender"]:
    _emit_post_tool_call_hook(function_name=name, function_args={"q": "Anna"}, result="{}", session_id="s-9", turn_id="t1")


def turn(turn_id, message, answer, platform="signal"):
    agent = types.SimpleNamespace(session_id="s-9", model="gemma", platform=platform, _persist_disabled=False)
    return _apply_output_hooks(agent, answer, logging.getLogger("proof"), platform=platform, effective_task_id="task",
                               turn_id=turn_id, original_user_message=message, messages=[])[0]


def pre(turn_id, message, platform="signal", user="+4917000"):
    agent = types.SimpleNamespace(session_id="s-9", model="gemma", platform=platform, _persist_disabled=False, _user_id=user, _parent_session_id="")
    return _collect_pre_llm_call_context(agent, effective_task_id="task", turn_id=turn_id, original_user_message=message, messages=[], conversation_history=[])


# #520: at the top level nothing is routed, so the user turn is the person's words alone
assert pre("t1", "[Anna] Wann kommt der Müll?") == "", "laib-persona added context to a turn with nothing to route"
assert pre("t4", "Ist das Licht an?").startswith("Persona:"), "laib-persona's routing did not reach the user turn"
a = turn("t1", "[Anna] Wann kommt der Müll?", "[unbeantwortet] Das weiß ich nicht.")
pre("t2", "Licht an")
b = turn("t2", "Licht an", "JOSHUA>\nErledigt.")
# #471: a screen turn is the engine's screen, verbatim, whatever the model wrote
assert "Bildschirm" in pre("t3", "Menü")
c = turn("t3", "Menü", "Hier ist das Menü: 1. Spiele, 2. Geräte")
assert c == "```\nWOPR\n\n1 SPIELE\n\nJOSHUA>\n```", c
# #520: Hermes' steer frame, copied by the model around the person's words, never reaches the person
pre("t5", "Bambi aus")
d = turn("t5", "Bambi aus", "Bambi ist aus." + format_steer_marker("Bambi aus"))
assert d == "Bambi ist aus.\n\nBambi aus", repr(d)
mod = next((m for m in list(sys.modules.values()) if getattr(m, "MARK", None) == "[unbeantwortet]"), None)
assert mod is not None, "Hermes did not load laib-nutzung (plugins.enabled, plugins/laib-nutzung)"
mod.flush()
for s in SEEN:
    print("sent:", s)
# #510: at the top level the prompt line the model copied is dropped
assert (a, b) == ("Das weiß ich nicht.", "Erledigt."), (a, b)
got = {body["event"]: body["count"] for path, body in SEEN if path == "/ingest"}
assert got == {"werkzeug.haushalt.kalender": 2, "zug": 4, "unbeantwortet": 1}, got
assert [body for path, body in SEEN if path == "/miss"] == [{"source": "bote", "text": "Wann kommt der Müll?", "day": SEEN[0][1]["day"]}], SEEN
assert "Anna" not in json.dumps(SEEN, ensure_ascii=False) and "s-9" not in json.dumps(SEEN)
# #591: a switch through ha, by the person who wrote this turn, reaches Laib's journal; a read does not
import time  # noqa: E402
pre("t7", "Schalte Bambi bitte ein")
_emit_post_tool_call_hook(function_name="mcp__ha__GetLiveContext", function_args={}, result=json.dumps({"result": '{"success": true, "result": "Bambi: off"}'}), session_id="s-9", turn_id="t7")
done = {"speech": {}, "response_type": "action_done", "data": {"success": [{"name": "Bambi", "type": "entity", "id": "light.bambi"}], "failed": []}}
_emit_post_tool_call_hook(function_name="mcp__ha__HassTurnOn", function_args={"name": "Bambi"}, result=json.dumps({"result": json.dumps(done)}), session_id="s-9", turn_id="t7")
for _ in range(50):
    if NOTES:
        break
    time.sleep(0.1)
time.sleep(0.3)
assert NOTES == [("Bearer bote-proof", {"name": "laib_note", "arguments": {"kind": "watch", "text": "ha person=anna service=intent.HassTurnOn entity=light.bambi context=ctx-proof"}})], NOTES
# #520, ADR 0015: HA's own intents first, on both paths. Signal ("Bambi an" from anna's number) and
# Haus's Sprechen field (Hermes' API server: platform api_server, no sender): HA switches, the person
# reads HA's answer whatever the model wrote, and laib-journal still notes who switched (#591)
NOTES.clear()
ASKED.clear()
assert "Home Assistant" in pre("t8", "Bambi an"), "HA's answer was not announced to the model"
e = turn("t8", "Bambi an", "Bambi an.")
assert e == "Eingeschaltet", repr(e)
assert "Home Assistant" in pre("t9", "[Haus] Mach Bambi aus", platform="api_server", user="")
f = turn("t9", "[Haus] Mach Bambi aus", "OK", platform="api_server")
assert f == "Ausgeschaltet", repr(f)
assert [(a, q["text"], q["agent_id"], q["language"]) for a, q in ASKED] == [
    ("Bearer ha-proof", "Bambi an", "conversation.home_assistant", "de"), ("Bearer ha-proof", "Mach Bambi aus", "conversation.home_assistant", "de")], ASKED
for _ in range(50):
    if len(NOTES) == 2:
        break
    time.sleep(0.1)
assert sorted(n[1]["arguments"]["text"] for n in NOTES) == [
    "ha person=- service=conversation.process entity=light.bambi context=ctx-proof",
    "ha person=anna service=conversation.process entity=light.bambi context=ctx-proof"], NOTES
# what HA did not understand (no_intent_match) is the model's turn, as before
assert pre("t10", "Erzähl mir einen Witz über Bambi", platform="api_server", user="") == ""
g = turn("t10", "Erzähl mir einen Witz über Bambi", "Ein Reh kommt in eine Bar.", platform="api_server")
assert g == "Ein Reh kommt in eine Bar.", repr(g)
# #521: laib-ablage under Hermes' own gateway hooks: pre_gateway_dispatch marks a PDF sent with
# "ablegen", post_gateway_admission (after Hermes' sender check) files it and answers, so a hook
# Hermes renames or a payload it narrows shows up here, not as documents that never arrive.
import asyncio  # noqa: E402
from gateway.config import Platform  # noqa: E402
from gateway.platforms.event import MessageEvent  # noqa: E402
from gateway.run_inbound import GatewayInboundMixin  # noqa: E402
from gateway.run_inbound_consumer import run_post_admission_hook  # noqa: E402
from gateway.session import SessionSource  # noqa: E402

FILED = []


class FakeAblage(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def do_POST(self):
        FILED.append((self.path, self.headers.get("Authorization"), json.loads(self.rfile.read(int(self.headers["Content-Length"])))))
        body = json.dumps({"ok": True, "titel": "Stromrechnung"}).encode()
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


asrv = HTTPServer(("127.0.0.1", 0), FakeAblage)
threading.Thread(target=asrv.serve_forever, daemon=True).start()
os.environ["ABLAGE_URL"] = "http://127.0.0.1:%d/ablage" % asrv.server_port
os.environ["NOTIFY_TOKEN"] = "notify-proof"
pdf = os.path.join(os.environ["HERMES_HOME"], "doc_proof.pdf")
with open(pdf, "wb") as f:
    f.write(b"%PDF-1.7 proof")
source = SessionSource(platform=Platform.SIGNAL, chat_id="+4917000", user_id="+4917000", user_name="Anna")


async def ablegen(text):
    event = MessageEvent(text=text, source=source, media_urls=[pdf], media_types=["application/pdf"])
    event = await GatewayInboundMixin._hm_pre_gateway_dispatch_hook(types.SimpleNamespace(), event, source)
    return await run_post_admission_hook(types.SimpleNamespace(), event, source, "proof-key")


assert asyncio.run(ablegen("Was steht da drin?")) == (False, None), "laib-ablage took a message without the word"
assert not FILED
handled, reply = asyncio.run(ablegen("Stromrechnung ablegen"))
assert handled and reply.startswith("Liegt in Paperless, Titel „Stromrechnung“"), (handled, reply)
assert [(p, a, b["titel"], b["absender"]) for p, a, b in FILED] == [("/ablage", "Bearer notify-proof", "Stromrechnung", "+4917000")], FILED
# #587: a reply carries the quoted message in front, as Hermes writes it; the engine gets the person's
# words only (the quote's ABSENDER read as ENDE, and the Bote said AUF WIEDERSEHEN)
quoted = GatewayInboundMixin._prepend_inbound_reply_context(
    types.SimpleNamespace(reply_to_text="Laib hat verworfen.\n\nDamit unbekannte Absender …", reply_to_message_id="1", reply_to_is_own_message=True),
    source, "Dir sollte die Nummer bekannt sein")
assert quoted.startswith("[Replying to"), quoted
LINES.clear()
pre("t6", quoted)
assert LINES == ["Dir sollte die Nummer bekannt sein"], LINES
# #586: Hermes' notice to the owner about a dropped sender is Laib's text (pre-start's locales overlay)
from gateway.run_inbound_unauthorized import unauthorized_owner_hint  # noqa: E402
hint = unauthorized_owner_hint("signal", "+4900000002", "Anna", hermes_home="/opt/data")
assert "box.yml people" in hint and "Wer ist das?" in hint and "+4900000002" in hint and ".env" not in hint and "hermes pairing" not in hint, hint
# #587: no generated get_prompt / list_prompts for ha, whose prompt names the model guessed
import hermes_yaml  # noqa: E402
from tools.mcp_tool_registration import _select_utility_schemas  # noqa: E402
conf = hermes_yaml.safe_load(open(os.path.join(os.environ["HERMES_HOME"], "config.yaml")))
both = types.SimpleNamespace(initialize_result=types.SimpleNamespace(capabilities=types.SimpleNamespace(prompts=object(), resources=object())), session=None)
assert _select_utility_schemas("ha", both, conf["mcp_servers"]["ha"]) == [], conf["mcp_servers"]["ha"]
assert _select_utility_schemas("laib", both, conf["mcp_servers"]["laib"]), "the check would pass with utilities off everywhere"
print("hermes plugin proof ok")
