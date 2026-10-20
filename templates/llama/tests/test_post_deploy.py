"""#423: llama.cpp removed --draft-max (it is --spec-draft-n-max now), and
the router refused every preset with a drafter after an image update:
"failed to load", the model gone. presets.ini uses the current name, and the
image is pinned by tag and digest (ADR 0019 §4), so an upstream rename
reaches the box only as a proposal someone looks at."""
import json, os, re, subprocess, sys, tempfile, unittest
from pathlib import Path

T = Path(__file__).resolve().parent.parent


def render(presets):
    """Runs the hook on presets (every weight already there: no download) and returns presets.ini."""
    with tempfile.TemporaryDirectory() as d:
        models = Path(d, "llama", "models")
        models.mkdir(parents=True)
        for p in presets["presets"]:
            for k in ("file", "draft", "mmproj"):
                if p.get(k):
                    (models / p[k]).write_text("x")
        hook = Path(d, "hook")
        hook.mkdir()
        (hook / "post-deploy").write_text((T / "post-deploy").read_text())
        (hook / "presets.json").write_text(json.dumps(presets))
        r = subprocess.run([sys.executable, str(hook / "post-deploy")], env=dict(os.environ, LAIB_DATA_DIR=d), capture_output=True, text=True, timeout=60)
        if r.returncode != 0:
            raise AssertionError(r.stdout + r.stderr)
        return (models / "presets.ini").read_text()


def sections(ini):
    out, name = {}, None
    for line in ini.splitlines():
        if line.startswith("["):
            name = line.strip("[]"); out[name] = {}
        elif " = " in line:
            k, v = line.split(" = ", 1); out[name][k] = v
    return out


class PostDeploy(unittest.TestCase):
    def test_presets_ini_names_the_current_drafter_flag(self):
        presets = json.loads((T / "presets.json").read_text())
        ini = render(presets)
        self.assertNotIn("draft-max", ini.replace("spec-draft-n-max", ""), "llama.cpp refuses --draft-max")
        drafters = [p for p in presets["presets"] if p.get("draft")]
        self.assertEqual(len(re.findall(r"(?m)^spec-draft-n-max = \d+$", ini)), len(drafters), ini)
        self.assertIn("spec-draft-n-max = 8", ini, "qwen3.8-27b's draftMax 8 is lost")

    def test_thinking_is_off_unless_a_preset_opts_in(self):
        """#501: Qwen thought before every answer (17 s instead of 1.8 s on the
        Kraftwerk) because Frage, the Operator and pi send no
        chat_template_kwargs. The router's default is "off" per preset;
        `"thinking": true` in presets.json is the opt-in. `reasoning` is the
        b11723 flag (`--reasoning off` sets enable_thinking=false as the
        server's template default; --chat-template-kwargs with
        enable_thinking is deprecated there, and --reasoning-budget 0 would
        cut a thinking request off too)."""
        shipped = json.loads((T / "presets.json").read_text())
        got = sections(render(shipped))
        for p in shipped["presets"]:
            self.assertEqual(got[p["alias"]].get("reasoning"), "on" if p.get("thinking") else "off", p["alias"])
        self.assertFalse(shipped["presets"][[p["alias"] for p in shipped["presets"]].index(shipped["household"])].get("thinking"),
                         "the household preset thinks by default: every Frage answer waits for it")
        one = {"household": "a", "presets": [{"alias": "a", "repo": "r", "file": "a.gguf"}, {"alias": "b", "repo": "r", "file": "b.gguf", "thinking": True}]}
        got = sections(render(one))
        self.assertEqual((got["a"]["reasoning"], got["b"]["reasoning"]), ("off", "on"))

    def test_the_bote_still_asks_for_thinking_per_request(self):
        """With the server default off, /model denken thinks only because its
        provider sends enable_thinking:true on every request (#476); llama.cpp
        merges a request's chat_template_kwargs over the server default
        (tools/server/server-common.cpp at b11723). Losing that kwarg would
        silently make denken a second fast model."""
        hook = (T.parent / "bote" / "post-deploy").read_text()
        self.assertRegex(hook, r"- name: denken\n\s+base_url: \$\{MODEL_URL\}\n\s+extra_body: \{chat_template_kwargs: \{enable_thinking: true\}\}")

    def test_the_image_is_pinned_by_tag_and_digest(self):
        image = re.search(r"(?m)^IMAGE: (\S+)$", (T / "values.example.yml").read_text()).group(1)
        self.assertRegex(image, r"^ghcr\.io/ggml-org/llama\.cpp:server-cuda-b\d+@sha256:[0-9a-f]{64}$")
        for unit in ("llama.container", "llama-embed.container"):
            self.assertNotIn("AutoUpdate=registry", (T / unit).read_text(), f"{unit}: a pinned image is changed by proposal, not by auto-update")


if __name__ == "__main__":
    unittest.main()
