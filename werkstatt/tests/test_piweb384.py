"""#384: pi in the browser again. pi-web-ui runs in the dev container as
dev on a port the host publishes on loopback only, Caddy routes pi.<domain>
to it behind Authelia (admins, two factors), and its pi has what every pi
in the Werkstatt has: the laib-llama provider, the guide, Laib as the pi
hand. The real image against a fake model is scripts/werkstatt-model.sh."""
import json, os, re, subprocess, tempfile, unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent.parent          # werkstatt/
TPL = HERE.parent / "templates" / "werkstatt"
EXT = "/opt/werkstatt/extensions/laib-llama.js"


class Launcher(unittest.TestCase):
    def launch(self, home, **env):
        """werkstatt/piweb.sh with a fake pi-web-ui that records its env and argv"""
        bin_ = home.parent / "bin"
        bin_.mkdir(exist_ok=True)
        fake = bin_ / "pi-web-ui"
        fake.write_text('#!/usr/bin/env python3\nimport json, os, sys\n'
                        'json.dump({"argv": sys.argv[1:], "env": dict(os.environ)}, open(os.environ["RECORD"], "w"))\n')
        fake.chmod(0o755)
        guide = home.parent / "AGENTS.md"
        guide.write_text("# the Werkstatt's house rules\n")
        record = home.parent / "record.json"
        e = dict(os.environ, HOME=str(home), PATH=f"{bin_}:{os.environ['PATH']}", RECORD=str(record),
                 PI_LAIB_GUIDE=str(guide), WERKSTATT_PI_WEB_ENV=str(home.parent / "none"), PI_WEB_CWD=str(home),
                 WERKSTATT_DOMAIN="dopp.test", LAIB_TOKEN="laib_pi_canary384", PI_WEB_TOKEN="x", CLAUDE_CODE_OAUTH_TOKEN="sk-canary384")
        e.update(env)
        e.pop("PI_CODING_AGENT_DIR", None)
        p = subprocess.run(["bash", str(HERE / "piweb.sh")], env=e, capture_output=True, text=True, timeout=30)
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        return json.loads(record.read_text())

    def test_the_web_pi_has_the_provider_the_guide_and_laib(self):
        with tempfile.TemporaryDirectory() as d:
            home = Path(d) / "home"
            agent = home / ".pi" / "web"
            agent.mkdir(parents=True)
            # what the web UI saved there before stays
            (agent / "settings.json").write_text(json.dumps({"defaultModel": "gemma-4-e4b", "extensions": ["/mine.js"]}))
            got = self.launch(home)
            settings = json.loads((agent / "settings.json").read_text())
            self.assertEqual(settings["extensions"], [EXT, "/mine.js"])
            self.assertEqual(settings["defaultModel"], "gemma-4-e4b")
            self.assertEqual((agent / "APPEND_SYSTEM.md").read_text(), "# the Werkstatt's house rules\n")
            laib = json.loads((agent / "mcp.json").read_text())["mcpServers"]["laib"]
            self.assertEqual(laib["headers"]["Authorization"], "Bearer ${LAIB_TOKEN}", "the header names the variable")
            self.launch(home)                                  # a restart adds nothing twice
            self.assertEqual(json.loads((agent / "settings.json").read_text())["extensions"], [EXT, "/mine.js"])
            for f in agent.rglob("*"):
                self.assertNotIn("canary384", f.read_text(), f"a token landed in {f.name}, in the kept home")
            env = got["env"]
            self.assertEqual(got["argv"], ["--no-browser"])
            self.assertEqual(env["PI_CODING_AGENT_DIR"], str(agent))
            self.assertEqual(env["PI_WEB_PORT"], "8791")
            self.assertEqual(env["PI_WEB_HOST"], "0.0.0.0", "podman forwards to the container's address, not its loopback")
            self.assertEqual(env["PI_WEB_ALLOW_HOSTS"].split(",")[0], "pi.dopp.test")
            self.assertEqual(env["PI_WEB_ALLOW_ORIGINS"], "https://pi.dopp.test")
            self.assertNotIn("PI_WEB_TOKEN", env, "Authelia is the gate, not a shared password")
            self.assertNotIn("CLAUDE_CODE_OAUTH_TOKEN", env, "the Claude login reached pi")
            self.assertEqual(env["LAIB_TOKEN_FILE"], "/run/laib/pi-token", "`laib` in pi's shell would speak as claude")

    def test_no_domain_no_start(self):
        with tempfile.TemporaryDirectory() as d:
            home = Path(d) / "home"
            home.mkdir()
            p = subprocess.run(["bash", str(HERE / "piweb.sh")], capture_output=True, text=True, timeout=30,
                               env=dict(os.environ, HOME=str(home), WERKSTATT_DOMAIN="", PI_LAIB_GUIDE=str(HERE / "guide" / "AGENTS.md"),
                                        WERKSTATT_PI_WEB_ENV=str(home / "none"), PATH="/usr/bin:/bin"))
            self.assertNotEqual(p.returncode, 0)


class Wiring(unittest.TestCase):
    def test_the_route_is_admins_only_with_two_factors(self):
        contract = (TPL / "laib.yml").read_text()
        self.assertRegex(contract, r"(?m)^  - host: pi\n    port: 8791\n    exposure: public\n    auth: two_factor\n    group: admins")
        self.assertRegex(contract, r"\{port: 8791, lan: false\}")
        unit = (TPL / "werkstatt.container").read_text()
        self.assertIn("PublishPort=127.0.0.1:8791:8791", unit, "the web pi on the LAN, past Authelia")
        for line in ("Environment=WERKSTATT_DOMAIN=${DOMAIN}", "Environment=LAIB_PI_TOKEN=${LAIB_PI_TOKEN}", "Environment=MODEL_KEY=${MODEL_KEY}"):
            self.assertIn(line, unit)

    def test_the_image_carries_pi_web_ui_and_the_entrypoint_keeps_it_running(self):
        cf = (HERE / "Containerfile").read_text()
        self.assertRegex(cf, r"pi-web-ui@\$\{PI_WEB_VERSION\}")
        self.assertRegex(cf, r"COPY piweb\.sh /usr/local/bin/werkstatt-pi-web\b")
        entry = (HERE / "entrypoint.sh").read_text()
        self.assertIn("werkstatt-pi-web", entry)
        self.assertIn("/run/laib/pi-token", entry)
        self.assertLess(entry.find("werkstatt-pi-web"), entry.find("exec /usr/sbin/sshd"))


if __name__ == "__main__":
    unittest.main()
