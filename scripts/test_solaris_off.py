"""#540: scripts/solaris-off.py takes Solaris off the box (Phase 5, ADR 0015):
its units out of the Quadlet root into a dated park dir, its host units
disabled, ignore.yml's lines out with one state commit, Stimme added and
Home Assistant applied; what stays the owner's step (HA entries that point
at Solaris, a :8787 line, the data after 30 days) is listed. systemctl and
laib are recorders, git is real; the script runs as the operator runs it."""
import json, os, socket, stat, subprocess, sys, tempfile, unittest
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent / "solaris-off.py"
RECORD = 'printf "%s %s\\n" "$(basename "$0")" "$*" >> "$FAKE/calls"\n'
SECRET = "-a-token-never-printed-7Q"


def exe(path, body):
    path.write_text(body)
    path.chmod(path.stat().st_mode | stat.S_IEXEC)


class SolarisOff(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        t = self.t = Path(self.tmp.name)
        for d in ("bin", "home", "q", "units/default.target.wants", "sbpark", "data/solaris", "data/solaris-whisper", "data/immich",
                  "state/services/home-assistant", "state/services/radicale", "ha/.storage", "ha/packages"):
            (t / d).mkdir(parents=True)
        self.socks = []
        for _ in range(3):   # Stimme's three ports, free until a test makes them answer
            sk = socket.socket()
            sk.bind(("127.0.0.1", 0))
            self.socks.append(sk)
            self.addCleanup(sk.close)
        self.ports = [sk.getsockname()[1] for sk in self.socks]
        # the Kraftwerk as #540 found it: Solaris' ServiceBay units in the Quadlet root, ignored; chronik stays
        for u in ("solaris", "solaris-tts", "solaris-whisper", "daggerheart-chronik"):
            (t / "q" / f"{u}.kube").write_text(f"[Kube]\nYaml={u}.yml\n")
            (t / "q" / f"{u}.yml").write_text("kind: Pod\n")
        (t / "q" / "radicale").mkdir()   # a Laib service's folder
        (t / "units" / "solaris-radicale-rights.timer").write_text("[Timer]\n")
        (t / "units" / "solaris-radicale-rights.service").write_text("[Service]\n")
        (t / "units" / "solaris-llama-policy.service").write_text("[Service]\n")
        (t / "units" / "laib-lease.service").write_text("[Service]\n")
        (t / "sbpark" / "solaris-import-google.kube").write_text("")
        ignore = "units:\n" + "".join(f"  - {u}.{x}\n" for u in ("solaris", "solaris-tts", "solaris-whisper", "daggerheart-chronik") for x in ("kube", "yml"))
        (t / "state" / "ignore.yml").write_text(ignore)
        (t / "state" / "box.yml").write_text("domain: box.test\nlanIp: 192.168.1.10\nnotify:\n  url: http://127.0.0.1:8787/api/laib-events\n")
        git = lambda *a: subprocess.run(["git", "-C", str(t / "state"), "-c", "user.name=t", "-c", "user.email=t@t", *a], check=True, capture_output=True)
        git("init", "-q")
        git("add", "-A")
        git("commit", "-q", "-m", "init")
        (t / "ha" / "configuration.yaml").write_text(
            "homeassistant:\n# >>> solaris notify.solaris — managed by the post-deploy >>>\nnotify:\n  - name: solaris\n"
            "    platform: rest\n    resource: \"http://127.0.0.1:8787/api/ha/notify\"\n# <<< solaris notify.solaris <<<\n")
        w, p, ww = self.ports
        self.entries([
            ("wyoming", "faster-whisper", {"host": "127.0.0.1", "port": w}),          # Solaris' Whisper, Stimme's port: stays
            ("wyoming", "openwakeword", {"host": "localhost", "port": ww}),            # the same
            ("wyoming", "piper", {"host": "127.0.0.1", "port": 10203}),               # Solaris' speech
            ("wyoming", "satellite", {"host": "127.0.0.1", "port": 10700}),
            ("wyoming", "Küche", {"host": "192.168.1.77", "port": 10700}),            # a real satellite on the LAN: not Solaris'
            ("custom", "Sol", {"token": SECRET}),
            ("custom", "Solaris", {"api_key": SECRET}),
            ("ollama", "http://127.0.0.1:8787/ollama", {"url": f"http://user:{SECRET}@127.0.0.1:8787/ollama"}),
            ("openai_streaming", "Assist LLM", {"base_url": "http://127.0.0.1:11434/v1", "api_key": SECRET}),
            ("wyoming", "solilos-gatekeeper-asr", {"host": "127.0.0.1", "port": 10301}),
            ("ollama", "http://127.0.0.1:8710", {"url": "http://127.0.0.1:8710"}),   # Frage
            ("forecast_solar", "Forecast.Solar", {"latitude": 50.0}),
            ("mqtt", "Mosquitto", {"broker": "127.0.0.1", "password": SECRET}),
        ])
        b = t / "bin"
        exe(b / "systemctl", "#!/bin/bash\n" + RECORD + r'''
case "$*" in *is-active*solaris.service*|*is-active*solaris-tts.service*) exit 0;; *is-active*) exit 3;; esac
case "$*" in *is-enabled*solaris-radicale-rights.timer*) exit 0;; *is-enabled*) exit 1;; esac
exit 0
''')
        exe(b / "laib", "#!/bin/bash\n" + RECORD + r'''
case "$1" in
  add) [ -n "${FAKE_FAIL_ADD:-}" ] && { echo "apply: rot"; exit 1; }; mkdir -p "$LAIB_STATE/services/$2";;
esac
exit 0
''')

    def entries(self, rows):
        e = [{"entry_id": f"e{i}", "domain": d, "title": ti, "data": data} for i, (d, ti, data) in enumerate(rows)]
        (self.t / "ha" / ".storage" / "core.config_entries").write_text(json.dumps({"version": 1, "data": {"entries": e}}))

    def run_off(self, *args, **env):
        t = self.t
        e = dict(os.environ, PATH=f"{t / 'bin'}:{os.environ['PATH']}", FAKE=str(t), HOME=str(t / "home"), USER="core",
                 Q=str(t / "q"), UNITS=str(t / "units"), SB_PARK=str(t / "sbpark"), OFF_DIR=str(t / "home" / "solaris-off"),
                 DATA_DIR=str(t / "data"), LAIB_HOME=str(t), LAIB_STATE=str(t / "state"), LAIB_BIN=str(t / "bin" / "laib"),
                 CONTAINERENV=str(t / "no-containerenv"), STIMME_PORTS=",".join(map(str, self.ports)), LAIB_RETRY_SLEEP="0")
        e.pop("LAIB_ACTOR", None)
        e.update(env)
        # HA's config lives under DATA_DIR/home-assistant/homeassistant
        ha = t / "data" / "home-assistant" / "homeassistant"
        if not ha.exists():
            ha.parent.mkdir(parents=True, exist_ok=True)
            ha.symlink_to(t / "ha")
        r = subprocess.run([sys.executable, "-I", str(SCRIPT), *args], capture_output=True, text=True, env=e, timeout=60)
        self.assertNotIn(SECRET, r.stdout + r.stderr, "a value from HA's config entries in the output")
        return r

    def calls(self, prog=None):
        f = self.t / "calls"
        lines = f.read_text().splitlines() if f.exists() else []
        return [l.split(" ", 1)[1] for l in lines if prog is None or l.split(" ", 1)[0] == prog]

    def listen(self):
        for sk in self.socks:
            sk.listen(8)

    def test_the_default_changes_nothing_and_names_what_is_left(self):
        before = sorted(p.name for p in (self.t / "q").iterdir())
        r = self.run_off()
        self.assertEqual(r.returncode, 1, r.stdout + r.stderr)
        self.assertEqual(sorted(p.name for p in (self.t / "q").iterdir()), before)
        self.assertEqual([c for c in self.calls("systemctl") if "is-" not in c], [], "the status stopped or disabled something")
        self.assertEqual(self.calls("laib"), [])
        out = r.stdout
        for name in ("solaris.kube", "solaris-tts.yml", "solaris-radicale-rights.timer", "solaris-llama-policy.service", "ignore.yml: - solaris.kube"):
            self.assertIn(name, out)
        self.assertNotIn("daggerheart", out)
        self.assertNotIn("laib-lease", out)
        self.assertIn("solaris-import-google.kube", out, "the units migration day parked are not named")
        self.assertIn("Stimme is not Laib's", out)
        left = [l for l in out.splitlines() if "Home Assistant:" in l]
        for title in ('"piper"', '"satellite"', '"Sol"', '"Solaris"', '"http://127.0.0.1:8787/ollama"', '"Assist LLM"', '"solilos-gatekeeper-asr"'):
            self.assertTrue(any(title in l for l in left), (title, left))
        for title in ('"faster-whisper"', '"openwakeword"', '"Küche"', '"http://127.0.0.1:8710"', '"Forecast.Solar"', '"Mosquitto"'):
            self.assertFalse(any(title in l for l in left), (title, left))
        self.assertIn("configuration.yaml:6", out)
        self.assertIn("box.yml:4", out)
        self.assertIn("podman unshare rm -rf " + str(self.t / "data" / "solaris-whisper"), out)
        self.assertNotIn("immich", out)

    def test_do_takes_solaris_off_adds_stimme_and_a_second_run_changes_nothing(self):
        r = self.run_off("--do")
        log = r.stdout + r.stderr
        self.assertEqual(r.returncode, 1, log)   # HA's entries and the :8787 lines are the owner's
        q = sorted(p.name for p in (self.t / "q").iterdir())
        self.assertEqual(q, ["daggerheart-chronik.kube", "daggerheart-chronik.yml", "radicale"])
        park = next((self.t / "home" / "solaris-off").iterdir())
        self.assertEqual(sorted(p.name for p in park.iterdir() if p.is_file()),
                         sorted(["rollback.sh"] + [f"{u}.{x}" for u in ("solaris", "solaris-tts", "solaris-whisper") for x in ("kube", "yml")]))
        self.assertEqual(sorted(p.name for p in (park / "host").iterdir()),
                         ["solaris-llama-policy.service", "solaris-radicale-rights.service", "solaris-radicale-rights.timer"])
        self.assertTrue((self.t / "units" / "laib-lease.service").exists())
        sysd = self.calls("systemctl")
        for c in ("--user stop solaris.service", "--user stop solaris-whisper.service", "--user disable --now solaris-radicale-rights.timer",
                  "--user disable --now solaris-llama-policy.service", "--user daemon-reload"):
            self.assertIn(c, sysd)
        self.assertLess(sysd.index("--user disable --now solaris-radicale-rights.timer"), sysd.index("--user disable --now solaris-radicale-rights.service"),
                        "the timer would start its service again")
        self.assertEqual((self.t / "state" / "ignore.yml").read_text(), "units:\n  - daggerheart-chronik.kube\n  - daggerheart-chronik.yml\n")
        g = subprocess.run(["git", "-C", str(self.t / "state"), "log", "-1", "--format=%an|%s", "--name-only"], capture_output=True, text=True).stdout
        self.assertIn("human:core|solaris off (#540)", g)
        self.assertIn("ignore.yml", g)
        self.assertEqual(subprocess.run(["git", "-C", str(self.t / "state"), "status", "--porcelain"], capture_output=True, text=True).stdout, "")
        self.assertEqual(self.calls("laib"), ["add stimme", "apply home-assistant"])
        rb = (park / "rollback.sh").read_text()
        for line in (f"mv {park / 'solaris.kube'} {self.t / 'q'}/", f"mv {park / 'host' / 'solaris-radicale-rights.timer'} {self.t / 'units'}/",
                     "systemctl --user enable --now solaris-radicale-rights.timer", "systemctl --user start solaris.service",
                     "laib ignore solaris.yml", "laib remove stimme"):
            self.assertIn(line, rb)
        self.assertNotIn("enable --now solaris-llama-policy.service", rb, "a unit that was disabled comes back enabled")
        self.assertIn(f"bash {park / 'rollback.sh'}", r.stdout)
        self.assertIn("Home Assistant:", r.stdout)
        # again: nothing moves, Stimme is there; HA is applied again (its hook is idempotent)
        (self.t / "calls").write_text("")
        r = self.run_off("--do")
        self.assertEqual(r.returncode, 1, r.stdout)
        self.assertEqual([c for c in self.calls("systemctl") if "is-" not in c], [])
        self.assertEqual(self.calls("laib"), ["apply home-assistant"])
        self.assertEqual(len(list((self.t / "home" / "solaris-off").iterdir())), 1)
        # the owner deleted Solaris' entries in HA and the :8787 lines; Stimme answers: nothing left
        w, p, ww = self.ports
        self.entries([("wyoming", "faster-whisper", {"host": "127.0.0.1", "port": w}), ("wyoming", "piper", {"host": "127.0.0.1", "port": p}),
                      ("wyoming", "openwakeword", {"host": "127.0.0.1", "port": ww}), ("wyoming", "Küche", {"host": "192.168.1.77", "port": 10700})])
        (self.t / "ha" / "configuration.yaml").write_text("homeassistant:\n")
        (self.t / "state" / "box.yml").write_text("domain: box.test\n")
        self.listen()
        r = self.run_off()
        self.assertEqual(r.returncode, 0, r.stdout)
        self.assertIn("nothing of Solaris is left", r.stdout)

    def test_a_port_someone_else_serves_stops_before_laib_add(self):
        self.listen()
        r = self.run_off("--do")
        self.assertEqual(r.returncode, 1)
        self.assertIn("RED: Stimme's ports still answer", r.stdout)
        self.assertNotIn("add stimme", self.calls("laib"))

    def test_a_red_laib_add_is_red(self):
        r = self.run_off("--do", FAKE_FAIL_ADD="1")
        self.assertEqual(r.returncode, 1)
        self.assertIn("RED: laib add stimme is red", r.stdout)
        self.assertNotIn("apply home-assistant", self.calls("laib"))

    def test_not_on_the_host_and_not_an_agent(self):
        (self.t / "containerenv").write_text("")
        r = self.run_off("--do", CONTAINERENV=str(self.t / "containerenv"))
        self.assertEqual(r.returncode, 2)
        self.assertEqual(self.calls(), [])
        r = self.run_off("--do", LAIB_ACTOR="claude")
        self.assertEqual(r.returncode, 1)
        self.assertIn("only a human", r.stdout)
        self.assertTrue((self.t / "q" / "solaris.kube").exists())

    def test_unreadable_ha_entries_cannot_be_checked(self):
        # nothing of Solaris left on the box, but HA's config entries are not readable: 2, not 0
        for p in (self.t / "q").glob("solaris*"):
            p.unlink()
        for p in (self.t / "units").glob("solaris*"):
            p.unlink()
        (self.t / "state" / "ignore.yml").write_text("units:\n")
        (self.t / "state" / "services" / "stimme").mkdir()
        (self.t / "state" / "box.yml").write_text("domain: box.test\n")
        (self.t / "ha" / "configuration.yaml").write_text("")
        (self.t / "ha" / ".storage" / "core.config_entries").unlink()
        self.listen()
        r = self.run_off()
        self.assertEqual(r.returncode, 2, r.stdout)
        self.assertIn("not readable", r.stdout)


if __name__ == "__main__":
    unittest.main()
