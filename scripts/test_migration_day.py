"""#350: scripts/migration-day.sh, docs/MIGRATION.md run as one resumable
script on the box, against fakes.

podman, systemctl, loginctl, systemd-run, curl and laib are shims that record
their argv into one calls file; laib keeps a fake state (services/<name>) and
fails `laib add <name>` when told to. The migration's own tools
(install-box.sh, migrate-servicebay.py, carry-servicebay-secrets.sh,
move-servicebay-data.sh) are replaced by recorders through the script's
overrides. The script is run as the operator runs it, as a process."""
import json, os, stat, subprocess, tempfile, time, unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent
SCRIPT = HERE / "migration-day.sh"


def exe(path, body):
    path.write_text(body)
    path.chmod(path.stat().st_mode | stat.S_IEXEC)


RECORD = 'printf "%s %s\\n" "$(basename "$0")" "$*" >> "$FAKE/calls"\n'


class MigrationDay(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        t = Path(self.tmp.name)
        self.t = t
        for d in ("bin", "q", "mig", "data", "sb", "state/services", "out", "home"):
            (t / d).mkdir(parents=True)
        self.calls = t / "calls"
        self.calls.write_text("")
        # ServiceBay's Quadlet files as the box has them (a .kube and its .yml per service)
        for u in ("auth", "radicale", "mosquitto", "vaultwarden", "nginx"):
            (t / "q" / f"{u}.kube").write_text(f"[Kube]\nYaml={u}.yml\n")
            (t / "q" / f"{u}.yml").write_text("kind: Pod\n")
        (t / "q" / "servicebay.container").write_text("[Container]\n")
        self.backup(hours_ago=2)
        b = t / "bin"
        exe(b / "podman", "#!/bin/bash\n" + RECORD + r'''
case "$1" in
  exec) case "$*" in *package.json*) echo "${FAKE_SB_VERSION:-5.44.3}";; esac; exit 0;;
  container) [ "$2" = inspect ] && echo true; exit 0;;
  cp) mkdir -p "$3/x"; exit 0;;
  unshare) shift; "$@"; exit $?;;
esac
exit 0
''')
        exe(b / "systemctl", "#!/bin/bash\n" + RECORD + r'''
case "$*" in *is-active*laib-migration*) [ -e "$FAKE/unit-running" ]; exit $?;; esac
case "$*" in *is-active*) exit 0;; esac
exit 0
''')
        exe(b / "loginctl", "#!/bin/bash\n" + RECORD + 'echo "${FAKE_LINGER:-yes}"\n')
        exe(b / "systemd-run", "#!/bin/bash\n" + RECORD)
        exe(b / "curl", "#!/bin/bash\n" + RECORD + 'echo 200\n')
        exe(b / "laib", "#!/bin/bash\n" + RECORD + r'''
case "$1" in
  version) echo "laib ${FAKE_LAIB_VERSION:-v0.6.0}";;
  add) [ "$2" = "${FAKE_FAIL_ADD:-}" ] && { echo "apply: rot"; exit 1; }; mkdir -p "$LAIB_STATE/services/$2";;
  remove) rm -rf "$LAIB_STATE/services/$2";;
  secret) [ "$2" = list ] && printf '%s\n' ${FAKE_SECRETS:-};;
esac
exit 0
''')
        exe(b / "install-box", "#!/bin/bash\n" + RECORD)
        exe(b / "carry", "#!/bin/bash\n" + RECORD)
        exe(b / "move", "#!/bin/bash\n" + RECORD + r'''
dry=""; [ "$1" = --dry-run ] && { dry=1; shift; }
[ -n "$dry" ] || { mkdir -p "$DATA_DIR/$1/laib-copy"; echo "$1: copy $DATA_DIR/$1/old -> $DATA_DIR/$1/laib-copy"; }
''')
        # migrate-servicebay.py: the lines MIGRATION.md's step 1 prints, and a draft per service
        exe(b / "migrate", "#!/bin/bash\n" + RECORD + r'''
for s in auth radicale mosquitto vaultwarden; do mkdir -p "$OUT/$s"; printf 'routes:\n  - {host: %s, port: 1}\n' "$s" > "$OUT/$s/laib.yml"; done
echo "ServiceBay still runs: scripts/carry-servicebay-secrets.sh auth mosquitto"
echo "laib add, once ServiceBay's unit is out"
echo "  laib add auth BASE_DN=dc=box,dc=test"
echo "  laib add radicale"
echo "  laib add mosquitto"
echo "  laib add vaultwarden"
''')

    def tearDown(self):
        self.tmp.cleanup()

    def backup(self, hours_ago, status="partial"):
        when = time.strftime("%Y-%m-%dT%H:%M:%S.000Z", time.gmtime(time.time() - hours_ago * 3600))
        cfg = {"reverseProxy": {"publicDomain": "box.test"},
               "externalBackup": {"enabled": True, "lastRun": when, "lastStatus": status, "servicesIncomplete": ["file-share"]},
               "installedSecrets": [{"varName": "X", "password": "never-printed-value"}]}
        (self.t / "sb" / "config.json").write_text(json.dumps(cfg))

    def run_md(self, *args, **env):
        t = self.t
        e = dict(os.environ, PATH=f"{t / 'bin'}:{os.environ['PATH']}", FAKE=str(t), HOME=str(t / "home"),
                 MIG_USER=os.environ.get("USER") or subprocess.run(["id", "-un"], capture_output=True, text=True).stdout.strip(),
                 Q=str(t / "q"), MIG_DIR=str(t / "mig"), DATA_DIR=str(t / "data"), SB_DATA=str(t / "sb"),
                 LAIB_BIN=str(t / "bin" / "laib"), LAIB_STATE=str(t / "state"), OUT=str(t / "out" / "migration"),
                 INSTALL_BOX=str(t / "bin" / "install-box"), MIGRATE_PY=str(t / "bin" / "migrate"), PYTHON="",
                 CARRY=str(t / "bin" / "carry"), MOVE=str(t / "bin" / "move"),
                 CONTAINERENV=str(t / "no-containerenv"), LAIB_VERSION="v0.6.0", MIN_FREE_GB="0", LAIB_RETRY_SLEEP="0")
        e.update(env)
        return subprocess.run(["bash", str(SCRIPT), *args], capture_output=True, text=True, env=e, timeout=120)

    def calls_of(self, prog):
        return [l.split(" ", 1)[1] if " " in l else "" for l in self.calls.read_text().splitlines() if l.split(" ", 1)[0] == prog]

    def progress(self):
        p = self.t / "mig" / "progress"
        last = {}
        for line in p.read_text().splitlines() if p.exists() else []:
            step, status = line.split()[:2]
            last[step] = status
        return last

    def changed_nothing(self):
        self.assertEqual(self.calls_of("laib"), [c for c in self.calls_of("laib") if c.split()[0] in ("version", "plan", "status", "secret")])
        self.assertFalse([c for c in self.calls_of("systemctl") if c.split()[1:2] in (["stop"], ["start"])], self.calls_of("systemctl"))
        self.assertEqual(self.calls_of("install-box"), [])
        self.assertTrue((self.t / "q" / "auth.kube").exists())

    # --- the backup is the first precondition

    def test_refuses_without_a_fresh_backup(self):
        self.backup(hours_ago=40)
        r = self.run_md("--foreground")
        self.assertNotEqual(r.returncode, 0, r.stdout)
        self.assertIn("no fresh off-box backup", r.stdout + r.stderr)
        self.assertIn("--no-fresh-backup", r.stdout + r.stderr)
        self.changed_nothing()

    def test_a_failed_backup_is_no_backup(self):
        self.backup(hours_ago=1, status="error")
        r = self.run_md("--foreground", "--until", "preflight")
        self.assertNotEqual(r.returncode, 0, r.stdout)
        self.assertIn("no fresh off-box backup", r.stdout + r.stderr)

    def test_no_fresh_backup_is_the_owners_explicit_choice(self):
        self.backup(hours_ago=40)
        r = self.run_md("--foreground", "--no-fresh-backup", "--until", "preflight")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertIn("--no-fresh-backup", r.stdout)
        self.assertEqual(self.progress(), {"preflight": "green"})

    def test_the_backup_check_never_prints_a_secret(self):
        r = self.run_md("--dry-run")
        self.assertNotIn("never-printed-value", r.stdout + r.stderr)

    # --- dry run

    def test_dry_run_prints_every_step_and_changes_nothing(self):
        r = self.run_md("--dry-run")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        for step in ("preflight", "install", "drafts", "secrets", "auth", "radicale", "vaultwarden", "caddy", "retire"):
            self.assertIn(step, r.stdout)
        self.assertIn("laib add auth BASE_DN=dc=box,dc=test", r.stdout)   # the drafts, into a scratch dir
        self.changed_nothing()
        self.assertFalse((self.t / "mig" / "progress").exists())

    def test_dry_run_reports_every_failed_precondition(self):
        self.backup(hours_ago=40)
        r = self.run_md("--dry-run", FAKE_SB_VERSION="5.40.0", FAKE_LINGER="no", FAKE_LAIB_VERSION="v0.5.0")
        self.assertNotEqual(r.returncode, 0)
        out = r.stdout + r.stderr
        for want in ("no fresh off-box backup", "5.40.0", "linger", "v0.5.0"):
            self.assertIn(want, out)
        self.changed_nothing()

    def test_refuses_inside_a_container(self):
        (self.t / "containerenv").write_text("")
        r = self.run_md("--dry-run", CONTAINERENV=str(self.t / "containerenv"))
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("on the host", r.stdout + r.stderr)

    # --- the real run

    def test_default_runs_in_a_transient_unit_that_outlives_ssh(self):
        r = self.run_md("--until", "auth")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        runs = self.calls_of("systemd-run")
        self.assertEqual(len(runs), 1, runs)
        self.assertIn("--user", runs[0])
        self.assertIn("--unit=laib-migration", runs[0])
        self.assertIn("--foreground --until auth", runs[0])
        self.assertIn("journalctl --user -u laib-migration", r.stdout)
        self.changed_nothing()   # the unit does the work, not this call

    def test_until_stops_after_that_step(self):
        r = self.run_md("--foreground", "--until", "auth")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertEqual(self.progress(), {s: "green" for s in ("preflight", "install", "drafts", "secrets", "auth")})
        self.assertIn("auth BASE_DN=dc=box,dc=test", self.calls_of("laib"))
        self.assertEqual(self.calls_of("carry"), ["auth mosquitto"])
        self.assertFalse((self.t / "q" / "auth.kube").exists())
        self.assertTrue((self.t / "home" / "servicebay-units" / "auth.kube").exists())
        self.assertTrue((self.t / "q" / "radicale.kube").exists())   # not reached

    def test_red_step_stops_and_hands_the_service_back(self):
        r = self.run_md("--foreground", "--until", "vaultwarden", FAKE_FAIL_ADD="radicale")
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("RED at radicale", r.stdout)
        self.assertIn("--resume", r.stdout)
        p = self.progress()
        self.assertEqual(p["auth"], "green")
        self.assertEqual(p["radicale"], "red")
        self.assertNotIn("mosquitto", p)                       # nothing after the first red step
        self.assertTrue((self.t / "q" / "radicale.kube").exists())   # ServiceBay's files back
        self.assertIn("--user start radicale.service", self.calls_of("systemctl"))
        self.assertTrue((self.t / "q" / "mosquitto.kube").exists())

    def test_resume_continues_after_the_last_green_step(self):
        self.run_md("--foreground", "--until", "vaultwarden", FAKE_FAIL_ADD="radicale")
        self.calls.write_text("")
        r = self.run_md("--foreground", "--resume", "--until", "vaultwarden")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        laib = self.calls_of("laib")
        self.assertFalse([c for c in laib if c.startswith("add auth")], laib)   # green: not again
        self.assertEqual(self.calls_of("install-box"), [])
        self.assertIn("add radicale", laib)
        self.assertIn("add mosquitto MQTT_USER=mqtt", laib)   # the box's broker user (MIGRATION.md)
        self.assertEqual(self.progress()["vaultwarden"], "green")
        # the copy the red attempt made is stale (ServiceBay ran on): gone before the move again
        self.assertIn("removed the stale copy", r.stdout)

    def test_a_second_run_without_resume_is_refused(self):
        self.run_md("--foreground", "--until", "preflight")
        r = self.run_md("--foreground", "--until", "auth")
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("--resume", r.stdout + r.stderr)
        self.assertEqual(self.progress(), {"preflight": "green"})

    def test_rollback_hands_one_service_back_and_resume_takes_it_again(self):
        self.run_md("--foreground", "--until", "vaultwarden")
        r = self.run_md("rollback", "vaultwarden")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertTrue((self.t / "q" / "vaultwarden.kube").exists())
        self.assertIn("remove vaultwarden", self.calls_of("laib"))
        self.assertEqual(self.progress()["vaultwarden"], "rolledback")
        r = self.run_md("--foreground", "--resume", "--until", "vaultwarden")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertEqual(self.progress()["vaultwarden"], "green")
        self.assertIn("removed the stale copy", r.stdout)

    def test_a_service_the_box_does_not_run_is_skipped(self):
        r = self.run_md("--foreground", "--until", "beets")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertEqual(self.progress()["beets"], "skip")
        self.assertEqual(self.progress()["adguard"], "skip")

    def test_unknown_step_is_refused(self):
        r = self.run_md("--dry-run", "--until", "vaultwardn")
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("unknown step", r.stdout + r.stderr)


if __name__ == "__main__":
    unittest.main()
