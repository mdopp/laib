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


    def test_the_ha_tool_speaks_assist_as_the_bote_and_the_automat_has_its_own_token(self):
        """#349: BOTE_HA_TOKEN belongs to HA's non-admin user bote, and HA serves
        a non-admin only the Assist API (/api/mcp/assist; /mcp_server/sse wants an
        administrator by default). The Automat edits automations, which HA allows
        administrators only: it gets BOTE_AUTOMAT_HA_TOKEN, not the Bote's."""
        def config(**secrets):
            with tempfile.TemporaryDirectory() as d:
                env = dict(os.environ, LAIB_DATA_DIR=d, LAIB_RENDER_DIR=d + "/none", MODEL_URL="http://127.0.0.1:1/v1", SIGNAL_ACCOUNT="+490000",
                           MODEL_NAME="m", THINKING_MODEL="t", HA_URL="http://host.containers.internal:8123", LAIB_SECRET_BOTE_LAIB_TOKEN="laib-tok",
                           **{"LAIB_SECRET_" + k: v for k, v in secrets.items()})
                p = subprocess.run(["bash", str(HOOK)], env=env, capture_output=True, text=True, timeout=60)
                self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
                return Path(d, "bote/hermes/config.yaml").read_text(), Path(d, "bote/hermes/.env").read_text()
        conf, dotenv = config(BOTE_HA_TOKEN="bote-user-tok")
        self.assertIn("url: http://host.containers.internal:8123/api/mcp/assist", conf)
        self.assertNotIn("mcp_server/sse", conf)
        self.assertNotIn("automat:", conf, "the Automat wired without its administrator token")
        self.assertIn("HA_TOKEN=bote-user-tok", dotenv)
        conf, _ = config(BOTE_HA_TOKEN="bote-user-tok", BOTE_AUTOMAT_HA_TOKEN="admin-tok")
        self.assertIn("automat:", conf)
        self.assertNotIn("admin-tok", conf + dotenv, "the Automat's administrator token reaches Hermes")
        conf, _ = config(BOTE_AUTOMAT_HA_TOKEN="admin-tok")
        self.assertIn("automat:", conf)
        self.assertNotIn("api/mcp/assist", conf)

    def test_the_persona_engine_is_a_plugin_in_front_of_the_model_not_a_tool(self):
        """#471: Hermes wraps every MCP result as untrusted data, so the engine's
        screens never reached the person. It runs as the plugin laib-persona
        (always, first), and is no MCP server the model calls any more."""
        for usage in ("", "um-tok"):
            with tempfile.TemporaryDirectory() as d:
                env = dict(os.environ, LAIB_DATA_DIR=d, LAIB_RENDER_DIR=d + "/none", MODEL_URL="http://127.0.0.1:1/v1", SIGNAL_ACCOUNT="+490000",
                           MODEL_NAME="m", THINKING_MODEL="t", LAIB_SECRET_BOTE_LAIB_TOKEN="laib-tok")
                if usage:
                    env["LAIB_SECRET_USAGE_METRICS_TOKEN"] = usage
                p = subprocess.run(["bash", str(HOOK)], env=env, capture_output=True, text=True, timeout=60)
                self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
                h = Path(d, "bote/hermes")
                conf, soul = (h / "config.yaml").read_text(), (h / "SOUL.md").read_text()
                self.assertNotIn("8644", conf, "the persona engine is still an MCP server the model calls")
                self.assertIn("enabled: [laib-persona" + (", laib-nutzung]" if usage else "]"), conf)
                self.assertEqual((h / "plugins/laib-persona/__init__.py").read_text(), (HOOK.parent / "persona_plugin.py").read_text())
                self.assertIn("name: laib-persona", (h / "plugins/laib-persona/plugin.yaml").read_text())
                self.assertNotIn("Werkzeug persona", soul)
                self.assertIn("Persona:", soul)

    def test_hermes_is_pinned(self):
        """#471, ADR 0019: a stock update must not change how Hermes treats tool output unseen."""
        vals = (HOOK.parent / "values.example.yml").read_text()
        self.assertIn("HERMES_IMAGE: docker.io/nousresearch/hermes-agent:v0.22.2", vals)


if __name__ == "__main__":
    unittest.main()
