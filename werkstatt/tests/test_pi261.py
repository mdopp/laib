"""#261: every pi in the Werkstatt has the laib-llama provider. The image
copied the extension to /opt/werkstatt/extensions and nothing told pi to
load it, so `pi --model laib-llama/<preset>` (the autoloop's call) ended in
"model not found". The real image against a fake lease and proxy is
scripts/werkstatt-model.sh; this pins the wrapper's contract."""
import importlib.util, os, subprocess, tempfile, unittest
from pathlib import Path
from unittest import mock

HERE = Path(__file__).resolve().parent.parent          # werkstatt/
EXT = "/opt/werkstatt/extensions/laib-llama.js"
GUIDE = "/opt/werkstatt/guide/AGENTS.md"     # #262: the Werkstatt guide in pi's system prompt
PRE = ["-e", EXT, "--append-system-prompt", GUIDE]


def run_wrapper(*args: str) -> list[str]:
    """werkstatt/pi with a fake pi-upstream that prints its argv, one per line."""
    with tempfile.TemporaryDirectory() as d:
        fake = Path(d) / "pi-upstream"
        fake.write_text('#!/bin/sh\nfor a in "$@"; do printf "%s\\n" "$a"; done\n')
        fake.chmod(0o755)
        env = dict(os.environ, PATH=f"{d}:{os.environ['PATH']}")
        p = subprocess.run(["sh", str(HERE / "pi"), *args], env=env, capture_output=True, text=True, timeout=30)
        if p.returncode != 0:
            raise AssertionError(p.stdout + p.stderr)
        return p.stdout.splitlines()


class EveryPiHasTheBoxModel(unittest.TestCase):
    def test_a_run_loads_the_extension_before_its_own_arguments(self):
        # the autoloop's exact call (autoloop.run_pi)
        argv = run_wrapper("--mode", "json", "--model", "laib-llama/qwen3.8-27b", "sag hallo")
        self.assertEqual(argv[:4], PRE)
        self.assertEqual(argv[4:], ["--mode", "json", "--model", "laib-llama/qwen3.8-27b", "sag hallo"])

    def test_an_interactive_start_loads_it_too(self):
        self.assertEqual(run_wrapper(), PRE)

    def test_subcommands_pass_through_untouched(self):
        # pi parses `-e x config` as a prompt run: a leading -e breaks every subcommand
        for sub in ("install", "remove", "uninstall", "update", "list", "config", "auth", "mcp"):
            self.assertEqual(run_wrapper(sub, "--help"), [sub, "--help"], sub)

    def test_the_image_puts_the_wrapper_where_pi_is_found(self):
        cf = (HERE / "Containerfile").read_text()
        self.assertIn("mv /usr/local/bin/pi /usr/local/bin/pi-upstream", cf)
        self.assertRegex(cf, r"COPY pi /usr/local/bin/pi\b")
        self.assertIn("COPY extensions /opt/werkstatt/extensions", cf)


class PiReadsNoStdin(unittest.TestCase):
    def test_the_autoloop_starts_pi_with_stdin_closed(self):
        # pi --mode json reads a piped stdin as more prompt: an inherited stdin
        # that never closes held the run until the time cap (seen in #261)
        spec = importlib.util.spec_from_file_location("autoloop", HERE / "autoloop.py")
        al = importlib.util.module_from_spec(spec); spec.loader.exec_module(al)
        seen = {}
        def run(cmd, **kw):
            seen.update(kw)
            return subprocess.CompletedProcess(cmd, 0, stdout='{"type":"agent_end"}\n', stderr="")
        with mock.patch.object(al.subprocess, "run", run):
            al.run_pi("hallo", al.CODING_PRESET, Path("."), 10)
        self.assertIs(seen.get("stdin"), subprocess.DEVNULL)


if __name__ == "__main__":
    unittest.main()
