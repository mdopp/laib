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
# the checkout's release, as the preflight reads it (#366): a release-please bump keeps these green
VERSION = "v" + (HERE.parent / "version.txt").read_text().strip()


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
  version) echo "laib ${FAKE_LAIB_VERSION:-$LAIB_VERSION}";;
  add) [ "$2" = "${FAKE_FAIL_ADD:-}" ] && { echo "apply: rot"; exit 1; }; mkdir -p "$LAIB_STATE/services/$2";;
  remove) rm -rf "$LAIB_STATE/services/$2";;
  secret) [ "$2" = list ] && printf '%s\n' ${FAKE_SECRETS:-}; [ "$2" = set ] && cat > "$FAKE/secret-$3";;
esac
exit 0
''')
        exe(b / "install-box", "#!/bin/bash\n" + RECORD)
        exe(b / "carry", "#!/bin/bash\n" + RECORD + r'''
[ "$1" = --login ] && [ "$2" = "${FAKE_LOGIN_RED:-}" ] && { echo "$2: its password meldet sich nicht an"; exit 1; }
exit 0
''')
        exe(b / "move", "#!/bin/bash\n" + RECORD + r'''
dry=""; [ "$1" = --dry-run ] && { dry=1; shift; }
[ -n "$dry" ] || { mkdir -p "$DATA_DIR/$1/laib-copy"; echo "$1: copy $DATA_DIR/$1/old -> $DATA_DIR/$1/laib-copy"; }
''')
        # migrate-servicebay.py: the lines MIGRATION.md's step 1 prints, and a draft per service
        exe(b / "migrate", "#!/bin/bash\n" + RECORD + r'''
