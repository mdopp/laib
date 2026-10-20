"""laib#490: frage's pre-start writes SearXNG's settings.yml before the unit
starts, on every apply. The image writes its own on the first start, without
`formats: [html, json]`, and the old post-deploy left that one standing
(`suche: 403 Forbidden`)."""
import os, subprocess, tempfile, unittest
from pathlib import Path

HOOK = Path(__file__).resolve().parent.parent / "pre-start"


def run(d):
    env = dict(os.environ, LAIB_DATA_DIR=d, LAIB_SECRET_SEARX_SECRET="searx-test-secret")
    p = subprocess.run(["bash", str(HOOK)], env=env, capture_output=True, text=True, timeout=30)
    if p.returncode:
        raise AssertionError(p.stdout + p.stderr)
    return p.stdout


class Settings(unittest.TestCase):
    def test_fresh_box(self):
        d = tempfile.mkdtemp()
        run(d)
        s = (Path(d) / "frage/searxng/settings.yml").read_text()
        self.assertIn("formats: [html, json]", s)
        self.assertIn('secret_key: "searx-test-secret"', s)
        self.assertIn("port: 8888", s)

    def test_the_images_own_settings_are_replaced(self):
        d = tempfile.mkdtemp()
        f = Path(d) / "frage/searxng/settings.yml"
        f.parent.mkdir(parents=True)
        f.write_text("use_default_settings: true\nserver:\n  secret_key: \"ultrasecretkey\"\n")
        run(d)
        self.assertIn("formats: [html, json]", f.read_text())
        self.assertNotIn("ultrasecretkey", f.read_text())

    def test_second_apply_changes_nothing(self):
        d = tempfile.mkdtemp()
        run(d)
        f = Path(d) / "frage/searxng/settings.yml"
        before = (f.read_text(), f.stat().st_mtime_ns)
        out = run(d)
        self.assertEqual(before, (f.read_text(), f.stat().st_mtime_ns))
        self.assertNotIn("written", out)


if __name__ == "__main__":
    unittest.main()
