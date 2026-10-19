"""#469: what pre-start must have done before the Bote's containers start.
Hermes writes its stock SOUL.md ("You are Hermes Agent …") on its first
start when there is none, and post-deploy, which runs after the units,
used to keep any SOUL.md without the laib marker: the Bote introduced
itself as Hermes. pre-start now writes the marked SOUL first, and Hermes'
stock text is replaced as if it were laib's."""
import os, subprocess, tempfile, unittest
from pathlib import Path

HOOK = Path(__file__).resolve().parent.parent / "pre-start"


def run(data):
    env = dict(os.environ, LAIB_DATA_DIR=str(data), SIGNAL_ACCOUNT="", MATRIX_HOMESERVER="", TELEGRAM_ALLOWED_USERS="",
               DISCORD_ALLOWED_USERS="1", LAIB_SECRET_BOTE_DISCORD_TOKEN="t", LAIB_SECRET_BOTE_LAIB_TOKEN="x")
    p = subprocess.run(["bash", str(HOOK)], env=env, capture_output=True, text=True, timeout=30)
    assert p.returncode == 0, p.stdout + p.stderr


class Soul(unittest.TestCase):
    def test_a_first_start_finds_laibs_soul_not_hermes_default(self):
        with tempfile.TemporaryDirectory() as d:
            run(d)
            soul = Path(d) / "bote" / "hermes" / "SOUL.md"
            self.assertIn("rendered by laib", soul.read_text().splitlines()[0])

    def test_hermes_stock_soul_is_replaced_and_a_persons_own_kept(self):
        with tempfile.TemporaryDirectory() as d:
            soul = Path(d) / "bote" / "hermes" / "SOUL.md"
            soul.parent.mkdir(parents=True)
            soul.write_text("You are Hermes Agent, built by Nous Research.\n")
            run(d)
            self.assertIn("rendered by laib", soul.read_text().splitlines()[0])
            soul.write_text("# meine eigene Seele\nDu bist Kalle.\n")
            run(d)
            self.assertEqual(soul.read_text(), "# meine eigene Seele\nDu bist Kalle.\n", "a SOUL the owner made his own was overwritten")


if __name__ == "__main__":
    unittest.main()
