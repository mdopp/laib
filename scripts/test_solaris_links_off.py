"""#396: scripts/solaris-links-off.py switches off Solaris's links into the
old world explicitly (ADR 0015: Solaris retires; no shims for it): the
ServiceBay admin toolbox, its token mint, and the DAV write-back that needs
Radicale rights Laib's Radicale does not grant. The ignored solaris.yml is
edited in place (a copy kept once), the rights timer is disabled, Solaris
restarted. systemctl is a recorder; the script runs as the operator runs it."""
import os, stat, subprocess, tempfile, unittest
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent / "solaris-links-off.py"

YML = """apiVersion: v1
kind: Pod
spec:
  containers:
  - name: engine
    env:
    - name: SB_MCP_URL
      value: "http://127.0.0.1:5888/mcp"
    - name: SB_API_URL
      value: "http://127.0.0.1:5888"
    - name: SB_MINT_URL
      value: "https://www.example.home"
    - name: DEADLINES_SYNC_URL_BASE
      value: "http://127.0.0.1:5232"
    - name: CONTACTS_SYNC_URL_BASE
      value: http://127.0.0.1:5232
    - name: DEADLINES_SYNC_URL
      value: ""
    - name: CALDAV_URL
      value: "http://127.0.0.1:5232/martin/"
"""


class LinksOff(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        t = Path(self.tmp.name)
        self.yml, self.calls = t / "solaris.yml", t / "calls"
        self.yml.write_text(YML)
        (t / "bin").mkdir()
        sc = t / "bin" / "systemctl"
        sc.write_text(f'#!/bin/sh\necho "systemctl $*" >> {self.calls}\n')
        sc.chmod(sc.stat().st_mode | stat.S_IEXEC)
        self.env = dict(os.environ, PATH=f"{t / 'bin'}:{os.environ['PATH']}")

    def tearDown(self):
        self.tmp.cleanup()

    def run_it(self):
        return subprocess.run(["python3", str(SCRIPT), "--yml", str(self.yml)], capture_output=True, text=True, env=self.env)

    def test_the_old_links_go_dark_and_the_rest_stays(self):
        r = self.run_it()
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        text = self.yml.read_text()
        for name in ("SB_MCP_URL", "SB_API_URL", "SB_MINT_URL", "DEADLINES_SYNC_URL_BASE", "CONTACTS_SYNC_URL_BASE"):
            self.assertIn(f'- name: {name}\n      value: ""\n', text)
            self.assertIn(name, r.stdout)
        self.assertIn('value: "http://127.0.0.1:5232/martin/"', text)   # CalDAV reading stays
        self.assertEqual(Path(str(self.yml) + ".before-396").read_text(), YML)
        calls = self.calls.read_text().splitlines()
        self.assertIn("systemctl --user disable --now solaris-radicale-rights.timer", calls)
        self.assertIn("systemctl --user restart solaris.service", calls)

    def test_a_second_run_changes_nothing(self):
        self.run_it()
        self.calls.write_text("")
        r = self.run_it()
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertIn("nothing to change", r.stdout)
        self.assertEqual(Path(str(self.yml) + ".before-396").read_text(), YML)
        self.assertNotIn("restart", self.calls.read_text())


if __name__ == "__main__":
    unittest.main()
