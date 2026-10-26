"""#665: scripts/rotate-carried.sh orders and gates the rotation of the
secrets that still have their ServiceBay value; laib does the mechanics.
One service at a time, green before and after, stop at the first red; the
ones the owner's devices or a provider hold are listed, never rotated. A
fake laib answers diag, status and secret rotate as the real one prints."""
import os, subprocess, tempfile, unittest
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent / "rotate-carried.sh"
DIAG = """gelb  servicebay auth           LLDAP_ADMIN_PASSWORD hat noch den Wert aus ServiceBay, lesbar in /mnt/data/servicebay   → scripts/rotate-carried.sh auth (laib secret rotate LLDAP_ADMIN_PASSWORD, dann grün geprüft)
gelb  servicebay immich         IMMICH_DB_PASSWORD hat noch den Wert aus ServiceBay, lesbar in /mnt/data/servicebay   → scripts/rotate-carried.sh immich (laib secret rotate IMMICH_DB_PASSWORD, dann grün geprüft)
gelb  servicebay mosquitto      MQTT_PASSWORD hat noch den Wert aus ServiceBay, lesbar in /mnt/data/servicebay   → scripts/rotate-carried.sh mosquitto (laib secret rotate MQTT_PASSWORD, dann grün geprüft)
gelb  servicebay auth           AUTHELIA_STORAGE_KEY hat noch den Wert aus ServiceBay, lesbar in /mnt/data/servicebay   → in der App ändern, dann laib secret set AUTHELIA_STORAGE_KEY und laib apply auth (scripts/rotate-carried.sh --wie AUTHELIA_STORAGE_KEY)
gelb  servicebay                AUTHELIA_SMTP_PASSWORD hat noch den Wert aus ServiceBay, lesbar in /mnt/data/servicebay   → beim Anbieter ein neues setzen, dann laib secret set AUTHELIA_SMTP_PASSWORD (Wert auf stdin)
gelb  backup                    Backup liegt nur auf der Datenplatte
"""
FAKE = """#!/usr/bin/env bash
echo "$*" >> "$LOG"
case "$1 $2" in
  "diag "*) cat "$DIAG";;
  "status "*) st=grün; for s in $RED_AFTER; do [ "$s" = "$2" ] && [ -e "$LOG.rotated.$2" ] && st=rot; done
              printf '  %-5s %-20s units\\n' "$st" "$2";;
  "secret rotate") for n in $FAIL; do [ "$n" = "$3" ] && exit 4; done
                   svc=$(awk -v n="$3" '$4 == n {print $3}' "$DIAG"); touch "$LOG.rotated.$svc"; echo "rotiert: $3";;
esac
"""


class RotateCarried(unittest.TestCase):
    def run_script(self, *args, fail="", red_after=""):
        d = tempfile.mkdtemp()
        self.addCleanup(lambda: subprocess.run(["rm", "-rf", d]))
        Path(d, "diag").write_text(DIAG)
        laib = Path(d, "laib")
        laib.write_text(FAKE)
        laib.chmod(0o755)
        log = Path(d, "log")
        p = subprocess.run(["bash", str(SCRIPT), *args], capture_output=True, text=True, timeout=30,
                           env=dict(os.environ, LAIB=str(laib), LOG=str(log), DIAG=str(Path(d, "diag")), FAIL=fail, RED_AFTER=red_after))
        calls = log.read_text().splitlines() if log.exists() else []
        return p, [c for c in calls if c.startswith("secret rotate")]

    def test_the_plan_rotates_nothing_and_says_the_order_and_the_owners_part(self):
        p, rotated = self.run_script()
        self.assertEqual((p.returncode, rotated), (0, []), p.stderr)
        self.assertLess(p.stdout.index("rotiert: immich IMMICH_DB_PASSWORD"), p.stdout.index("rotiert: auth LLDAP_ADMIN_PASSWORD"), "the login is not last")
        for n in ("MQTT_PASSWORD", "AUTHELIA_STORAGE_KEY", "AUTHELIA_SMTP_PASSWORD"):
            self.assertIn("- " + n, p.stdout)
            self.assertNotIn("rotiert: mosquitto", p.stdout, "a password the devices hold is rotated by the script")

    def test_los_rotates_one_at_a_time_and_never_the_owners(self):
        p, rotated = self.run_script("--los")
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertEqual(rotated, ["secret rotate IMMICH_DB_PASSWORD", "secret rotate LLDAP_ADMIN_PASSWORD"])
        self.assertIn("grün: auth nach LLDAP_ADMIN_PASSWORD", p.stdout)

    def test_the_first_red_rotate_stops_the_rest(self):
        p, rotated = self.run_script("--los", fail="IMMICH_DB_PASSWORD")
        self.assertNotEqual(p.returncode, 0)
        self.assertEqual(rotated, ["secret rotate IMMICH_DB_PASSWORD"], "it went on after a red")
        self.assertIn("STOPP", p.stderr)

    def test_red_after_a_green_rotate_stops_and_warns_off_laib_rollback(self):
        p, rotated = self.run_script("--los", red_after="immich")
        self.assertNotEqual(p.returncode, 0)
        self.assertEqual(rotated, ["secret rotate IMMICH_DB_PASSWORD"])
        self.assertIn("nie mit laib rollback", p.stderr)

    def test_los_with_a_service_rotates_only_that_one(self):
        p, rotated = self.run_script("--los", "auth")
        self.assertEqual((p.returncode, rotated), (0, ["secret rotate LLDAP_ADMIN_PASSWORD"]), p.stderr)

    def test_wie_says_what_the_owner_does(self):
        p, _ = self.run_script("--wie", "MQTT_PASSWORD")
        self.assertIn("Home Assistant", p.stdout)
        self.assertIn("laib secret rotate MQTT_PASSWORD", p.stdout)


if __name__ == "__main__":
    unittest.main()
