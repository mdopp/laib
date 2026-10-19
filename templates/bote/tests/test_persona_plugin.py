"""#471: Hermes wraps every MCP tool result as untrusted data, so the persona
engine's screens and games, given to the model as a tool result, were
rewritten or dropped. The engine now sits in front of the model, as a Hermes
plugin (ADR 0016: Hermes' own extension point, no fork; templates/bote/
persona_plugin.py, placed by post-deploy as plugins/laib-persona):
pre_llm_call asks the engine and puts its routing into the user turn;
transform_llm_output answers a screen turn with the engine's screen,
verbatim, whatever the model wrote. Hermes calls the hooks with keyword
arguments; here they are called the same way against a fake engine."""
import importlib.util, json, os, threading, unittest
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

HERE = Path(__file__).resolve().parent.parent
MENU = "LOGON: JOSHUA\n\nGREETINGS PROFESSOR FALKEN.\n\n1 GERÄTE\n2 SPIELE\n\nJOSHUA>"
GAMES = "FALKEN'S MAZE\nBLACK JACK\nTIC-TAC-TOE\nGLOBAL THERMONUCLEAR WAR\n\nJOSHUA/SPIELE>"
CALLS = []


def answer(line):
    if line in ("Menü", "MENÜ"):
        return MENU
    if line == "LIST GAMES":
        return GAMES
    if line == "zeig mir was du spielen kannst":
        return "UNKNOWN COMMAND.\nVALID: LIST GAMES, PLAY <GAME>, HELP\n\nJOSHUA>"
    if line == "Ist das Licht in der Küche an?":
        return "WEITER: ha\nPROMPT: JOSHUA/GERÄTE>"
    if line == "Wer bist du?":
        return "WEITER: -\nPROMPT: OMA>\nFIGUR: Du bist Oma Erna, warm und geduldig."
    return "WEITER: -\nPROMPT: JOSHUA>"


class FakeEngine(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def do_POST(self):
        req = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        assert req["method"] == "tools/call" and req["params"]["name"] == "persona", req
        args = req["params"]["arguments"]
        CALLS.append(args)
        body = json.dumps({"jsonrpc": "2.0", "id": req["id"], "result": {"content": [{"type": "text", "text": answer(args["eingabe"])}]}}).encode()
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


def load():
    spec = importlib.util.spec_from_file_location("laib_persona", HERE / "persona_plugin.py")
    m = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(m)
    return m


class PersonaPlugin(unittest.TestCase):
    def setUp(self):
        self.srv = HTTPServer(("127.0.0.1", 0), FakeEngine)
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()
        os.environ["PERSONA_MCP_URL"] = "http://127.0.0.1:%d/mcp" % self.srv.server_port
        self.p = load()
        CALLS.clear()

    def tearDown(self):
        self.srv.shutdown()
        self.srv.server_close()
        os.environ.pop("PERSONA_MCP_URL", None)

    def turn(self, message, model_says, turn="t1", sender="+4917012345", platform="signal"):
        ctx = self.p.on_pre(session_id="s1", turn_id=turn, user_message=message, sender_id=sender, platform=platform, is_first_turn=False)
        out = self.p.on_output(response_text=model_says, session_id="s1", turn_id=turn, platform=platform, model="m")
        return ctx, out

    def test_a_screen_reaches_the_person_verbatim(self):
        ctx, out = self.turn("Menü", "Hier ist das Menü: 1. Spiele 2. Geräte")   # what the model made of it before
        self.assertEqual(out, "```\n" + MENU + "\n```")
        self.assertIn("context", ctx)
        self.assertEqual(CALLS, [{"eingabe": "Menü", "sitzung": "+4917012345", "sprache": "de"}])

    def test_the_games_list_is_the_films(self):
        _, out = self.turn("LIST GAMES", "SPIELE-AUSWAHL: 20 Fragen, Wortkette, Zahlenraten")
        self.assertEqual(out, "```\n" + GAMES + "\n```")

    def test_free_words_become_a_command_the_engine_answers(self):
        # the engine asks for one of its commands; the model translates, the engine answers that
        ctx, out = self.turn("zeig mir was du spielen kannst", "LIST GAMES")
        self.assertIn("LIST GAMES, PLAY <GAME>, HELP", ctx["context"])
        self.assertEqual(out, "```\n" + GAMES + "\n```")
        self.assertEqual([c["eingabe"] for c in CALLS], ["zeig mir was du spielen kannst", "LIST GAMES"])

    def test_a_tool_turn_goes_to_the_model_with_its_routing_in_the_user_turn(self):
        ctx, out = self.turn("Ist das Licht in der Küche an?", "Ja, das Küchenlicht ist an.")
        self.assertIn("ha", ctx["context"])
        self.assertIn("JOSHUA/GERÄTE>", ctx["context"])
        self.assertEqual(out, "JOSHUA/GERÄTE>\nJa, das Küchenlicht ist an.")    # the prompt line, then the model's answer
        _, out = self.turn("Ist das Licht in der Küche an?", "JOSHUA/GERÄTE>\nJa.", turn="t2")
        self.assertIsNone(out)                                                   # already there: the answer stays

    def test_a_chat_persona_comes_with_its_figure(self):
        ctx, _ = self.turn("Wer bist du?", "Ich bin Oma Erna.")
        self.assertIn("Oma Erna", ctx["context"])

    def test_without_the_engine_hermes_answers_as_usual(self):
        os.environ["PERSONA_MCP_URL"] = "http://127.0.0.1:9/mcp"
        ctx, out = self.turn("Menü", "Das Menü ist gerade nicht da.")
        self.assertIsNone(ctx)
        self.assertIsNone(out)

    def test_a_scheduled_job_is_no_person(self):
        ctx, out = self.turn("Morgenbericht", "…", platform="cron")
        self.assertIsNone(ctx)
        self.assertIsNone(out)
        self.assertEqual(CALLS, [])

    def test_hermes_prefixes_are_not_the_message(self):
        self.turn("[Anna] Menü", "x")
        self.assertEqual(CALLS[0]["eingabe"], "Menü")


if __name__ == "__main__":
    unittest.main()
