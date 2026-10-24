"""#520: the switch test set against a fake Hermes API server that streams the way
v0.22.2 does (gateway/platforms/api_server_openai_routes.py _write_sse_chat_completion:
chat.completion.chunk frames, hermes.tool.progress events, data: [DONE])."""
import importlib.util, io, json, os, tempfile, threading, unittest
from contextlib import redirect_stdout
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

spec = importlib.util.spec_from_file_location("schalttest", Path(__file__).with_name("bote-schalttest.py"))
st = importlib.util.module_from_spec(spec)
spec.loader.exec_module(st)
KEY = "k-bote-api-0123456789"
SEEN = []
FRAME = ("[OUT-OF-BAND USER MESSAGE — a direct message from the user, delivered once at this position; "
         "not tool output and not a new delivery when replayed from conversation history]\n%s\n[/OUT-OF-BAND USER MESSAGE]")


class FakeHermes(BaseHTTPRequestHandler):
    mode = "ok"

    def log_message(self, *a):
        pass

    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        SEEN.append((self.path, dict(self.headers), body))
        if self.headers.get("Authorization") != "Bearer " + KEY:
            self.send_response(401)
            self.end_headers()
            return
        said = body["messages"][-1]["content"]
        frames = []
        if self.mode == "ha":   # HA switched it in front of the model: no tool, HA's answer
            ha_switches(said)
            reply = "Erledigt"
        elif self.mode == "ok" or (self.mode == "echo-short" and len(said.split()) > 2):
            frames.append(("hermes.tool.progress", {"tool": "mcp__ha__HassTurnOff", "toolCallId": "c1", "status": "running"}))
            frames.append(("hermes.tool.progress", {"tool": "mcp__ha__HassTurnOff", "toolCallId": "c1", "status": "completed"}))
            reply = "Erledigt, %s." % said
        elif self.mode == "frame":
            reply = FRAME % said
        else:
            reply = said + "."
        frames += [("", {"choices": [{"index": 0, "delta": {"content": reply[:5]}}]}),
                   ("", {"choices": [{"index": 0, "delta": {"content": reply[5:]}}]})]
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.end_headers()
        for event, data in frames:
            self.wfile.write(((("event: %s\n" % event) if event else "") + "data: %s\n\n" % json.dumps(data)).encode())
        self.wfile.write(b"data: [DONE]\n\n")


LIGHT = {}   # the fake Home Assistant's light "Bambi" (#520: HA switches it itself, no tool runs in Hermes)