out=""; while [ $# -gt 0 ]; do [ "$1" = --out ] && out=$2; shift; done
for s in auth radicale mosquitto vaultwarden; do mkdir -p "$out/$s"; printf 'routes:\n  - {host: %s, port: 1}\n' "$s" > "$out/$s/laib.yml"; done
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
                 CONTAINERENV=str(t / "no-containerenv"), LAIB_VERSION=VERSION, MIN_FREE_GB="0", LAIB_RETRY_SLEEP="0")
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
        self.assertIn("add auth BASE_DN=dc=box,dc=test", self.calls_of("laib"))
        self.assertEqual(self.calls_of("carry"), ["auth mosquitto"])
        self.assertFalse((self.t / "q" / "auth.kube").exists())
        self.assertTrue((self.t / "home" / "servicebay-units" / "auth.kube").exists())
        self.assertTrue((self.t / "q" / "radicale.kube").exists())   # not reached

    def test_a_steps_images_are_pulled_before_servicebays_unit_stops(self):
        # #370: AdGuard is the box's resolver; once ServiceBay's is stopped, no pull resolves
        r = self.run_md("--foreground", "--until", "radicale")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        lines = self.calls.read_text().splitlines()
        pull = lines.index("podman pull -q docker.io/tomsquest/docker-radicale:latest")
        stop = lines.index("systemctl --user stop radicale.service")
        self.assertLess(pull, stop, lines)

    def test_servicebays_mail_password_is_piped_into_laib(self):
        # #359: the password of notifications.email goes into AUTHELIA_SMTP_PASSWORD on stdin,
        # never on argv or into the log; without a complete setting nothing is set
        cfg = json.loads((self.t / "sb" / "config.json").read_text())
        (self.t / "sb" / "config.json").write_text(json.dumps(cfg | {"notifications": {"email": {"host": "smtp.mail.test", "port": 587, "user": "box@mail.test", "from": "box@mail.test"}}}))
        r = self.run_md("--foreground", "--until", "secrets")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertNotIn("set AUTHELIA_SMTP_PASSWORD", " ".join(self.calls_of("laib")))
        self.assertIn("no complete mail setting", r.stdout)
        (self.t / "mig" / "progress").unlink()
        cfg["notifications"] = {"email": {"host": "smtp.mail.test", "port": 587, "user": "box@mail.test", "pass": "pw-n'ever \"printed", "from": "box@mail.test"}}
        (self.t / "sb" / "config.json").write_text(json.dumps(cfg))
        r = self.run_md("--foreground", "--until", "secrets")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertIn("secret set AUTHELIA_SMTP_PASSWORD", self.calls_of("laib"))
        self.assertEqual((self.t / "secret-AUTHELIA_SMTP_PASSWORD").read_text(), "pw-n'ever \"printed")
        self.assertNotIn("never", r.stdout + r.stderr + self.calls.read_text())

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

    def test_rollback_all_hands_back_every_service_caddy_first(self):
        (self.t / "q" / "nginx.kube").touch()
        self.run_md("--foreground", "--until", "vaultwarden")
        r = self.run_md("rollback", "all")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        for u in ("auth", "radicale", "mosquitto", "vaultwarden"):
            self.assertTrue((self.t / "q" / f"{u}.kube").exists(), u)
        removed = [c for c in self.calls_of("laib") if c.startswith("remove ")]
        self.assertEqual(removed, ["remove vaultwarden", "remove mosquitto", "remove radicale", "remove auth"])

    def test_handback_frees_the_roles_the_service_holds(self):
        self.run_md("--foreground", "--until", "auth")
        (self.t / "state" / "box.yml").write_text("domain: box.test\nroles: {sso: auth, identity: auth, proxy: caddy}\nhostNetwork: [caddy]\n")
        r = self.run_md("rollback", "auth")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertEqual((self.t / "state" / "box.yml").read_text(),
                         'domain: box.test\nroles: {sso: "", identity: "", proxy: caddy}\nhostNetwork: [caddy]\n')
        self.assertIn("remove auth", self.calls_of("laib"))

    def test_names_behind_a_loopback_built_in_wait_for_caddy(self):
        r = self.run_md("--foreground", "--until", "auth")
        self.assertIn("NPM: auth.box.test waits for Caddy", r.stdout)
        self.assertEqual([c for c in self.calls_of("curl") if "Host: auth." in c], [])

    def test_a_step_cut_off_after_its_add_applies_on_resume(self):
        (self.t / "state" / "services" / "auth").mkdir()   # an earlier attempt's add, the unit stopped before it recorded
        r = self.run_md("--foreground", "--until", "auth")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        laib = self.calls_of("laib")
        self.assertIn("apply auth", laib)
        self.assertFalse([c for c in laib if c.startswith("add auth")], laib)

    def test_auto_update_on_is_a_warning(self):
        cfg = json.loads((self.t / "sb" / "config.json").read_text())
        cfg["autoUpdate"] = {"enabled": True}
        (self.t / "sb" / "config.json").write_text(json.dumps(cfg))
        r = self.run_md("--dry-run")
        self.assertEqual(r.returncode, 0, r.stdout)
        self.assertIn("auto-update", r.stdout)

    def test_a_service_the_box_does_not_run_is_skipped(self):
        r = self.run_md("--foreground", "--until", "beets")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertEqual(self.progress()["beets"], "skip")
        self.assertEqual(self.progress()["adguard"], "skip")

    def test_install_takes_the_release_or_the_ci_build(self):
        self.run_md("--foreground", "--until", "install")
        self.assertEqual(self.calls_of("install-box"), [f"--version {VERSION} --domain box.test --gpu"])
        (self.t / "mig" / "progress").unlink()
        self.calls.write_text("")
        self.run_md("--foreground", "--until", "install", SRC="http://10.0.2.2:8000", INSTALL_ARGS="--tls internal")
        self.assertEqual(self.calls_of("install-box"), ["--source http://10.0.2.2:8000 --domain box.test --gpu --tls internal"])

    def test_dry_run_names_what_no_step_claims(self):
        (self.t / "q" / "auth.kube.bak-old").write_text("")
        r = self.run_md("--dry-run")
        self.assertIn("claimed by no step and not in IGNORE: auth.kube.bak-old", r.stdout)

    def importer(self, name):   # a ServiceBay unit that mounts radicale's data, as solaris-import-google does
        (self.t / "q" / f"{name}.kube").write_text(f"[Kube]\nYaml={name}.yml\n")
        (self.t / "q" / f"{name}.yml").write_text(
            f"kind: Pod\nspec:\n  volumes:\n    - name: cal\n      hostPath:\n        path: {self.t}/data/radicale/data\n"
            f"    - name: share\n      hostPath:\n        path: {self.t}/data/file-share/data\n")

    def test_dry_run_reports_a_konflikt_with_a_unit_that_stays(self):
        # #371: a unit that stays (IGNORE) and mounts radicale's data blocks radicale's laib add
        self.importer("other-import")
        r = self.run_md("--dry-run", IGNORE="other-import")
        self.assertNotEqual(r.returncode, 0, r.stdout)
        self.assertIn(f"RED   radicale: other-import.kube runs it too (data {self.t}/data/radicale/data)", r.stdout)
        self.assertNotIn("file-share/data)", r.stdout)   # the shared tree is nobody's own (backup: false)
        self.changed_nothing()
        # the run refuses the step before it stops anything
        r = self.run_md("--foreground", "--until", "radicale", IGNORE="other-import")
        self.assertNotEqual(r.returncode, 0, r.stdout)
        self.assertIn("radicale: laib add would refuse (KONFLIKT), nothing changed yet", r.stdout)
        self.assertNotIn("--user stop radicale.service", self.calls_of("systemctl"))

    def test_the_google_import_goes_off_with_radicale(self):
        # #371, decided: solaris-import-google writes into radicale's data, so it leaves with radicale
        self.importer("solaris-import-google")
        r = self.run_md("--dry-run")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertNotIn("runs it too", r.stdout)
        r = self.run_md("--foreground", "--until", "radicale")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertIn("--user stop solaris-import-google.service", self.calls_of("systemctl"))
        self.assertFalse((self.t / "q" / "solaris-import-google.kube").exists())

    def test_dry_run_tries_the_carried_admin_logins(self):
        # #373: HA's carried admin password, tried read-only against ServiceBay's running HA
        for u in ("home-assistant", "adguard"):
            (self.t / "q" / f"{u}.kube").write_text(f"[Kube]\nYaml={u}.yml\n")
        r = self.run_md("--dry-run", FAKE_LOGIN_RED="home-assistant")
        self.assertNotEqual(r.returncode, 0, r.stdout)
        self.assertIn("RED   home-assistant: the carried administrator login", r.stdout)
        self.assertIn("ok    adguard: the carried administrator login", r.stdout)
        self.assertEqual([c for c in self.calls_of("carry") if c.startswith("--login")],
                         ["--login home-assistant laib", "--login adguard admin"])
        self.changed_nothing()
        r = self.run_md("--dry-run")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)

    def test_unknown_step_is_refused(self):
        r = self.run_md("--dry-run", "--until", "vaultwardn")
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("unknown step", r.stdout + r.stderr)


if __name__ == "__main__":
    unittest.main()
