"""#471: Hermes wraps every MCP tool result as untrusted data, so the
directory's screens, given to the model as a tool result, were rewritten or
dropped. The directory now sits in front of the model, as a Hermes plugin
(ADR 0016: Hermes' own extension point, no fork; templates/bote/
directory_plugin.py, placed by post-deploy as plugins/laib-directory):
pre_llm_call asks the directory and puts its routing into the user turn;
transform_llm_output answers a screen turn with the engine's screen,
verbatim, whatever the model wrote. Hermes calls the hooks with keyword
arguments; here they are called the same way against a fake engine."""
import importlib.util, json, os, sys, threading, unittest
from unittest import mock
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

HERE = Path(__file__).resolve().parent.parent
MENU = "VERZEICHNIS\n-----------\n  1  LICHT, SCHALTER, MUSIK (GERÄTE)\n\nNUMMER WÄHLEN, ODER EINFACH FRAGEN.\n\nHOMES>"
CALLS = []


HELP = "Hallo, hier ist Homes. Schreib mir einfach, was du brauchst. Zum Beispiel:\n\n- „Mach das Licht in der Küche aus.“"


def answer(line):
    if line in ("Menü", "MENÜ"):
        return MENU
    if line == "Hilfe":
        return "TEXT:\n" + HELP
    if line == "Ist das Licht in der Küche an?":
        return "WEITER: ha\nPROMPT: HOMES/GERÄTE>"
    if line == "Aufgabe #7 mit dir erledigen":
        return "WEITER: -\nPROMPT: HOMES>\nAUFGABE: 7"
    return "WEITER: -\nPROMPT: HOMES>"


