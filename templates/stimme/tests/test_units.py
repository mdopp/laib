"""#557: `EnvironmentFile=-${VOICE_ENV}` broke stimme-whisper on the box.
systemd knows the optional "-", Quadlet does not: it hands the line to
podman as --env-file "-/mnt/…", a relative path, exit 125, a restart loop
that `laib status` showed only as "activating". The file is required, so
something must write it before the unit starts: llama's pre-start does
(laib-lease rewrites it on every mode change)."""
import os, subprocess, sys, tempfile, unittest
from pathlib import Path

T = Path(__file__).resolve().parent.parent
LLAMA = T.parent / "llama"


class Units(unittest.TestCase):
    def test_no_container_marks_a_file_optional_with_a_dash(self):
        for unit in sorted(T.glob("*.container")):
            for line in unit.read_text().splitlines():
                if line.startswith("EnvironmentFile="):
                    self.assertFalse(line.split("=", 1)[1].startswith("-"), f"{unit.name}: {line} (Quadlet passes the dash to podman)")

    def test_whisper_reads_the_lease_device_file(self):
        self.assertIn("EnvironmentFile=${VOICE_ENV}", (T / "stimme-whisper.container").read_text())
        self.assertIn("llama/lease/voice-device.env", (T / "values.example.yml").read_text())

    def run_llama_pre_start(self, d):
        (Path(d) / "llama" / "models").mkdir(parents=True, exist_ok=True)
        r = subprocess.run([sys.executable, str(LLAMA / "pre-start")], env=dict(os.environ, LAIB_DATA_DIR=d), capture_output=True, text=True, timeout=60)
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)

    def test_llama_pre_start_writes_the_device_file_once_and_keeps_the_leases(self):
        with tempfile.TemporaryDirectory() as d:
            env = Path(d, "llama", "lease", "voice-device.env")
            self.run_llama_pre_start(d)
            self.assertEqual(env.read_text(), "WHISPER_DEVICE=cuda\nTTS_DEVICE=cuda\n", "the lease's own gpu default (internal/lease voiceDevice)")
            env.write_text("WHISPER_DEVICE=cpu\nTTS_DEVICE=cpu\n")   # a mode holds the card
            self.run_llama_pre_start(d)
            self.assertIn("cpu", env.read_text(), "pre-start overwrote what the lease wrote")


if __name__ == "__main__":
    unittest.main()
