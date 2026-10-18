"""#226: scripts/migrate-servicebay.py against a config shaped like the
Kraftwerk's (scripts/testdata/migrate: made-up domain and values, the real
shapes — a renamed subdomain, a proxy host the template never declared, one
with no installed service, a variable with no service, a podman named volume,
a `backup: none` annotation over a database, a port that is a placeholder).

The script is run the way the operator runs it, as a process."""
import os, subprocess, sys, tempfile, unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent
SCRIPT = HERE / "migrate-servicebay.py"
FIX = HERE / "testdata" / "migrate"
BUILTINS = HERE.parent / "templates"


def run(out, *extra):
    p = subprocess.run([sys.executable, "-I", str(SCRIPT), "--config", str(FIX / "config.json"),
                        "--templates", str(FIX / "templates"), "--builtins", str(BUILTINS), "--out", str(out), *extra],
                       capture_output=True, text=True)
    return p


class Migrate(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.tmp = tempfile.TemporaryDirectory()
        cls.out = Path(cls.tmp.name) / "drafts"
        cls.p = run(cls.out)
        cls.stdout = cls.p.stdout

    @classmethod
    def tearDownClass(cls):
        cls.tmp.cleanup()

    def draft(self, name, f="laib.yml"):
        return (self.out / name / f).read_text()

    def test_runs(self):
        self.assertEqual(self.p.returncode, 0, self.p.stderr)

    def test_never_writes_into_a_state_repo(self):
        # a services/<name> without units makes laib refuse the whole state, and
        # `laib add <name>` refuses an existing services/<name>
        with tempfile.TemporaryDirectory() as st:
            Path(st, "box.yml").write_text("domain: example.home\n")
            for out in (st, os.path.join(st, "services")):
                p = run(out)
                self.assertNotEqual(p.returncode, 0)
                self.assertIn("state repo", p.stderr)
            self.assertEqual(sorted(os.listdir(st)), ["box.yml"])

    def test_drafts_sit_in_the_out_dir_not_under_services(self):
        self.assertTrue((self.out / "vaultwarden" / "laib.yml").exists())
        self.assertFalse((self.out / "services").exists())

    def test_data_paths_come_from_the_host_paths(self):
        self.assertIn("  - path: ${DATA_DIR}/vaultwarden\n", self.draft("vaultwarden"))
        immich = self.draft("immich")       # backup: none says nothing about where the data is
        for d in ("upload", "pgdata", "model-cache"):
            self.assertIn(f"  - path: ${{DATA_DIR}}/immich/{d}\n", immich)
        self.assertNotIn("  - path: ${DATA_DIR}/file-share/data\n", immich)
        self.assertIn("file-share/data", immich)                       # named as a shared mount
        self.assertIn("  - path: ${DATA_DIR}/home-assistant/matter-server\n", self.draft("home-assistant"))
        self.assertNotIn("/run/dbus", self.draft("home-assistant"))
        self.assertNotIn("  - path: /mnt/data/servicebay", self.draft("pi-web"))    # ServiceBay's own, not the service's
        self.assertRegex(self.stdout, r"pi-web: .*/mnt/data/servicebay/agent-kit/checkout/assists")

    def test_data_the_builtin_would_not_mount_is_reported(self):
        self.assertRegex(self.stdout, r"vaultwarden: the box keeps data in \$\{DATA_DIR\}/vaultwarden; the built-in does not mount it")
        self.assertIn("home-assistant/matter-server; the built-in does not mount it", self.stdout)
        self.assertNotIn("auth/lldap; the built-in", self.stdout)

    def test_named_volume_is_reported(self):
        self.assertIn("file-share-syncthing-config", self.stdout)

    def test_route_follows_the_installed_subdomain(self):
        self.assertIn("{host: asteroids, port: 8092,", self.draft("flutstunde"))

    def test_undeclared_subdomain_is_not_a_route(self):
        self.assertNotIn("host: pi,", self.draft("claude-dev"))
        self.assertIn("{host: pi, port: 8504,", self.draft("pi-web"))
        self.assertRegex(self.stdout, r"claude-dev: .*CLAUDE_DEV_PI_SUBDOMAIN")

    def test_auth_is_what_servicebay_applied(self):
        self.assertIn("{host: auth, port: 9091, exposure: public, auth: none}", self.draft("auth"))
        self.assertIn("{host: ldap, port: 17170, exposure: internal, auth: one_factor, group: admins}", self.draft("auth"))
        self.assertIn("{host: home, port: 8123, exposure: public, auth: none}", self.draft("home-assistant"))
        self.assertIn("{host: zwave, port: 8091, exposure: internal, auth: one_factor}", self.draft("home-assistant"))
        self.assertIn("{host: vault, port: 8222, exposure: public, auth: none}", self.draft("vaultwarden"))

    def test_dropped_hosts_are_reported(self):
        for h in ("ws-test.example.home", "example.home → servicebay-portal"):
            self.assertIn(h, self.stdout)

    def test_service_less_variable_reaches_the_template_that_declares_it(self):
        self.assertIn('ZWAVE_DEVICE: "/dev/ttyACM9"', self.draft("home-assistant", "values.yml"))
        self.assertRegex(self.stdout, r"HOST_GATEWAY_IP.*no installed template declares it")

    def test_variables_of_services_not_installed_are_reported(self):
        self.assertIn("STALE_SPIKE_PORT", self.stdout)

    def test_rsa_and_bcrypt_are_secrets(self):
        self.assertIn("{name: AUTHELIA_OIDC_RSA_PRIVATE_KEY}", self.draft("auth"))

    def test_placeholder_port_falls_back_to_the_default(self):
        self.assertIn("LLDAP_LDAP_PORT", self.stdout)       # reported, the template default is used
        self.assertIn("{port: 3890, lan: false}", self.draft("auth"))

    def test_builtin_is_named(self):
        self.assertIn("laib add auth", self.draft("auth"))
        self.assertNotIn("laib add flutstunde", self.draft("flutstunde"))

    def test_data_bound_secrets_to_carry(self):
        self.assertIn("laib secret set AUTHELIA_STORAGE_KEY", self.stdout)
        self.assertRegex(self.stdout, r"laib secret set AUTHELIA_STORAGE_KEY .*AUTHELIA_STORAGE_ENCRYPTION_KEY")
        self.assertRegex(self.stdout, r"laib secret set IMMICH_DB_PASSWORD .*DB_PASSWORD")
        self.assertIn("laib secret set LLDAP_ADMIN_PASSWORD", self.stdout)
        self.assertNotIn("laib secret set PAPERLESS", self.stdout)    # paperless is not installed here
        self.assertIn("scripts/carry-servicebay-secrets.sh auth file-share home-assistant immich", self.stdout)   # one command sets them (#228)

    def test_add_line_pins_db_bearing_images_and_carries_values(self):
        # #229: a DB-bearing container (no AutoUpdate in the built-in) whose box tag
        # differs is pinned to the digest the box runs; the same tag is not, nor
        # one Laib updates itself; the box's base DN goes under Laib's name
        line = next((l for l in self.stdout.splitlines() if "laib add auth " in l), "")
        self.assertIn("LLDAP_IMAGE=$(podman image inspect --format '{{index .RepoDigests 0}}' docker.io/lldap/lldap:latest)", line)
        self.assertIn("BASE_DN=dc=box,dc=test", line)
        self.assertNotIn("AUTHELIA_IMAGE", line)
        self.assertNotIn("SAMBA_IMAGE", self.stdout)

    def test_builtins_that_are_not_laibs_templates_are_refused(self):
        # #248: run from a copy without templates/, every service was taken for a
        # foreign one: no laib add line, no carry check against the built-in, no word
        with tempfile.TemporaryDirectory() as d:
            p = subprocess.run([sys.executable, "-I", str(SCRIPT), "--config", str(FIX / "config.json"), "--templates", str(FIX / "templates"),
                                "--builtins", d, "--out", str(Path(d) / "o")], capture_output=True, text=True)
        self.assertNotEqual(p.returncode, 0)
        self.assertIn("built-in", p.stderr)

    def test_a_sealed_value_never_lands_on_the_add_line(self):
        # #248: ServiceBay declares MQTT_USERNAME a secret; the add line carried
        # it as `MQTT_USER=!secret MQTT_USERNAME`, two words on laib's argv
        line = next((l for l in self.stdout.splitlines() if "laib add mosquitto" in l), "")
        self.assertTrue(line, self.stdout)
        self.assertNotIn("!secret", line)
        self.assertRegex(self.stdout, r"mosquitto: MQTT_USER .*sealed.*MQTT_USERNAME")

    def test_add_line_carries_the_zwave_stick(self):
        # #248: the box's Z-Wave stick is a service-less value; the built-in's
        # default (/dev/ttyACM0) is another box's, and AddDevice= of a path
        # that is not there keeps Z-Wave JS UI from starting
        line = next((l for l in self.stdout.splitlines() if "laib add home-assistant" in l), "")
        self.assertIn("ZWAVE_DEVICE=/dev/ttyACM9", line)
        # #349: the administrator ServiceBay onboarded Home Assistant as, which post-deploy logs in as
        self.assertIn("HA_ADMIN_USER=oscar", line)
        self.assertIn("laib secret set HA_ADMIN_PASSWORD   # ServiceBay: OSCAR_HA_ADMIN_PASSWORD (home-assistant)", self.stdout)

    def test_add_line_never_runs_with_an_empty_pin(self):
        # #237 review: a box image without a RepoDigest (or gone) makes the
        # $(podman …) empty; laib add must not run then (a blank image value)
        line = next(l for l in self.stdout.splitlines() if "laib add auth " in l).strip()
        with tempfile.TemporaryDirectory() as b:
            for rc, digest in ((125, ""), (0, "docker.io/lldap/lldap@sha256:feed")):
                Path(b, "podman").write_text(f"#!/bin/sh\necho {digest}\nexit {rc}\n")
                Path(b, "laib").write_text(f'#!/bin/sh\nprintf "%s\\n" "$@" > {b}/laib-argv\n')
                for f in ("podman", "laib"):
                    Path(b, f).chmod(0o755)
                Path(b, "laib-argv").unlink(missing_ok=True)
                subprocess.run(["bash", "-c", line], env=dict(os.environ, PATH=f"{b}:{os.environ['PATH']}"), capture_output=True)
                ran = Path(b, "laib-argv").exists()
                if rc:
                    self.assertFalse(ran, line)
                else:
                    self.assertIn("LLDAP_IMAGE=docker.io/lldap/lldap@sha256:feed", Path(b, "laib-argv").read_text().split("\n"), line)

    def test_no_secret_value_is_written(self):
        for d in self.out.iterdir():
            for f in d.iterdir():
                self.assertNotIn("__REDACTED__", f.read_text())


if __name__ == "__main__":
    unittest.main()
