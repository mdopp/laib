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
        if self.mode == "ok" or (self.mode == "echo-short" and len(said.split()) > 2):
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
