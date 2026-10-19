"""#392: the owner's Claude sessions reached the box through ServiceBay's
MCP (host.containers.internal:5888/mcp, entries `servicebay` and
`atHome-Servicebay`, user scope and per project in ~/.claude.json). Since
migration day they fail on every start. The Werkstatt's sessions have
Laib's /mcp (the entrypoint's `laib` entry); at every start
werkstatt-mcp-tidy drops every entry that points at ServiceBay's MCP and
leaves everything else as it was. The token in an entry's header is never
printed."""
import json, os, stat, subprocess, sys, tempfile, unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent.parent
SCRIPT = HERE / "mcp_tidy.py"
SB = "http://host.containers.internal:5888/mcp"


class Tidy(unittest.TestCase):
    def run_tidy(self, home):
        return subprocess.run([sys.executable, "-I", str(SCRIPT)], env=dict(os.environ, HOME=home), capture_output=True, text=True, timeout=30)

    def test_servicebays_entries_go_the_rest_stays(self):
        with tempfile.TemporaryDirectory() as home:
            cfg = {
                "numStartups": 7,
                "mcpServers": {
                    "servicebay": {"type": "http", "url": SB, "headers": {"Authorization": "Bearer sb_synthetic-0"}},
                    "atHome-Servicebay": {"type": "http", "url": SB + "/", "headers": {"Authorization": "Bearer sb_synthetic-1"}},
                    "laib": {"type": "http", "url": "http://host.containers.internal:5900/mcp", "headers": {"Authorization": "Bearer ${LAIB_TOKEN}"}},
                    "github": {"type": "stdio", "command": "gh-mcp"},
                },
                "projects": {
                    "/workspace/solarisbay": {"mcpServers": {"atHome-Servicebay": {"type": "http", "url": SB}}, "allowedTools": ["x"]},
                    "/workspace/laib": {"history": [1]},
                },
            }
            p = Path(home, ".claude.json")
            p.write_text(json.dumps(cfg))
            p.chmod(0o600)
            r = self.run_tidy(home)
            self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
            out = json.loads(p.read_text())
            self.assertEqual(sorted(out["mcpServers"]), ["github", "laib"])
            self.assertEqual(out["projects"]["/workspace/solarisbay"], {"mcpServers": {}, "allowedTools": ["x"]})
            self.assertEqual(out["projects"]["/workspace/laib"], {"history": [1]})
            self.assertEqual(out["numStartups"], 7)
            self.assertEqual(stat.S_IMODE(p.stat().st_mode), 0o600)
            self.assertIn("servicebay", r.stdout)
            self.assertNotIn("sb_synthetic", r.stdout + r.stderr)
            # the next start: nothing to do, the file is not rewritten
            before = p.stat().st_mtime_ns
            r = self.run_tidy(home)
            self.assertEqual((r.returncode, p.stat().st_mtime_ns), (0, before), r.stdout + r.stderr)

    def test_no_config_is_nothing_to_do(self):
        with tempfile.TemporaryDirectory() as home:
            r = self.run_tidy(home)
            self.assertEqual(r.returncode, 0, r.stderr)
            self.assertFalse(Path(home, ".claude.json").exists())

    def test_the_image_runs_it_at_every_start(self):
        self.assertIn("COPY mcp_tidy.py /usr/local/bin/werkstatt-mcp-tidy", (HERE / "Containerfile").read_text())
        self.assertIn("werkstatt-mcp-tidy", (HERE / "entrypoint.sh").read_text())


if __name__ == "__main__":
    unittest.main()
