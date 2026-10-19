"""#380: the Werkstatt's Claude gets a short token per session. The
container holds only the werkstatt hand's token; `laib --headers` (Claude
Code's headersHelper for the laib MCP server) and `laib` itself ask Laib
for a claude token per session (laib_session <name>), keep it in the
container's tmpfs and ask again when it runs out. The kernel side is
cmd/laib/session380_test.go; the real path through laib serve is
scripts/werkstatt-app.sh."""
import json, os, re, stat, subprocess, tempfile, threading, time, unittest
from datetime import datetime, timedelta, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

HERE = Path(__file__).resolve().parent.parent          # werkstatt/
TPL = HERE.parent / "templates" / "werkstatt"


class FakeLaib:
    """laib serve's /mcp: laib_session mints for the werkstatt token only"""

    def __init__(self, ttl=timedelta(hours=8)):
        self.mints, self.calls, self.ttl, n = [], [], ttl, [0]
        fake = self

        class H(BaseHTTPRequestHandler):
            def log_message(self, *a): pass

            def do_POST(self):
                req = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
                bearer = self.headers.get("Authorization", "")
                result = {}
                if req["method"] == "tools/call":
                    name, args = req["params"]["name"], req["params"]["arguments"].get("args", "")
                    fake.calls.append((bearer, name, args))
                    if name == "laib_session" and bearer == "Bearer werkstatt-380":
                        n[0] += 1
                        tok = f"laib_claude_s{n[0]}"
                        fake.mints.append(args)
                        exp = (datetime.now(timezone.utc) + fake.ttl).isoformat()
                        result = {"content": [{"type": "text", "text": json.dumps({"token": tok, "hand": "claude", "expires": exp})}]}
                    elif name == "laib_session":
                        result = {"content": [{"type": "text", "text": "refused"}], "isError": True}
                    else:
                        result = {"content": [{"type": "text", "text": "as " + bearer}]}
                body = json.dumps({"jsonrpc": "2.0", "id": req.get("id"), "result": result}).encode()
                self.send_response(200); self.send_header("Content-Length", str(len(body))); self.end_headers(); self.wfile.write(body)

        self.srv = ThreadingHTTPServer(("127.0.0.1", 0), H)
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()
        self.url = f"http://127.0.0.1:{self.srv.server_address[1]}/mcp"


class SessionToken(unittest.TestCase):
    def setUp(self):
        self.fake = FakeLaib()
        self.addCleanup(self.fake.srv.shutdown)
        self.run_dir = Path(tempfile.mkdtemp())
        (self.run_dir / "werkstatt-token").write_text("werkstatt-380")

    def laib(self, *args, session=None):
        env = {k: v for k, v in os.environ.items() if not k.startswith("LAIB_")}
        env.update(LAIB_MCP_URL=self.fake.url, LAIB_RUN_DIR=str(self.run_dir))
        if session:
            env["LAIB_SESSION"] = session
        p = subprocess.run(["python3", str(HERE / "laibctl.py"), *args], env=env, capture_output=True, text=True, timeout=30)
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        return p.stdout

    def test_each_session_gets_its_own_token_and_keeps_it_while_it_lasts(self):
        h1 = json.loads(self.laib("--headers", session="laib"))
        self.assertEqual(h1, {"Authorization": "Bearer laib_claude_s1"})
        self.assertEqual(json.loads(self.laib("--headers", session="laib")), h1, "a reconnect minted again")
        h2 = json.loads(self.laib("--headers", session="haus"))
        self.assertNotEqual(h2, h1, "two sessions share a token")
        self.assertEqual(self.fake.mints, ["laib", "haus"])
        cache = self.run_dir / "sessions" / "laib.json"
        self.assertEqual(stat.S_IMODE(cache.stat().st_mode), 0o600)
        self.assertEqual(stat.S_IMODE(cache.parent.stat().st_mode), 0o700)

    def test_a_token_that_runs_out_is_replaced_without_the_owner(self):
        self.fake.ttl = timedelta(minutes=5)              # less than the margin: never handed out
        self.laib("--headers", session="laib")
        self.laib("--headers", session="laib")
        self.assertEqual(self.fake.mints, ["laib", "laib"])

    def test_laib_on_the_shell_speaks_as_the_sessions_claude(self):
        out = self.laib("status", session="laib")
        self.assertIn("as Bearer laib_claude_s1", out)
        out = self.laib("status")                          # a shell outside a session
        self.assertIn("as Bearer laib_claude_s2", out)
        self.assertEqual(self.fake.mints, ["laib", "shell"])

    def test_a_hand_file_wins(self):
        # the autoloop's pi hand (/run/laib/token), the web pi's (LAIB_TOKEN_FILE)
        (self.run_dir / "token").write_text("pi-380")
        self.assertIn("as Bearer pi-380", self.laib("status"))
        self.assertEqual(self.fake.mints, [])


class Wiring(unittest.TestCase):
    def test_no_claude_token_at_rest(self):
        contract = (TPL / "laib.yml").read_text()
        self.assertNotIn("LAIB_CLAUDE_TOKEN", contract)
        self.assertRegex(contract, r"- name: LAIB_WERKSTATT_TOKEN\b[^\n]*\n(?:.*\n)*?    mint: werkstatt")
        self.assertNotIn("LAIB_CLAUDE_TOKEN", (TPL / "values.example.yml").read_text())
        self.assertIn("Environment=LAIB_WERKSTATT_TOKEN=", (TPL / "werkstatt.container").read_text())

    def test_claude_code_asks_per_connection(self):
        entry = (HERE / "entrypoint.sh").read_text()
        self.assertRegex(entry, r'headersHelper\\?":\\?"laib --headers')
        self.assertNotIn("Bearer \\${LAIB_TOKEN}", entry, "a fixed bearer in ~/.claude.json")
        self.assertIn("LAIB_SESSION=", (HERE / "sessions.sh").read_text(), "a session's windows must say which session asks")


if __name__ == "__main__":
    unittest.main()
