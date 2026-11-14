"""The health check sees a name conflict (the first day on the box: avahi renamed itself
drucker-2 … drucker-46, every 20 seconds, and the container counted as healthy). druck-start.sh
gesund runs with stubs for lpstat, avahi-daemon and ps."""
import os
import subprocess
import tempfile
import time
import unittest
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent.parent / "druck-start.sh"


def gesund(avahi_title, fresh=True):
    with tempfile.TemporaryDirectory() as d:
        bin_ = Path(d, "bin")
        bin_.mkdir()
        for name, body in {"lpstat": "echo 'scheduler is running'", "avahi-daemon": "exit 0",
                           "ps": f"echo 'avahi-daemon: {avahi_title}'"}.items():
            Path(bin_, name).write_text("#!/bin/sh\n" + body + "\n")
            Path(bin_, name).chmod(0o755)
        Path(d, "hostname").write_text("atHome-Server\n")
        status = Path(d, "laib-status.json")
        status.write_text("{}")
        if not fresh:
            old = time.time() - 3600
            os.utime(status, (old, old))
        env = dict(os.environ, PATH=f"{bin_}:{os.environ['PATH']}", DRUCK_DATA=d, DRUCK_HOSTNAME_FILE=str(Path(d, "hostname")))
        r = subprocess.run(["bash", str(SCRIPT), "gesund"], env=env, capture_output=True, text=True)
        return r.returncode, r.stdout


class Gesund(unittest.TestCase):
    def test_avahi_running_under_the_box_s_name_is_healthy(self):
        self.assertEqual(gesund("running [atHome-Server.local]")[0], 0)
        self.assertEqual(gesund("running [athome-server.local]")[0], 0, "case does not matter in mDNS")

    def test_a_renamed_or_still_registering_avahi_is_red(self):
        for title in ("running [atHome-Server-2.local]", "registering [drucker-46.local]", "running [drucker.local]"):
            code, out = gesund(title)
            self.assertEqual(code, 1, title)
            self.assertIn("Namenskonflikt", out)

    def test_a_stuck_search_is_red(self):
        self.assertEqual(gesund("running [atHome-Server.local]", fresh=False)[0], 1)


if __name__ == "__main__":
    unittest.main()
