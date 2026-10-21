"""#423: llama.cpp removed --draft-max (it is --spec-draft-n-max now), and
the router refused every preset with a drafter after an image update:
"failed to load", the model gone. presets.ini uses the current name, and the
image is pinned by tag and digest (ADR 0019 §4), so an upstream rename
reaches the box only as a proposal someone looks at."""
import json, os, re, subprocess, sys, tempfile, unittest
from pathlib import Path

T = Path(__file__).resolve().parent.parent


def render(presets):
    """Runs pre-start on presets and returns presets.ini."""
    with tempfile.TemporaryDirectory() as d:
        models = Path(d, "llama", "models")
        models.mkdir(parents=True)
        hook = Path(d, "hook")
        hook.mkdir()
        (hook / "pre-start").write_text((T / "pre-start").read_text())
        (hook / "presets.json").write_text(json.dumps(presets))
        r = subprocess.run([sys.executable, str(hook / "pre-start")], env=dict(os.environ, LAIB_DATA_DIR=d), capture_output=True, text=True, timeout=60)
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


class Presets(unittest.TestCase):
    def test_presets_ini_is_written_before_the_router_starts(self):
        """#512: post-deploy wrote presets.ini after apply had restarted llama,
        so the router ran on the old file (thinking stayed on) until a second
        restart. The router reads it only when it starts: pre-start writes
        it, and post-deploy, which runs after the start, does not."""
        self.assertIn("presets.ini", (T / "pre-start").read_text())
        self.assertNotIn("presets.ini\"", (T / "post-deploy").read_text())
        self.assertNotIn("open(os.path.join(MODELS, \"presets.ini", (T / "post-deploy").read_text())
        self.assertTrue(os.access(T / "pre-start", os.X_OK), "pre-start must be executable")


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

    def test_prompt_eval_knobs_are_per_preset(self):
        """#525: Qwen 35B-A3B read prompts at ~300-400 t/s on the 16 GB card.
        batch, ubatch, flash-attn, n-gpu-layers, n-cpu-moe and the slots are
        presets.json keys the box can measure and pick; an unset key leaves
        llama.cpp's default, so the other presets do not change."""
        one = {"household": "a", "presets": [
            {"alias": "a", "repo": "r", "file": "a.gguf", "batch": 4096, "ubatch": 2048, "flashAttn": True, "ngl": 99, "nCpuMoe": 0, "parallel": 2},
            {"alias": "b", "repo": "r", "file": "b.gguf", "flashAttn": "auto"}]}
        got = sections(render(one))
        self.assertEqual({k: got["a"][k] for k in ("batch-size", "ubatch-size", "flash-attn", "n-gpu-layers", "n-cpu-moe", "parallel")},
                         {"batch-size": "4096", "ubatch-size": "2048", "flash-attn": "on", "n-gpu-layers": "99", "n-cpu-moe": "0", "parallel": "2"})
        self.assertEqual(got["b"]["flash-attn"], "auto")
        self.assertFalse({"batch-size", "n-gpu-layers", "n-cpu-moe", "parallel"} & set(got["b"]), got["b"])
        shipped = json.loads((T / "presets.json").read_text())
        q = sections(render(shipped))[shipped["household"]]
        self.assertEqual((q.get("batch-size"), q.get("ubatch-size"), q.get("flash-attn")), ("4096", "2048", "on"), q)


class Measure(unittest.TestCase):
    """#525: measure.py is how the box compares a preset before and after."""

    def setUp(self):
        sys.path.insert(0, str(T))
        import measure
        self.m = measure

    def test_reads_where_the_weights_sit_from_the_llama_log(self):
        log = "\n".join([
            "load_tensors: offloaded 30/41 layers to GPU",
            "load_tensors:   CPU_Mapped model buffer size =  4100.50 MiB",
            "load_tensors:        CUDA0 model buffer size =  9000.00 MiB",
            "llama_context: n_batch       = 2048", "llama_context: n_ubatch      = 512",
            "llama_context: flash_attn    = auto", "llama_context: n_seq_max     = 4",
            "load_tensors: offloaded 41/41 layers to GPU"])
        got = self.m.from_log(log)
        self.assertEqual(got["offloaded"], "41/41", "the last load counts")
        self.assertEqual((got["cpu_mapped"], got["n_ubatch"], got["flash_attn"], got["n_seq_max"]), ("4100.50 MiB", "512", "auto", "4"))

    def test_one_run_is_prompt_eval_only(self):
        import http.server, threading
        seen = {}

        class H(http.server.BaseHTTPRequestHandler):
            def do_POST(self):
                seen.update(json.loads(self.rfile.read(int(self.headers["Content-Length"]))))
                b = json.dumps({"timings": {"prompt_n": 14000, "prompt_ms": 7000.0, "prompt_per_second": 2000.0}}).encode()
                self.send_response(200); self.send_header("Content-Length", str(len(b))); self.end_headers(); self.wfile.write(b)

            def log_message(self, *a):
                pass

        srv = http.server.HTTPServer(("127.0.0.1", 0), H)
        threading.Thread(target=srv.serve_forever, daemon=True).start()
        try:
            r = self.m.once(f"http://127.0.0.1:{srv.server_port}", "qwen", self.m.prompt(14000))
        finally:
            srv.shutdown()
        self.assertEqual((seen["max_tokens"], seen["cache_prompt"], seen["model"]), (1, False, "qwen"))
        self.assertEqual(r["prompt_per_second"], 2000.0)
        self.assertGreater(len(seen["messages"][0]["content"].split()), 5000)


if __name__ == "__main__":
    unittest.main()
