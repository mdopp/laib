"""#214: bote's post-deploy probes the Haushalt with Haus's bearer and spawns
python3 for it. A spawned process's argv shows in `ps` on the host, to every
local user, so the bearer must not be on it. The hook runs here with a
python3 on PATH that records its argv, against a fake Haushalt that records
the bearer: the bearer still arrives, and no argv carries it."""
import os, subprocess, sys, tempfile, threading, unittest
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

HOOK = Path(__file__).resolve().parent.parent / "post-deploy"
SECRETS = {"HAUS_HAUSHALT_TOKEN": "hh-bearer-6Zq", "BOTE_LAIB_TOKEN": "laib-tok-9Xr"}
SEEN = []


class FakeHaushalt(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def do_POST(self):
        self.rfile.read(int(self.headers.get("Content-Length") or 0))
        SEEN.append(self.headers.get("Authorization"))
        self.send_response(200)
        self.send_header("Content-Length", "2")
        self.end_headers()
        self.wfile.write(b"{}")


class PostDeploy(unittest.TestCase):
    def test_the_bearer_reaches_the_haushalt_and_no_argv(self):
        srv = HTTPServer(("127.0.0.1", 0), FakeHaushalt)
        threading.Thread(target=srv.serve_forever, daemon=True).start()
        self.addCleanup(srv.shutdown)
        with tempfile.TemporaryDirectory() as d:
            shims, log = Path(d, "bin"), Path(d, "argv.log")
            shims.mkdir()
            (shims / "python3").write_text('#!/bin/sh\nprintf "%s\\n" "$*" >> "$ARGV_LOG"\nexec "$REAL_PYTHON" "$@"\n')
            (shims / "python3").chmod(0o755)
            env = dict(os.environ, PATH=f"{shims}:{os.environ['PATH']}", ARGV_LOG=str(log), REAL_PYTHON=sys.executable,
                       LAIB_DATA_DIR=d, LAIB_RENDER_DIR=d + "/none", MODEL_URL="http://127.0.0.1:1/v1", SIGNAL_ACCOUNT="+490000",
                       MODEL_NAME="m", THINKING_MODEL="t", HAUSHALT_MCP_URL=f"http://127.0.0.1:{srv.server_port}/mcp/haushalt",
                       **{"LAIB_SECRET_" + k: v for k, v in SECRETS.items()})
            p = subprocess.run(["bash", str(HOOK)], env=env, capture_output=True, text=True, timeout=60)
            self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
            argv = log.read_text() if log.exists() else ""
            self.assertIn("haushalt:", Path(d, "bote/hermes/config.yaml").read_text(), p.stdout)
        self.assertIn("Bearer " + SECRETS["HAUS_HAUSHALT_TOKEN"], SEEN, "the probe did not carry the bearer")
        for name, s in SECRETS.items():
            self.assertNotIn(s, argv, f"{name} is on a process's argv, where ps shows it")


    def test_the_ha_tool_speaks_assist_as_the_bote_and_the_automat_has_its_own_token(self):
        """#349: BOTE_HA_TOKEN belongs to HA's non-admin user bote, and HA serves
        a non-admin only the Assist API (/api/mcp/assist; /mcp_server/sse wants an
        administrator by default). The Automat edits automations, which HA allows
        administrators only: it gets BOTE_AUTOMAT_HA_TOKEN, not the Bote's."""
        def config(**secrets):
            with tempfile.TemporaryDirectory() as d:
                env = dict(os.environ, LAIB_DATA_DIR=d, LAIB_RENDER_DIR=d + "/none", MODEL_URL="http://127.0.0.1:1/v1", SIGNAL_ACCOUNT="+490000",
                           MODEL_NAME="m", THINKING_MODEL="t", HA_URL="http://host.containers.internal:8123", LAIB_SECRET_BOTE_LAIB_TOKEN="laib-tok",
                           **{"LAIB_SECRET_" + k: v for k, v in secrets.items()})
                p = subprocess.run(["bash", str(HOOK)], env=env, capture_output=True, text=True, timeout=60)
                self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
                return Path(d, "bote/hermes/config.yaml").read_text(), Path(d, "bote/hermes/.env").read_text()
        conf, dotenv = config(BOTE_HA_TOKEN="bote-user-tok")
        self.assertIn("url: http://host.containers.internal:8123/api/mcp/assist", conf)
        self.assertNotIn("mcp_server/sse", conf)
        self.assertNotIn("automat:", conf, "the Automat wired without its administrator token")
        self.assertIn("HA_TOKEN=bote-user-tok", dotenv)
        conf, _ = config(BOTE_HA_TOKEN="bote-user-tok", BOTE_AUTOMAT_HA_TOKEN="admin-tok")
        self.assertIn("automat:", conf)
        self.assertNotIn("admin-tok", conf + dotenv, "the Automat's administrator token reaches Hermes")
        conf, _ = config(BOTE_AUTOMAT_HA_TOKEN="admin-tok")
        self.assertIn("automat:", conf)
        self.assertNotIn("api/mcp/assist", conf)

    def test_the_persona_engine_is_a_plugin_in_front_of_the_model_not_a_tool(self):
        """#471: Hermes wraps every MCP result as untrusted data, so the engine's
        screens never reached the person. It runs as the plugin laib-persona
        (always, first), and is no MCP server the model calls any more."""
        for usage in ("", "um-tok"):
            with tempfile.TemporaryDirectory() as d:
                env = dict(os.environ, LAIB_DATA_DIR=d, LAIB_RENDER_DIR=d + "/none", MODEL_URL="http://127.0.0.1:1/v1", SIGNAL_ACCOUNT="+490000",
                           MODEL_NAME="m", THINKING_MODEL="t", LAIB_SECRET_BOTE_LAIB_TOKEN="laib-tok")
                if usage:
                    env["LAIB_SECRET_USAGE_METRICS_TOKEN"] = usage
                p = subprocess.run(["bash", str(HOOK)], env=env, capture_output=True, text=True, timeout=60)
                self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
                h = Path(d, "bote/hermes")
                conf, soul = (h / "config.yaml").read_text(), (h / "SOUL.md").read_text()
                self.assertNotIn("8644", conf, "the persona engine is still an MCP server the model calls")
                self.assertIn("enabled: [laib-persona" + (", laib-nutzung]" if usage else "]"), conf)
                self.assertEqual((h / "plugins/laib-persona/__init__.py").read_text(), (HOOK.parent / "persona_plugin.py").read_text())
                self.assertIn("name: laib-persona", (h / "plugins/laib-persona/plugin.yaml").read_text())
                self.assertNotIn("Werkzeug persona", soul)
                self.assertIn("Persona:", soul)

    def test_who_switched_is_a_plugin_while_home_assistant_is_wired(self):
        """#591: laib-journal journals who switched in HA through the Bote; it is placed and on
        only while the ha tool or the Automat is wired, and the Bote's hand may note (propose tier)."""
        for secrets, on in (({}, False), ({"LAIB_SECRET_BOTE_HA_TOKEN": "t"}, True), ({"LAIB_SECRET_BOTE_AUTOMAT_HA_TOKEN": "t"}, True)):
            with tempfile.TemporaryDirectory() as d:
                env = dict(os.environ, LAIB_DATA_DIR=d, LAIB_RENDER_DIR=d + "/none", MODEL_URL="http://127.0.0.1:1/v1", SIGNAL_ACCOUNT="+490000",
                           MODEL_NAME="m", THINKING_MODEL="t", LAIB_SECRET_BOTE_LAIB_TOKEN="laib-tok", **secrets)
                p = subprocess.run(["bash", str(HOOK)], env=env, capture_output=True, text=True, timeout=60)
                self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
                h = Path(d, "bote/hermes")
                conf = (h / "config.yaml").read_text()
                self.assertEqual("laib-journal" in conf, on, (secrets, conf))
                self.assertEqual((h / "plugins/laib-journal").exists(), on)
                if on:
                    self.assertIn("enabled: [laib-persona, laib-journal]", conf)
                    self.assertEqual((h / "plugins/laib-journal/__init__.py").read_text(), (HOOK.parent / "journal_plugin.py").read_text())
                    self.assertIn("name: laib-journal", (h / "plugins/laib-journal/plugin.yaml").read_text())

    def test_the_tools_are_in_the_array_and_hermes_stock_skills_are_gone(self):
        """#502, measured on the Kraftwerk: behind Hermes' tool search every tool call took
        tool_search, tool_describe and tool_call, three extra model rounds, and the search
        once picked laib_look for a question for Frage. Hermes v0.22.2 cannot keep one MCP
        server eager (tools/tool_search.py: an mcp- toolset is always deferrable, the defer
        list only adds), so the search is off and every wired tool is in the array. Hermes'
        58 bundled skills do not fit a household: its own `hermes skills opt-out --remove`
        drops them, in the Hermes container, on every apply."""
        with tempfile.TemporaryDirectory() as d:
            env = dict(os.environ, LAIB_DATA_DIR=d, LAIB_RENDER_DIR=d + "/none", MODEL_URL="http://127.0.0.1:1/v1", SIGNAL_ACCOUNT="+490000",
                       MODEL_NAME="m", THINKING_MODEL="t", LAIB_SECRET_BOTE_LAIB_TOKEN="laib-tok")
            p = subprocess.run(["bash", str(HOOK)], env=env, capture_output=True, text=True, timeout=60)
            self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
            conf = Path(d, "bote/hermes/config.yaml").read_text()
        self.assertIn('tools:\n  tool_search:\n    enabled: "off"', conf, "the tools still hide behind tool_search")
        self.assertIn("laib-exec: systemd-bote-hermes hermes skills opt-out --remove --yes", p.stdout.splitlines())
        self.assertNotIn("idle_compact_after_seconds", conf, "a key the gateway never acts on (it resets the idle clock per turn)")

    def test_with_frage_the_world_is_asked_not_remembered(self):
        """#510: without thinking the model answered "Wie groß ist ein K8 Berg?" from
        memory, with a made-up table. With Frage wired the SOUL sends every question
        about the world to frage; without it the SOUL names no tool it lacks."""
        srv = HTTPServer(("127.0.0.1", 0), FakeHaushalt)
        threading.Thread(target=srv.serve_forever, daemon=True).start()
        self.addCleanup(srv.shutdown)
        for frage in ("", f"http://127.0.0.1:{srv.server_port}/mcp"):
            with tempfile.TemporaryDirectory() as d:
                env = dict(os.environ, LAIB_DATA_DIR=d, LAIB_RENDER_DIR=d + "/none", MODEL_URL="http://127.0.0.1:1/v1", SIGNAL_ACCOUNT="+490000",
                           MODEL_NAME="m", THINKING_MODEL="t", LAIB_SECRET_BOTE_LAIB_TOKEN="laib-tok", FRAGE_URL=frage)
                p = subprocess.run(["bash", str(HOOK)], env=env, capture_output=True, text=True, timeout=60)
                self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
                soul = Path(d, "bote/hermes/SOUL.md").read_text()
            self.assertEqual("immer mit frage, nie aus dem Gedächtnis" in soul, bool(frage), soul)
            self.assertIn("Keine Emojis", soul)

    def test_a_long_conversation_is_compressed_by_token_count(self):
        """#502, the owner's choice: Signal conversations run for days and drag the box trail into
        every call. Hermes' compression.threshold_tokens compresses once a prompt passes N tokens
        (the lower of N and the ratio threshold, agent/context_compressor.py). N is COMPACT_TOKENS,
        32000 by default; a value Hermes would drop in silence (not a positive integer) fails the hook."""
        def run(**values):
            with tempfile.TemporaryDirectory() as d:
                env = dict(os.environ, LAIB_DATA_DIR=d, LAIB_RENDER_DIR=d + "/none", MODEL_URL="http://127.0.0.1:1/v1", SIGNAL_ACCOUNT="+490000",
                           MODEL_NAME="m", THINKING_MODEL="t", LAIB_SECRET_BOTE_LAIB_TOKEN="laib-tok", **values)
                p = subprocess.run(["bash", str(HOOK)], env=env, capture_output=True, text=True, timeout=60)
                conf = Path(d, "bote/hermes/config.yaml")
                return p, conf.read_text() if conf.exists() else ""
        p, conf = run()
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertIn("compression:\n  threshold_tokens: 32000\n", conf)
        p, conf = run(COMPACT_TOKENS="40000")
        self.assertIn("compression:\n  threshold_tokens: 40000\n", conf)
        for bad in ("24k", "0", "-5"):
            p, _ = run(COMPACT_TOKENS=bad)
            self.assertNotEqual(p.returncode, 0, bad)
            self.assertIn("COMPACT_TOKENS", p.stderr)
        self.assertIn("COMPACT_TOKENS: 32000", (HOOK.parent / "values.example.yml").read_text())

    def hook(self, **values):
        with tempfile.TemporaryDirectory() as d:
            env = dict(os.environ, LAIB_DATA_DIR=d, LAIB_RENDER_DIR=d + "/none", MODEL_URL="http://127.0.0.1:1/v1", SIGNAL_ACCOUNT="+490000",
                       MODEL_NAME="m", THINKING_MODEL="t", LAIB_SECRET_BOTE_LAIB_TOKEN="laib-tok", **values)
            p = subprocess.run(["bash", str(HOOK)], env=env, capture_output=True, text=True, timeout=60)
            h = Path(d, "bote/hermes")
            read = lambda f: (h / f).read_text() if (h / f).exists() else ""
            return p, read("config.yaml"), read("SOUL.md")

    def test_the_laib_tools_trust_is_the_owners_value(self):
        """#524: the hook wrote trust: untrusted itself, so every template update undid the owner's
        trust: full. It is LAIB_TOOLS_TRUST now (a template update keeps a value), untrusted by default;
        any other word fails the hook rather than reach Hermes."""
        laib = lambda conf: conf.split("mcp_servers:\n  laib:\n", 1)[1].split("\n  ", 4)[:4]
        p, conf, _ = self.hook()
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertTrue(any(l.strip().startswith("trust: untrusted ") for l in laib(conf)), laib(conf))
        p, conf, _ = self.hook(LAIB_TOOLS_TRUST="full")
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertTrue(any(l.strip().startswith("trust: full ") for l in laib(conf)), laib(conf))
        for bad in ("Full", "ja", "untrusted full"):
            p, _, _ = self.hook(LAIB_TOOLS_TRUST=bad)
            self.assertNotEqual(p.returncode, 0, bad)
            self.assertIn("LAIB_TOOLS_TRUST", p.stderr)
        self.assertIn("\nLAIB_TOOLS_TRUST: untrusted ", (HOOK.parent / "values.example.yml").read_text())

    def test_a_short_sentence_with_a_device_is_a_command(self):
        """#520: without thinking, "Bambi aus" read as a statement and nothing switched. With ha wired
        the SOUL makes it a command; without ha it names no tool the Bote lacks (#54)."""
        p, _, soul = self.hook(LAIB_SECRET_BOTE_HA_TOKEN="bote-user-tok")
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertIn('sind Befehle: schalte sofort mit ha, ohne Rückfrage, dann ein Satz Bestätigung', soul)
        self.assertIn('"Bambi aus"', soul)
        _, _, soul = self.hook()
        self.assertNotIn("schalte sofort", soul)

    def test_a_fact_comes_from_frage_or_the_bote_says_it_does_not_know(self):
        """#587: "Wie groß ist ein k8 Berg" got invented valleys and heights, without a call to Frage.
        With Frage wired, names and numbers come only from its answer, with the source; without it
        the SOUL tells the Bote to say it does not know rather than answer from memory."""
        srv = HTTPServer(("127.0.0.1", 0), FakeHaushalt)
        threading.Thread(target=srv.serve_forever, daemon=True).start()
        self.addCleanup(srv.shutdown)
        _, _, soul = self.hook(FRAGE_URL=f"http://127.0.0.1:{srv.server_port}/mcp")
        self.assertIn("Namen und Zahlen nennst du nur aus einer Antwort von frage, mit ihrer Quelle", soul)
        _, _, soul = self.hook(FRAGE_URL="")
        self.assertNotIn("frage", soul.split("Du kennst:", 1)[1].split("\n", 1)[0], "the SOUL names Frage it lacks (#54)")
        self.assertIn("Du hast keine Websuche", soul)
        self.assertIn("sag, dass du es nicht weißt", soul)

    def test_a_persona_line_never_replaces_the_answer(self):
        """#587: the owner answered Laib's notice ("Dir sollte die Nummer bekannt sein") and got
        "AUF WIEDERSEHEN.", a line of the persona. The SOUL: a line of the figure never stands in for
        the answer, and a reply to one of Laib's notices is read with laib, not guessed."""
        _, _, soul = self.hook()
        self.assertIn("Ein Spruch der Figur ersetzt nie die Antwort", soul)
        self.assertIn("Antwortet jemand auf eine Mitteilung von Laib", soul)
        self.assertIn("box.yml people", soul)

    def test_an_escalation_is_offered_as_what_it_is_for_every_sender(self):
        """#863 (owner, 2026-10-28): an escalation through the Bote goes to the owner as a Box-Aufgabe,
        never into a public repo (the kernel files it so, whoever wrote). The SOUL is one for every
        sender, so it offers exactly that: handing the question to the admin, not asking Claude."""
        _, _, soul = self.hook()
        line = next(l for l in soul.splitlines() if "laib escalate" in l)
        self.assertIn("an den Admin weiterzugeben", line)
        self.assertIn("Box-Aufgabe", line)
        self.assertIn("verlässt das Haus", line)
        self.assertNotIn("Claude", soul)

    def test_the_soul_and_the_concept_agree_on_memory(self):
        """#863: the SOUL said the Bote remembers nothing beyond a conversation while KONZEPT.md
        promised a memory per number. Nothing is configured (README, "Memory"): both say so."""
        _, _, soul = self.hook()
        self.assertIn("Du kannst dir über ein Gespräch hinaus nichts merken", soul)
        konzept = (HOOK.parent.parent.parent / "docs" / "KONZEPT.md").read_text()
        self.assertNotIn("memory namespace", konzept)
        self.assertNotIn("Honcho", konzept)
        self.assertIn("nothing beyond a conversation", konzept)

    def test_an_answer_naming_who_a_number_is_becomes_a_proposal(self):
        """#596: the owner answers Laib's notice about a dropped sender with "+49… ist mama"; the
        SOUL has the Bote propose it with laib_person_signal, and the owner approves it."""
        _, _, soul = self.hook()
        self.assertIn("laib_person_signal", soul)
        self.assertIn("Inbox", soul.split("laib_person_signal", 1)[1].split("\n", 1)[0])

    def test_the_ha_tool_has_no_prompts_to_guess(self):
        """#587: the Bote called mcp__ha__get_prompt with made-up names (light_status, ha_status) and got
        "Unknown prompt" before GetLiveContext, a detour per question. Hermes' generated prompt tools for
        ha are off (mcp_servers.ha.tools.prompts, tools/mcp_tool_registration.py), the SOUL names
        GetLiveContext for the state; without ha neither is there."""
        _, conf, soul = self.hook(LAIB_SECRET_BOTE_HA_TOKEN="bote-user-tok")
        ha = conf.split("\n  ha:\n", 1)[1].split("\n  automat:", 1)[0].split("\n# ", 1)[0]
        self.assertRegex(ha, r"\n    tools: +(#[^\n]*)?\n      prompts: false\n      resources: false")
        self.assertIn("GetLiveContext", soul)
        self.assertIn("Prompt-Namen", soul)
        _, conf, soul = self.hook()
        self.assertNotIn("GetLiveContext", soul)

    def test_hermes_is_pinned(self):
        """#471, ADR 0019: a stock update must not change how Hermes treats tool output unseen."""
        vals = (HOOK.parent / "values.example.yml").read_text()
        self.assertIn("HERMES_IMAGE: docker.io/nousresearch/hermes-agent:v0.22.2", vals)


if __name__ == "__main__":
    unittest.main()
