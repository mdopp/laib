"""#665: ADGUARD_ADMIN_PASSWORD, carried from ServiceBay, rotates through
`laib secret rotate`. AdGuard has no API for its password; pre-start
replaces the user's hash in AdGuardHome.yaml (the rest of the file is
AdGuard's and stays) before the restart reads it, only when the hash is
the current one's, and hands the new one back. bcrypt is the hooks
image's; here a stand-in with the same calls (tests/fakebcrypt)."""
import os, subprocess, sys, tempfile, unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent / "fakebcrypt"))
import bcrypt  # noqa: E402

HOOK = Path(__file__).resolve().parent.parent / "pre-start"
OLD, NEW = "adg-carried-665", "adg-rotated-665"


class PreStart(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.dir.cleanup)
        self.conf = Path(self.dir.name, "adguard", "conf", "AdGuardHome.yaml")
        self.conf.parent.mkdir(parents=True)
        self.conf.write_text("http:\n  address: 0.0.0.0:8083\nusers:\n  - name: admin\n    password: " + bcrypt.hashpw(OLD.encode(), bcrypt.gensalt()).decode()
                             + "\nfiltering:\n  user_rules:\n    - '||ads.example^'\n")

    def run_hook(self, current=OLD, **env):
        e = dict(os.environ, LAIB_DATA_DIR=self.dir.name, ADMIN_USER="admin", LAIB_SECRET_ADGUARD_ADMIN_PASSWORD=current,
                 PYTHONPATH=str(Path(__file__).resolve().parent / "fakebcrypt"), **env)
        return subprocess.run([sys.executable, str(HOOK)], env=e, capture_output=True, text=True, timeout=30)

    def hash(self):
        return next(l.split("password: ")[1] for l in self.conf.read_text().splitlines() if "password: " in l)

    def test_a_rotation_replaces_the_hash_and_hands_the_new_one_back(self):
        p = self.run_hook(LAIB_ROTATE_ADGUARD_ADMIN_PASSWORD=NEW)
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertTrue(bcrypt.checkpw(NEW.encode(), self.hash().encode()))
        self.assertIn("laib-secret: ADGUARD_ADMIN_PASSWORD=" + NEW, p.stdout.splitlines())
        self.assertIn("'||ads.example^'", self.conf.read_text(), "AdGuard's own settings went")
        self.assertEqual(self.conf.stat().st_mode & 0o777, 0o600)
        p = self.run_hook(LAIB_ROTATE_ADGUARD_ADMIN_PASSWORD=NEW)   # again, before laib stored it: only handed back
        self.assertIn("laib-secret: ADGUARD_ADMIN_PASSWORD=" + NEW, p.stdout.splitlines())
        p = self.run_hook(current=NEW, LAIB_ROTATE_ADGUARD_ADMIN_PASSWORD=OLD)   # the rollback
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertTrue(bcrypt.checkpw(OLD.encode(), self.hash().encode()))

    def test_a_password_changed_in_adguard_is_not_overwritten(self):
        p = self.run_hook(current="what laib thinks", LAIB_ROTATE_ADGUARD_ADMIN_PASSWORD=NEW)
        self.assertNotEqual(p.returncode, 0)
        self.assertIn("NOT rotated", p.stderr)
        self.assertNotIn("laib-secret:", p.stdout)
        self.assertTrue(bcrypt.checkpw(OLD.encode(), self.hash().encode()))

    def test_without_a_rotation_the_file_stays_adguards(self):
        before = self.conf.read_text()
        p = self.run_hook()
        self.assertEqual((p.returncode, self.conf.read_text()), (0, before), p.stderr)


if __name__ == "__main__":
    unittest.main()
