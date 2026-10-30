"""Owner, 2026-10-30: „Wenn gemeldet wird, dass Hermes weg ist, sollte auch gemeldet werden, dass es
wieder da ist.“ A stop sends Hermes' shutdown notice to the home channel and leaves .clean_shutdown;
Hermes says it is back only after a restart it planned (.restart_pending.json, v0.22.2). The Hermes
unit's ExecStartPre writes that marker after a clean stop, on every start, not only an apply's."""
import json, re, subprocess, tempfile, unittest
from pathlib import Path

UNIT = Path(__file__).resolve().parent.parent / "bote-hermes.container"


def script(data_dir):
    line = next(l for l in UNIT.read_text().splitlines() if l.startswith("ExecStartPre=-/usr/bin/podman unshare sh -c "))
    body = re.match(r"ExecStartPre=-/usr/bin/podman unshare sh -c '(.*)'$", line).group(1)  # unshare: the same shell, in the container's user namespace
    # systemd's C-style unescaping inside quotes, then laib's ${DATA_DIR}
    return body.replace('\\"', '"').replace("\\n", "\n").replace("${DATA_DIR}", data_dir)


class BackOnline(unittest.TestCase):
    def run_pre(self, d):
        p = subprocess.run(["/bin/sh", "-c", script(d)], capture_output=True, text=True, timeout=10)
        self.assertEqual(p.returncode, 0, p.stderr)

    def test_a_start_after_a_clean_stop_owes_the_notice(self):
        with tempfile.TemporaryDirectory() as d:
            h = Path(d) / "bote" / "hermes"
            h.mkdir(parents=True)
            (h / ".clean_shutdown").touch()
            self.run_pre(d)
            self.assertEqual(json.loads((h / ".restart_pending.json").read_text()), {})

    def test_a_crash_or_first_start_owes_none_and_never_fails_the_start(self):
        with tempfile.TemporaryDirectory() as d:
            self.run_pre(d)  # not even the folder: the start goes on
            (Path(d) / "bote" / "hermes").mkdir(parents=True)
            self.run_pre(d)
            self.assertFalse((Path(d) / "bote" / "hermes" / ".restart_pending.json").exists())


if __name__ == "__main__":
    unittest.main()