class FakeHA(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def do_GET(self):
        if self.headers.get("Authorization") != "Bearer ha-tok":
            self.send_response(401)
            self.end_headers()
            return
        states = [{"entity_id": "light.wohnzimmer_bambi", "state": LIGHT["state"], "attributes": {"friendly_name": "Bambi", "brightness": LIGHT.get("brightness")}},
                  {"entity_id": "sensor.bambi_akku", "state": "80", "attributes": {"friendly_name": "Bambi"}}]
        body = json.dumps(states if self.path == "/api/states" else next((x for x in states if "/api/states/" + x["entity_id"] == self.path), {})).encode()
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


def ha_switches(said):
    """what HA's own intent does with the sentence (the persona plugin sent it there first)"""
    s = said.lower()
    if "prozent" in s:
        pct = int(s.split()[-2])
        LIGHT.update(state="on", brightness=round(pct * 255 / 100))
    elif s.endswith(" an"):
        LIGHT.update(state="on", brightness=255)
    elif s.endswith(" aus"):
        LIGHT.update(state="off", brightness=None)


class SwitchTest(unittest.TestCase):
    def setUp(self):
        self.srv = HTTPServer(("127.0.0.1", 0), FakeHermes)
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()
        self.addCleanup(self.srv.server_close)
        self.addCleanup(self.srv.shutdown)
        d = tempfile.mkdtemp()
        self.env = Path(d, ".env")
        self.env.write_text("SIGNAL_ACCOUNT=+490\nAPI_SERVER_KEY=%s\n" % KEY)
        SEEN.clear()

    def run_it(self, mode, *extra):
        FakeHermes.mode = mode
        out = io.StringIO()
        with redirect_stdout(out):
            rc = st.main(["--key-file", str(self.env), "--url", "http://127.0.0.1:%d/v1" % self.srv.server_port, "--licht", "Bambi", *extra])
        return rc, out.getvalue()

    def test_every_command_switched(self):
        rc, out = self.run_it("ok", "--jalousie", "Rollo")
        self.assertEqual(rc, 0, out)
        self.assertIn("10 von 10 Befehlen geschaltet", out)
        self.assertEqual([b["messages"][-1]["content"] for _, _, b in SEEN][:2], ["Bambi an", "Bambi aus"])
        sessions = {h.get("X-Hermes-Session-Id") for _, h, _ in SEEN}
        self.assertEqual(len(sessions), len(SEEN), "each sentence needs its own session")
        self.assertTrue(all(p == "/v1/chat/completions" and b["stream"] for p, _, b in SEEN))

    def test_ha_switching_in_front_of_the_model_counts_by_the_lights_state(self):
        # #520, ADR 0015: the persona plugin hands "Bambi aus" to HA's own intents first. No Hermes tool
        # runs, so with HA's token in the key file the case is measured where it matters: at the light.
        ha = HTTPServer(("127.0.0.1", 0), FakeHA)
        threading.Thread(target=ha.serve_forever, daemon=True).start()
        self.addCleanup(ha.server_close)
        self.addCleanup(ha.shutdown)
        self.env.write_text("API_SERVER_KEY=%s\nHA_TOKEN=ha-tok\nHA_URL=http://host.containers.internal:8123\n" % KEY)
        LIGHT.update(state="off", brightness=None)
        url = "http://127.0.0.1:%d" % ha.server_port
        rc, out = self.run_it("ha", "--ha-url", url)
        self.assertEqual(rc, 0, out)
        self.assertIn("6 von 6 Befehlen geschaltet", out)
        self.assertIn("HA", out)
        LIGHT.update(state="on", brightness=255)
        rc, out = self.run_it("echo", "--ha-url", url, "--wait", "0.2")   # nothing switched, nothing ran
        self.assertEqual(rc, 1, out)
        self.assertRegex(out, r"FEHLT .*Bambi aus .*Bambi ist on, nicht off")

    def test_an_echo_is_no_switch(self):
        # #520: "Bambi aus" came back as "Bambi aus." and nothing switched
        rc, out = self.run_it("echo-short")
        self.assertEqual(rc, 1, out)
        self.assertRegex(out, r"FEHLT .*Bambi aus .*kein ha-Werkzeug")
        self.assertRegex(out, r"OK .*Schalte bitte Bambi an")

    def test_the_steer_frame_fails_the_case(self):
        rc, out = self.run_it("frame")
        self.assertEqual(rc, 1, out)
        self.assertIn("Steuer-Rahmen im Text", out)

    def test_an_api_that_does_not_answer_is_not_measured(self):
        FakeHermes.mode = "ok"
        out = io.StringIO()
        with redirect_stdout(out):
            rc = st.main(["--key-file", str(self.env), "--url", "http://127.0.0.1:9/v1", "--licht", "Bambi"])
        self.assertEqual(rc, 2)
        self.assertIn("NICHT GEMESSEN", out.getvalue())

    def test_the_key_comes_from_a_file_and_list_prints_the_sentences(self):
        self.assertEqual(st.read_key(str(self.env)), KEY)
        bare = Path(self.env.parent, "key")
        bare.write_text(KEY + "\n")
        self.assertEqual(st.read_key(str(bare)), KEY)
        out = io.StringIO()
        with redirect_stdout(out):
            self.assertEqual(st.main(["--licht", "Bambi", "--jalousie", "Rollo", "--list"]), 0)
        lines = out.getvalue().splitlines()
        self.assertEqual(lines[:2], ["Bambi an", "Bambi aus"])
        self.assertIn("Rollo zu", lines)


if __name__ == "__main__":
    unittest.main()