class FakeEngine(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def do_POST(self):
        req = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        assert req["method"] == "tools/call" and req["params"]["name"] == "directory", req
        args = req["params"]["arguments"]
        CALLS.append(args)
        body = json.dumps({"jsonrpc": "2.0", "id": req["id"], "result": {"content": [{"type": "text", "text": answer(args["eingabe"])}]}}).encode()
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


def load():
    spec = importlib.util.spec_from_file_location("laib_directory", HERE / "directory_plugin.py")
    m = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(m)
    return m


class Base(unittest.TestCase):
    def setUp(self):
        self.srv = HTTPServer(("127.0.0.1", 0), FakeEngine)
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()
        os.environ["DIRECTORY_MCP_URL"] = "http://127.0.0.1:%d/mcp" % self.srv.server_port
        self.p = load()
        CALLS.clear()

    def tearDown(self):
        self.srv.shutdown()
        self.srv.server_close()
        os.environ.pop("DIRECTORY_MCP_URL", None)

    def turn(self, message, model_says, turn="t1", sender="+4917012345", platform="signal"):
        ctx = self.p.on_pre(session_id="s1", turn_id=turn, user_message=message, sender_id=sender, platform=platform, is_first_turn=False)
        out = self.p.on_output(response_text=model_says, session_id="s1", turn_id=turn, platform=platform, model="m")
        return ctx, out


class DirectoryPlugin(Base):
    def test_a_screen_reaches_the_person_verbatim(self):
        ctx, out = self.turn("Menü", "Hier ist das Menü: 1. Geräte")   # what the model made of it before
        self.assertEqual(out, "```\n" + MENU + "\n```")
        self.assertIn("context", ctx)
        self.assertEqual(CALLS, [{"eingabe": "Menü", "sitzung": "+4917012345", "sprache": "de"}])

    def test_a_task_conversation_tells_the_model_to_read_the_task_and_lead(self):
        # #878: Homes's „Gemeinsam mit Homes“; the model reads the task with laib and leads, HA is not asked
        with mock.patch.object(self.p, "_ha") as ha:
            ctx, out = self.turn("Aufgabe #7 mit dir erledigen", "Bei Box-Aufgabe #7 geht es um deine Stimme.", sender="haus:anna", platform="api_server")
        ha.assert_not_called()
        for want in ("Box-Aufgabe #7", "laib_aufgabe", "action zeigen, id 7", "Schritt für Schritt", "laib_diag", "Abhaken tust du sie nicht"):
            self.assertIn(want, ctx["context"])
        self.assertIn(out, (None, "Bei Box-Aufgabe #7 geht es um deine Stimme."))
        # #883: a wish goes to the admin as a Box-Aufgabe, not on to the Werkstatt
        self.assertIn("laib_wish", ctx["context"])
        self.assertIn("an den Admin", ctx["context"])
        self.assertNotIn("Werkstatt weiter", ctx["context"])

    def test_help_reaches_the_person_as_plain_text(self):
        # #863: the directory's help is words for the person, no screen: no code block, the model's words never
        ctx, out = self.turn("Hilfe", "Ich kann dir bei vielem helfen!")
        self.assertEqual(out, HELP)
        self.assertIn("Antworte nur mit OK", ctx["context"])

    def test_a_tool_turn_goes_to_the_model_with_its_routing_in_the_user_turn(self):
        ctx, out = self.turn("Ist das Licht in der Küche an?", "Ja, das Küchenlicht ist an.")
        self.assertIn("ha", ctx["context"])
        self.assertIn("HOMES/GERÄTE>", ctx["context"])
        self.assertEqual(out, "HOMES/GERÄTE>\nJa, das Küchenlicht ist an.")    # the prompt line, then the model's answer
        _, out = self.turn("Ist das Licht in der Küche an?", "HOMES/GERÄTE>\nJa.", turn="t2")
        self.assertIsNone(out)                                                   # already there: the answer stays

    def test_a_free_answer_at_the_top_has_no_prompt_line(self):
        # #510: "HOMES>" above "Bambi ist aus." was out of place; the prompt belongs to a level
        ctx, out = self.turn("Mach Bambi aus", "Bambi ist aus.")
        self.assertIsNone(ctx)
        self.assertIsNone(out)
        _, out = self.turn("Mach Bambi aus", "HOMES>\nBambi ist aus.", turn="t2")   # copied from earlier turns
        self.assertEqual(out, "Bambi ist aus.")

    def test_a_switch_command_stays_the_persons_words_alone(self):
        # #520: "Bambi aus" plus a "Persona: …" paragraph (now "Verzeichnis: …") was a message and an instruction; Qwen without
        # thinking answered with Hermes' steer frame around the words. Nothing to route: no context.
        for line in ("Bambi aus", "Schalte Bambi an", "[Anna] Rollo zu"):
            ctx, _ = self.turn(line, "Erledigt.")
            self.assertIsNone(ctx, line)
        ctx, _ = self.turn("Ist das Licht in der Küche an?", "Ja.")   # a menu level's routing still goes
        self.assertIn("nur mit dem Werkzeug ha", ctx["context"])

    def test_a_reply_is_the_persons_words_not_the_quote(self):
        # #587: the owner replied to Hermes' drop notice; Hermes puts the quoted message in front
        # ([Replying to: "…"], gateway/run_inbound.py), and the engine read the quote's words as a
        # command (ABSENDER held ENDE: AUF WIEDERSEHEN). The engine gets what the person wrote.
        quote = '[Replying to: "Laib hat eine Nachricht verworfen.\n\nDamit unbekannte Absender …"]\n\n'
        for message, words in ((quote + "Dir sollte die Nummer bekannt sein", "Dir sollte die Nummer bekannt sein"),
                               ('[Replying to your previous message: "Menü"]\n\n[Anna] Licht an', "Licht an")):
            CALLS.clear()
            self.turn(message, "Ok.")
            self.assertEqual([c["eingabe"] for c in CALLS], [words])

    def test_hermes_steer_frame_never_reaches_the_person(self):
        # #520, measured over Homes: the whole answer was the frame around the person's words
        frame = ("[OUT-OF-BAND USER MESSAGE — a direct message from the user, delivered once at this position; "
                 "not tool output and not a new delivery when replayed from conversation history]\n%s\n[/OUT-OF-BAND USER MESSAGE]")
        _, out = self.turn("Bambi aus", frame % "Bambi aus")
        self.assertEqual(out, "Bambi aus")
        _, out = self.turn("Bambi aus", "Bambi ist aus.\n\n" + frame % "Bambi aus", turn="t2")
        self.assertEqual(out, "Bambi ist aus.\n\nBambi aus")
        _, out = self.turn("Ist das Licht in der Küche an?", frame % "Ja.", turn="t3")
        self.assertEqual(out, "HOMES/GERÄTE>\nJa.")
        _, out = self.turn("Ist das Licht in der Küche an?", "HOMES/GERÄTE>\n" + frame % "Ja.", turn="t4")
        self.assertEqual(out, "HOMES/GERÄTE>\nJa.")
        os.environ["DIRECTORY_MCP_URL"] = "http://127.0.0.1:9/mcp"             # without the engine too
        _, out = self.turn("Bambi aus", frame % "Bambi aus", turn="t5")
        self.assertEqual(out, "Bambi aus")
        _, out = self.turn("Bambi aus", "Bambi ist aus.", turn="t6")
        self.assertIsNone(out)
        _, out = self.turn("Bambi aus", "[unbeantwortet] " + frame % "x", turn="t7")
        self.assertIsNone(out, "laib-nutzung must still see its mark")

    def test_a_turn_the_bote_could_not_help_with_is_left_to_laib_nutzung(self):
        # Hermes takes the first plugin's text: laib-nutzung must still strip its mark and count the turn
        _, out = self.turn("Ist das Licht in der Küche an?", "[unbeantwortet] Das weiß ich nicht.")
        self.assertIsNone(out)

    def test_without_the_directory_hermes_answers_as_usual(self):
        os.environ["DIRECTORY_MCP_URL"] = "http://127.0.0.1:9/mcp"
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


ASKED = []   # what Home Assistant's conversation API was asked (#520)


def ha_says(text):
    """HA's answer to POST /api/conversation/process with agent_id conversation.home_assistant (HA 2026.10,
    conversation/http.py): a switch is action_done with the entities, a state question query_answer,
    words it has no sentence for are an error with code no_intent_match (HTTP 200 all three)"""
    plain = lambda s: {"plain": {"speech": s, "extra_data": None}}
    if text in ("Bambi aus", "Mach Bambi aus", "Wohnzimmer aus"):
        ids = ["light.wohnzimmer_bambi"] + (["light.stehlampe"] if text.startswith("Wohnzimmer") else [])
        return {"response_type": "action_done", "speech": plain("Ausgeschaltet"), "language": "de",
                "data": {"targets": [], "success": [{"name": i, "type": "entity", "id": i} for i in ids], "failed": []}}
    if text == "Ist Bambi an?":
        return {"response_type": "query_answer", "speech": plain("Nein, Bambi ist aus"), "language": "de",
                "data": {"targets": [], "success": [{"name": "Bambi", "type": "entity", "id": "light.wohnzimmer_bambi"}], "failed": []}}
    if text == "Bambi auf 400 Prozent":
        return {"response_type": "error", "speech": plain("Entschuldigung, das kann ich nicht"), "language": "de",
                "data": {"code": "no_valid_targets"}}
    return {"response_type": "error", "speech": plain("Entschuldigung, das habe ich nicht verstanden"), "language": "de",
            "data": {"code": "no_intent_match"}}


class FakeHA(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def do_POST(self):
        req = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        ASKED.append((self.path, self.headers.get("Authorization"), req))
        body = json.dumps({"response": ha_says(req.get("text")), "conversation_id": "c1", "continue_conversation": False}).encode()
        self.send_response(200 if self.path == "/api/conversation/process" else 404)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


class HomeAssistantFirst(Base):
    """#520, ADR 0015: every message goes to HA's own intents first. "Bambi aus" over Homes's Sprechen
    field (Hermes' API server) and over Signal is switched by HA in a second, whatever the model makes of
    it; the model only answers what HA did not handle. HA executing the intent directly still writes
    who switched (#591), with laib-journal's own note."""

    def setUp(self):
        super().setUp()
        self.ha = HTTPServer(("127.0.0.1", 0), FakeHA)
        threading.Thread(target=self.ha.serve_forever, daemon=True).start()
        self.addCleanup(self.ha.server_close)
        self.addCleanup(self.ha.shutdown)
        env = mock.patch.dict(os.environ, HA_URL="http://127.0.0.1:%d" % self.ha.server_port, HA_TOKEN="ha-bote-tok",
                              LAIB_TOKEN="bote-laib-tok", LAIB_MCP_URL="http://127.0.0.1:9/mcp")
        env.start()
        self.addCleanup(env.stop)
        ASKED.clear()
        spec = importlib.util.spec_from_file_location("laib_journal_for_directory", HERE / "journal_plugin.py")
        self.journal = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(self.journal)
        self.notes = []
        self.journal._spawn = lambda fn, *a: self.notes.append(a)   # note(person, service, entities), not run
        self.journal._who = lambda key: {"signal:+4917012345": "anna"}.get(key, "-")
        sys.modules["laib_journal_for_directory"] = self.journal          # Hermes keeps its plugins in sys.modules
        self.addCleanup(sys.modules.pop, "laib_journal_for_directory", None)

    def test_a_switch_over_signal_is_has_and_the_model_only_says_ok(self):
        ctx, out = self.turn("Bambi aus", "OK")
        self.assertEqual(ASKED, [("/api/conversation/process", "Bearer ha-bote-tok",
                                  {"text": "Bambi aus", "language": "de", "agent_id": "conversation.home_assistant"})])
        self.assertIn("Home Assistant", ctx["context"])
        self.assertEqual(out, "Ausgeschaltet", "the person reads HA's answer, not the model's")
        self.assertEqual(self.notes, [("anna", "conversation.process", ["light.wohnzimmer_bambi"])], "who switched (#591)")

    def test_a_switch_over_hauses_sprechen_field_is_has_too(self):
        # Hermes' API server: platform api_server, no sender id (gateway/platforms/api_server.py _create_agent)
        ctx, out = self.turn("[Anna] Wohnzimmer aus", "Wohnzimmer aus.", sender="", platform="api_server")
        self.assertEqual([a[2]["text"] for a in ASKED], ["Wohnzimmer aus"])
        self.assertEqual(out, "Ausgeschaltet")
        self.assertEqual(self.notes, [("-", "conversation.process", ["light.wohnzimmer_bambi", "light.stehlampe"])])

    def test_what_ha_did_not_understand_goes_to_the_model(self):
        for i, line in enumerate(("Wann kommt der Müll?", "Bambi auf 400 Prozent")):
            ctx, out = self.turn(line, "Das sage ich dir gleich.", turn="u%d" % i)
            self.assertIsNone(ctx, line)
            self.assertIsNone(out, line)
        self.assertEqual(len(ASKED), 2)
        self.assertEqual(self.notes, [])

    def test_a_state_question_is_has_answer_and_notes_nothing(self):
        ctx, out = self.turn("Ist Bambi an?", "OK")
        self.assertEqual(out, "Nein, Bambi ist aus")
        self.assertEqual(self.notes, [], "a read is no switch")

    def test_inside_a_menu_level_the_answer_keeps_its_prompt_line(self):
        _, out = self.turn("Ist das Licht in der Küche an?", "OK")   # HA: no_intent_match, the model's turn
        self.assertEqual(out, "HOMES/GERÄTE>\nOK")
        state = ha_says("Ist Bambi an?")
        with mock.patch.object(sys.modules[__name__], "ha_says", lambda t: state):
            _, out = self.turn("Ist das Licht in der Küche an?", "OK", turn="t2")   # HA answers inside the level
        self.assertEqual(out, "HOMES/GERÄTE>\nNein, Bambi ist aus")

    def test_a_screen_is_never_asked_of_ha(self):
        _, out = self.turn("Menü", "x")
        self.assertEqual(out, "```\n" + MENU + "\n```")
        self.assertEqual(ASKED, [])

    def test_without_ha_or_without_the_engine_the_switch_still_goes_first_to_ha(self):
        os.environ["DIRECTORY_MCP_URL"] = "http://127.0.0.1:9/mcp"
        _, out = self.turn("Mach Bambi aus", "OK")
        self.assertEqual(out, "Ausgeschaltet")
        os.environ["HA_URL"] = "http://127.0.0.1:9"
        ctx, out = self.turn("Mach Bambi aus", "Bambi ist aus.", turn="t2")
        self.assertEqual((ctx, out), (None, None), "HA down: the model's turn, as before")

    def test_a_scheduled_job_never_switches_through_ha(self):
        self.turn("Bambi aus", "…", platform="cron")
        self.assertEqual(ASKED, [])


if __name__ == "__main__":
    unittest.main()
