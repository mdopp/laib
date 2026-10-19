"""laib#418: Radicale's rights. Every account owns its own collections, as
before (owner_only); the household's calendar and address book are Haus's
account's (haus/haushalt, haus/haushalt-kontakte), and every account may
read and write their entries, so the family adds it in DAVx⁵ and Haus, the
Bote and the phones share one Haushalt. Nobody reaches another person's
own calendars. The rules are matched the way Radicale's from_file does:
the first section whose user and collection patterns match decides."""
import configparser, os, re, subprocess, tempfile, unittest
from pathlib import Path

HOOK = Path(__file__).resolve().parent.parent / "pre-start"


def rights(text):
    cp = configparser.ConfigParser(interpolation=None)
    cp.read_string(text)
    return [(cp[s]["user"], cp[s]["collection"], cp[s]["permissions"]) for s in cp.sections()]


def allowed(rules, user, collection):
    for u, c, perm in rules:
        m = re.fullmatch(u, user)
        if not m:
            continue
        if re.fullmatch(c.format(*m.groups(), user=user), collection):
            return perm
    return ""


class Rights(unittest.TestCase):
    def setUp(self):
        d = tempfile.mkdtemp()
        env = dict(os.environ, LAIB_DATA_DIR=d, BASE_DN="dc=vm,dc=test", LAIB_SECRET_RADICALE_LDAP_PASSWORD="ro-pw")
        p = subprocess.run(["bash", str(HOOK)], env=env, capture_output=True, text=True, timeout=30)
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        cfg = configparser.ConfigParser(interpolation=None)
        cfg.read(Path(d) / "radicale" / "config" / "config")
        self.assertEqual(cfg["rights"]["type"], "from_file")
        self.assertEqual(cfg["rights"]["file"], "/config/rights", "the config dir is /config in the container")
        self.rules = rights((Path(d) / "radicale" / "config" / "rights").read_text())

    def test_everyone_owns_their_own_and_nobody_elses(self):
        self.assertIn("w", allowed(self.rules, "anna", "anna/kalender"))
        self.assertIn("W", allowed(self.rules, "anna", "anna"))
        self.assertEqual(allowed(self.rules, "anna", "ben/kalender"), "")
        self.assertEqual(allowed(self.rules, "anna", "ben"), "")
        self.assertIn("R", allowed(self.rules, "anna", ""), "discovery reads the root")

    def test_the_haushalt_is_haus_and_everyone_reads_and_writes_its_entries(self):
        for coll in ("haus/haushalt", "haus/haushalt-kontakte"):
            for who in ("anna", "ben"):
                p = allowed(self.rules, who, coll)
                self.assertIn("r", p, (who, coll))
                self.assertIn("w", p, (who, coll))
                self.assertNotIn("W", p, f"{who} could delete or replace {coll} itself")
            self.assertIn("w", allowed(self.rules, "haus", coll))
        self.assertIn("W", allowed(self.rules, "haus", "haus"), "Haus makes the Haushalt when it is missing")
        # only the Haushalt: Haus's account's other collections stay its own
        self.assertEqual(allowed(self.rules, "anna", "haus/privat"), "")
        self.assertEqual(allowed(self.rules, "anna", "haus"), "")


if __name__ == "__main__":
    unittest.main()
