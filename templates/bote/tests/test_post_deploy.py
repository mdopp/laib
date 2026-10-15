"""#214: bote's post-deploy probes the Haushalt with Haus's bearer and spawns
python3 for it. A spawned process's argv shows in `ps` on the host, to every
local user, so the bearer must not be on it. The hook runs here with a
python3 on PATH that records its argv, against a fake Haushalt that records
the bearer: the bearer still arrives, and no argv carries it."""
import os, subprocess, sys, tempfile, threading, unittest
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

HOOK = Path(__file__).resolve().parent.parent / "post-deploy"
SECRETS = {"HAUS_HAUSHALT_TOKEN": "hh-bearer-6Zq", "BOTE_LAIB_TOKEN": "laib-tok-9Xr"}
SEEN = []


class FakeHaushalt(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def do_POST(self):
        self.rfile.read(int(self.headers.get("Content-Length") or 0))
        SEEN.append(self.headers.get("Authorization"))
        self.send_response(200)
        self.send_header("Content-Length", "2")
        self.end_headers()
        self.wfile.write(b"{}")


class PostDeploy(unittest.TestCase):
    def test_the_bearer_reaches_the_haushalt_and_no_argv(self):
        srv = HTTPServer(("127.0.0.1", 0), FakeHaushalt)
        threading.Thread(target=srv.serve_forever, daemon=True).start()
        self.addCleanup(srv.shutdown)
        with tempfile.TemporaryDirectory() as d:
            shims, log = Path(d, "bin"), Path(d, "argv.log")
            shims.mkdir()
            (shims / "python3").write_text('#!/bin/sh\nprintf "%s\\n" "$*" >> "$ARGV_LOG"\nexec "$REAL_PYTHON" "$@"\n')
            (shims / "python3").chmod(0o755)
            env = dict(os.environ, PATH=f"{shims}:{os.environ['PATH']}", ARGV_LOG=str(log), REAL_PYTHON=sys.executable,
                       LAIB_DATA_DIR=d, LAIB_RENDER_DIR=d + "/none", MODEL_URL="http://127.0.0.1:1/v1", SIGNAL_ACCOUNT="+490000",
                       MODEL_NAME="m", THINKING_MODEL="t", HAUSHALT_MCP_URL=f"http://127.0.0.1:{srv.server_port}/mcp/haushalt",
                       **{"LAIB_SECRET_" + k: v for k, v in SECRETS.items()})
            p = subprocess.run(["bash", str(HOOK)], env=env, capture_output=True, text=True, timeout=60)
            self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
            argv = log.read_text() if log.exists() else ""
            self.assertIn("haushalt:", Path(d, "bote/hermes/config.yaml").read_text(), p.stdout)
        self.assertIn("Bearer " + SECRETS["HAUS_HAUSHALT_TOKEN"], SEEN, "the probe did not carry the bearer")
        for name, s in SECRETS.items():
            self.assertNotIn(s, argv, f"{name} is on a process's argv, where ps shows it")


if __name__ == "__main__":
    unittest.main()
