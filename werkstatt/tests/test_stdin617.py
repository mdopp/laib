"""#617: the Werkstatt's `laib` forwards its stdin, when it is not a TTY,
for every verb whose MCP schema has a `stdin` parameter (calendar
subscribe's address, write's content), read off tools/list, and keeps
--patch for propose. A verb without that parameter gets no stdin."""
import json, os, subprocess, tempfile, threading, unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

HERE = Path(__file__).resolve().parent.parent          # werkstatt/
TOOLS = [
    {"name": "laib_calendar", "description": "c", "inputSchema": {"type": "object", "properties": {"args": {"type": "string"}, "stdin": {"type": "string"}}}},
    {"name": "laib_write", "description": "w", "inputSchema": {"type": "object", "properties": {"args": {"type": "string"}, "stdin": {"type": "string"}}}},
    {"name": "laib_propose", "description": "p", "inputSchema": {"type": "object", "properties": {"args": {"type": "string"}, "patch": {"type": "string"}}}},
    {"name": "laib_status", "description": "s", "inputSchema": {"type": "object", "properties": {"args": {"type": "string"}}}},
]


class FakeLaib:
    def __init__(self):
        self.calls = []
        fake = self

        class H(BaseHTTPRequestHandler):
            def log_message(self, *a): pass

            def do_POST(self):
                req = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
                result = {}
                if req["method"] == "tools/list":
                    result = {"tools": TOOLS}
                elif req["method"] == "tools/call":
                    fake.calls.append((req["params"]["name"], req["params"]["arguments"]))
                    result = {"content": [{"type": "text", "text": "ok"}]}
                body = json.dumps({"jsonrpc": "2.0", "id": req.get("id"), "result": result}).encode()
                self.send_response(200); self.send_header("Content-Length", str(len(body))); self.end_headers(); self.wfile.write(body)

        self.srv = ThreadingHTTPServer(("127.0.0.1", 0), H)
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()
        self.url = f"http://127.0.0.1:{self.srv.server_address[1]}/mcp"


class Stdin(unittest.TestCase):
    def setUp(self):
        self.fake = FakeLaib()
        self.addCleanup(self.fake.srv.shutdown)
        self.tok = tempfile.NamedTemporaryFile("w", delete=False)
        self.tok.write("synthetic-617"); self.tok.close()
        self.addCleanup(os.unlink, self.tok.name)

    def laib(self, *args, stdin=""):
        env = {k: v for k, v in os.environ.items() if not k.startswith("LAIB_")}
        env.update(LAIB_MCP_URL=self.fake.url, LAIB_TOKEN_FILE=self.tok.name, LAIB_RUN_DIR=tempfile.mkdtemp())
        p = subprocess.run(["python3", str(HERE / "laibctl.py"), *args], input=stdin, env=env, capture_output=True, text=True, timeout=30)
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        return self.fake.calls[-1]

    def test_calendar_subscribe_gets_the_address(self):
        name, args = self.laib("calendar", "subscribe", "haus", "muell", "abo", "Müllabfuhr", stdin="https://example.invalid/m.ics")
        self.assertEqual(name, "laib_calendar")
        self.assertEqual(args["stdin"], "https://example.invalid/m.ics")

    def test_write_and_propose_patch_still_work(self):
        self.assertEqual(self.laib("write", "media", "/x", stdin="content")[1]["stdin"], "content")
        args = self.laib("propose", "--patch", "grund", stdin="--- a\n+++ b\n")[1]
        self.assertEqual(args["patch"], "--- a\n+++ b\n")
        self.assertNotIn("stdin", args)

    def test_a_verb_without_stdin_gets_none(self):
        self.assertNotIn("stdin", self.laib("status", stdin="ignored")[1])

    def test_empty_stdin_is_not_sent(self):
        self.assertNotIn("stdin", self.laib("calendar", "list")[1])


if __name__ == "__main__":
    unittest.main()
