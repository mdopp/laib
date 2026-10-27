"""#665: PAPERLESS_DB_PASSWORD, carried from ServiceBay, rotates through
`laib secret rotate`: the hook gets the new value as
LAIB_ROTATE_PAPERLESS_DB_PASSWORD and has psql in paperless-db run ALTER
ROLE from a 0600 file in pgdata, never from argv (#214, ps shows every
argv on the box). The hand-back comes after that psql, so laib takes it
only when psql ran through, and before the file goes."""
import os, subprocess, tempfile, unittest
from pathlib import Path

HOOK = Path(__file__).resolve().parent.parent / "post-deploy"


class PostDeploy(unittest.TestCase):
    def run_hook(self, d, **env):
        bin_ = Path(d, "bin")
        bin_.mkdir(exist_ok=True)
        (bin_ / "python3").write_text("#!/bin/sh\necho mail set up\n")   # paperless-mail.py has its own tests
        (bin_ / "python3").chmod(0o755)
        return subprocess.run(["bash", str(HOOK)], env=dict(PATH=f"{bin_}:/usr/bin:/bin", LAIB_DATA_DIR=d, **env),
                              capture_output=True, text=True, timeout=30)

    def test_a_rotation_alters_the_role_from_a_file(self):
        with tempfile.TemporaryDirectory() as d:
            os.makedirs(os.path.join(d, "paperless", "pgdata"))
            new = "pl-db-rotated-665"
            p = self.run_hook(d, LAIB_SECRET_PAPERLESS_DB_PASSWORD="pl-db-carried", LAIB_ROTATE_PAPERLESS_DB_PASSWORD=new)
            self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
            lines = [l for l in p.stdout.splitlines() if l.startswith(("laib-exec: ", "laib-secret: "))]
            self.assertEqual([l.split()[2] if l.startswith("laib-exec") else l for l in lines],
                             ["psql", "laib-secret: PAPERLESS_DB_PASSWORD=" + new, "rm"])
            self.assertTrue(all(new not in l for l in lines if l.startswith("laib-exec")), "the password on argv")
            f = Path(d, "paperless", "pgdata", ".laib-rotate.sql")
            self.assertEqual(f.read_text(), f"ALTER ROLE paperless PASSWORD '{new}';\n")
            self.assertEqual(f.stat().st_mode & 0o777, 0o600)
            self.assertIn("mail set up", p.stdout)

    def test_without_a_rotation_nothing_changes(self):
        with tempfile.TemporaryDirectory() as d:
            p = self.run_hook(d, LAIB_SECRET_PAPERLESS_DB_PASSWORD="pl-db-carried")
            self.assertEqual(p.returncode, 0, p.stderr)
            self.assertNotIn("laib-", p.stdout)


class RecordMove(unittest.TestCase):
    """ADR 0028: the contract helper's record moves once from Paperless' data folder to Dokumente's
    (the scan service's), so the reminders made carry over; never over one Dokumente has, and the old
    file stays for a rollback."""
    run_hook = PostDeploy.run_hook

    def test_copied_once_and_never_overwritten(self):
        with tempfile.TemporaryDirectory() as d:
            old, new = Path(d, "paperless", "data", "laib-vertraege.json"), Path(d, "dokumente", "laib-vertraege.json")
            old.parent.mkdir(parents=True)
            new.parent.mkdir(parents=True)
            old.write_text('{"erinnerungen": {"12": "2026-11-01"}}\n')
            p = self.run_hook(d)
            self.assertEqual(p.returncode, 0, p.stderr)
            self.assertEqual(new.read_text(), old.read_text())
            self.assertIn("umgezogen", p.stdout)
            self.assertTrue(old.exists(), "the old record stays for a rollback")
            self.assertFalse(Path(str(new) + ".umzug").exists())
            new.write_text('{"erinnerungen": {}, "dokumente": {"3": {}}}\n')   # Dokumente's own since
            p = self.run_hook(d)
            self.assertEqual(p.returncode, 0, p.stderr)
            self.assertIn('"3"', new.read_text())
            self.assertNotIn("umgezogen", p.stdout)

    def test_without_an_old_record_nothing(self):
        with tempfile.TemporaryDirectory() as d:
            Path(d, "dokumente").mkdir()
            p = self.run_hook(d)
            self.assertEqual(p.returncode, 0, p.stderr)
            self.assertFalse(Path(d, "dokumente", "laib-vertraege.json").exists())


if __name__ == "__main__":
    unittest.main()
