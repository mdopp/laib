"""#423: llama.cpp removed --draft-max (it is --spec-draft-n-max now), and
the router refused every preset with a drafter after an image update:
"failed to load", the model gone. presets.ini uses the current name, and the
image is pinned by tag and digest (ADR 0019 §4), so an upstream rename
reaches the box only as a proposal someone looks at."""
import json, os, re, subprocess, sys, tempfile, unittest
from pathlib import Path

T = Path(__file__).resolve().parent.parent


class PostDeploy(unittest.TestCase):
    def test_presets_ini_names_the_current_drafter_flag(self):
        presets = json.loads((T / "presets.json").read_text())
        with tempfile.TemporaryDirectory() as d:
            models = Path(d, "llama", "models")
            models.mkdir(parents=True)
            for p in presets["presets"]:   # every weight already there: no download
                for k in ("file", "draft", "mmproj"):
                    if p.get(k):
                        (models / p[k]).write_text("x")
            r = subprocess.run([sys.executable, str(T / "post-deploy")], env=dict(os.environ, LAIB_DATA_DIR=d), capture_output=True, text=True, timeout=60)
            self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
            ini = (models / "presets.ini").read_text()
        self.assertNotIn("draft-max", ini.replace("spec-draft-n-max", ""), "llama.cpp refuses --draft-max")
        drafters = [p for p in presets["presets"] if p.get("draft")]
        self.assertEqual(len(re.findall(r"(?m)^spec-draft-n-max = \d+$", ini)), len(drafters), ini)
        self.assertIn("spec-draft-n-max = 8", ini, "qwen3.8-27b's draftMax 8 is lost")

    def test_the_image_is_pinned_by_tag_and_digest(self):
        image = re.search(r"(?m)^IMAGE: (\S+)$", (T / "values.example.yml").read_text()).group(1)
        self.assertRegex(image, r"^ghcr\.io/ggml-org/llama\.cpp:server-cuda-b\d+@sha256:[0-9a-f]{64}$")
        for unit in ("llama.container", "llama-embed.container"):
            self.assertNotIn("AutoUpdate=registry", (T / unit).read_text(), f"{unit}: a pinned image is changed by proposal, not by auto-update")


if __name__ == "__main__":
    unittest.main()
