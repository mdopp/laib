"""laib#418: Radicale's rights. Every account owns its own collections, as
before (owner_only); the household's calendar and address book are Homes's
account's (homes/haushalt, homes/haushalt-kontakte), and every account may
read and write their entries, so the family adds it in DAVx⁵ and Homes, the
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
        self.text = (Path(d) / "radicale" / "config" / "rights").read_text()
        self.rules = rights(self.text)
        self.auth = cfg["auth"]

    def test_logins_are_cached_and_a_password_change_lands_within_minutes(self):
        # every request is an LDAP bind against LLDAP, 0.13-0.30 s each (2026-10-28): Radicale (>= 3.4.0)
        # keeps a login for a while, so a session's later requests skip the bind; the expiries are seconds
        self.assertEqual(self.auth["type"], "ldap")
        self.assertEqual(self.auth["cache_logins"], "true")
        ok, failed = int(self.auth["cache_successful_logins_expiry"]), int(self.auth["cache_failed_logins_expiry"])
        self.assertEqual(ok, 900, "a changed or removed password still works this long")
        self.assertEqual(failed, 90)

    def test_everyone_owns_their_own_and_nobody_elses(self):
        self.assertIn("w", allowed(self.rules, "anna", "anna/kalender"))
        self.assertIn("W", allowed(self.rules, "anna", "anna"))
        self.assertEqual(allowed(self.rules, "anna", "ben/kalender"), "")
        self.assertEqual(allowed(self.rules, "anna", "ben"), "")
        self.assertIn("R", allowed(self.rules, "anna", ""), "discovery reads the root")

    def test_the_haushalt_is_homes_and_everyone_reads_and_writes_its_entries(self):
        for coll in ("homes/haushalt", "homes/haushalt-kontakte"):
            for who in ("anna", "ben"):
                p = allowed(self.rules, who, coll)
                self.assertIn("r", p, (who, coll))
                self.assertIn("w", p, (who, coll))
                self.assertNotIn("W", p, f"{who} could delete or replace {coll} itself")
            self.assertIn("w", allowed(self.rules, "homes", coll))
        self.assertIn("W", allowed(self.rules, "homes", "homes"), "Homes makes the Haushalt when it is missing")
        # only the Haushalt: Homes's account's other collections stay its own
        self.assertEqual(allowed(self.rules, "anna", "homes/privat"), "")
        self.assertEqual(allowed(self.rules, "anna", "homes"), "")

    def test_the_mirrors_are_written_only_by_the_kalender_account_and_read_by_their_person(self):   # #495
        for coll in ("anna/google-familie", "anna/google-familie/x.ics", "anna/abo-feiertage", "homes/abo-muell/y.ics"):
            self.assertIn("w", allowed(self.rules, "kalender", coll), coll)
            self.assertIn("r", allowed(self.rules, "homes", coll), f"the Haushalt's Termine leave out {coll}")
        for who, coll in (("anna", "anna/google-familie"), ("anna", "anna/abo-feiertage/x.ics"), ("ben", "homes/abo-muell"), ("anna", "homes/abo-muell/y.ics")):
            p = allowed(self.rules, who, coll)
            self.assertIn("r", p, (who, coll))
            self.assertNotIn("w", p, f"{who} could write into the mirror {coll}: the next run would undo it")
        self.assertEqual(allowed(self.rules, "ben", "anna/google-familie"), "", "another person's Google calendar is theirs")
        self.assertIn("w", allowed(self.rules, "anna", "anna/google-kontakte/x.vcf"), "a Takeout's address book stays the person's own")
        self.assertEqual(allowed(self.rules, "kalender", "anna/google-kontakte"), "")
        for coll in ("anna/kalender", "anna", "homes/privat", "auth"):
            self.assertNotIn("w", allowed(self.rules, "kalender", coll), f"kalender may write {coll}")
        self.assertNotIn("w", allowed(self.rules, "kalender", "homes/haushalt/x.ics"), "the Haushalt is copied for HA, never written")
        self.assertIn("r", allowed(self.rules, "kalender", "homes/haushalt"))
        self.assertIn("R", allowed(self.rules, "kalender", "homes"), "it lists Homes's calendars to copy them")
        self.assertIn("W", allowed(self.rules, "kalender", "homeassistant"), "it makes HA's home")

    def test_home_assistant_reads_its_copies_and_nothing_else(self):   # #495
        self.assertIn("r", allowed(self.rules, "homeassistant", "homeassistant/haus-haushalt/x.ics"))
        self.assertNotIn("w", allowed(self.rules, "homeassistant", "homeassistant/haus-haushalt/x.ics"))
        self.assertNotIn("W", allowed(self.rules, "homeassistant", "homeassistant"))
        self.assertEqual(allowed(self.rules, "homeassistant", "anna/kalender"), "")

    def test_homes_makes_a_calendar_for_one_person_or_the_family_and_nothing_else_of_theirs(self):   # haus#66
        self.assertIn("w", allowed(self.rules, "homes", "anna/privat-sport"))
        self.assertIn("w", allowed(self.rules, "anna", "anna/privat-sport"), "the person owns it")
        self.assertEqual(allowed(self.rules, "ben", "anna/privat-sport"), "", "nur für mich")
        self.assertIn("R", allowed(self.rules, "homes", "anna"), "Homes lists the person's calendars")
        self.assertEqual(allowed(self.rules, "homes", "anna/kalender"), "", "the person's own calendars stay theirs")
        for who in ("anna", "ben"):
            self.assertIn("w", allowed(self.rules, who, "homes/familie-geburtstage/x.ics"))
            self.assertNotIn("W", allowed(self.rules, who, "homes/familie-geburtstage"))

    def test_homes_mirrors_a_persons_phone_contacts_into_their_own_address_book(self):   # ADR 0029
        self.assertIn("w", allowed(self.rules, "homes", "anna/handy-kontakte"))
        self.assertIn("w", allowed(self.rules, "homes", "anna/handy-kontakte/abc.vcf"))
        self.assertIn("r", allowed(self.rules, "anna", "anna/handy-kontakte/abc.vcf"), "the person reads their mirror")
        self.assertEqual(allowed(self.rules, "ben", "anna/handy-kontakte"), "", "nobody else")
        self.assertEqual(allowed(self.rules, "homes", "anna/google-kontakte"), "", "the Takeout book stays the person's")

    def test_the_archive_reads_its_persons_contacts_and_calendars_and_writes_nothing(self):   # laib#1009, archiv#21
        self.assertEqual(allowed(self.rules, "archiv-anna", ""), "R", "discovery reads the root")
        self.assertEqual(allowed(self.rules, "archiv-anna", "anna"), "R", "it lists anna's collections, nothing more")
        for coll in ("anna/handy-kontakte", "anna/handy-kontakte/abc.vcf", "anna/google-familie", "anna/google-familie/x.ics",
                     "anna/privat-sport", "anna/privat-sport/y.ics"):
            self.assertEqual(allowed(self.rules, "archiv-anna", coll), "r", coll)
        for coll in ("anna/google-kontakte", "anna/google-kontakte/x.vcf", "anna/kalender", "anna/abo-feiertage", "anna/tagebuch/x.ics",
                     "ben", "ben/handy-kontakte", "ben/privat-sport/y.ics", "ben/google-familie",
                     "homes", "homes/haushalt", "homes/haushalt/x.ics", "homes/familie-geburtstage", "homes/abo-muell",
                     "homes/haushalt-kontakte-alt", "homes/privat-x", "anna/haushalt-kontakte",
                     "archiv-anna", "archiv-anna/x", "homeassistant", "anna/handy-kontakte-alt", "annax/handy-kontakte"):
            self.assertEqual(allowed(self.rules, "archiv-anna", coll), "", f"archiv-anna reaches {coll}")

    def test_the_archive_reads_the_households_address_book_and_writes_nothing(self):   # ADR 0029 amendment 2026-11-10, archiv#27
        for who in ("archiv-anna", "archiv-ben"):
            for coll in ("homes/haushalt-kontakte", "homes/haushalt-kontakte/x.vcf"):
                self.assertEqual(allowed(self.rules, who, coll), "r", (who, coll))

    def test_no_section_before_the_archives_grants_it_more(self):   # laib#1009
        # Radicale takes the first section that matches: every section above the archive's four may give
        # an archiv-<person> at most what [root] gives everyone, R on the root itself
        cp = configparser.ConfigParser(interpolation=None)
        cp.read_string(self.text)
        first = cp.sections().index("archiv-home")
        self.assertEqual(cp.sections()[first:first + 4], ["archiv-home", "archiv-lesen", "archiv-haushalt-kontakte", "archiv-sonst-nichts"])
        self.assertEqual(cp.sections()[:first], ["root"], "a section before the archive's could give it more")

    def test_the_other_service_accounts_keep_exactly_what_they_had(self):   # laib#1009
        # the archive's sections match only archiv-<something>: kalender, homes, homeassistant and the family are untouched
        for who, coll, want in (("kalender", "anna/google-familie", "rw"), ("kalender", "homes/haushalt", "Rr"), ("kalender", "homeassistant", "RrWw"),
                                ("homes", "anna/privat-sport", "Rrw"), ("homes", "anna/handy-kontakte/x.vcf", "Rrw"), ("homes", "anna/google-familie", "r"),
                                ("homes", "homes", "RrWw"), ("homeassistant", "homeassistant/x", "Rr"), ("anna", "anna/handy-kontakte", "RrWw"),
                                ("archiv", "archiv/x", "RrWw"), ("anna", "homes/haushalt", "rw")):
            self.assertEqual(allowed(self.rules, who, coll), want, (who, coll))


if __name__ == "__main__":
    unittest.main()


class TheHouseholdsCollectionsMove(unittest.TestCase):
    """laib#1021, ADR 0031: the household's account was `haus`; its collections move to `homes`
    before Radicale starts with rights that name `homes`, entries, UIDs and props along."""

    def run_hook(self, d):
        env = dict(os.environ, LAIB_DATA_DIR=d, BASE_DN="dc=vm,dc=test", LAIB_SECRET_RADICALE_LDAP_PASSWORD="ro-pw")
        return subprocess.run(["bash", str(HOOK)], env=env, capture_output=True, text=True, timeout=30)

    def root(self, d):
        return Path(d) / "radicale" / "data" / "collections" / "collection-root"

    def test_homes_moves_to_homes_with_everything_in_it_once(self):
        d = tempfile.mkdtemp()
        r = self.root(d)
        (r / "haus" / "haushalt").mkdir(parents=True)
        (r / "haus" / "haushalt" / ".Radicale.props").write_text('{"tag": "VCALENDAR"}')
        (r / "haus" / "haushalt" / "a.ics").write_text("BEGIN:VCALENDAR\nUID:a\nEND:VCALENDAR\n")
        (r / "homeassistant" / "haus-haushalt").mkdir(parents=True)   # HA's copy keeps its path: HA's entity ids hang on it
        p = self.run_hook(d)
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertFalse((r / "haus").exists())
        self.assertEqual((r / "homes" / "haushalt" / "a.ics").read_text(), "BEGIN:VCALENDAR\nUID:a\nEND:VCALENDAR\n")
        self.assertTrue((r / "homes" / "haushalt" / ".Radicale.props").exists())
        self.assertTrue((r / "homeassistant" / "haus-haushalt").is_dir())
        p = self.run_hook(d)   # again: nothing left to move, nothing touched
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertTrue((r / "homes" / "haushalt" / "a.ics").exists())

    def test_an_empty_homes_folder_is_taken_over(self):
        # empty, or only what Radicale writes when the new login looked before the move
        d = tempfile.mkdtemp()
        r = self.root(d)
        (r / "haus" / "haushalt").mkdir(parents=True)
        (r / "homes").mkdir()
        (r / "homes" / ".Radicale.props").write_text("{}")
        p = self.run_hook(d)
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertTrue((r / "homes" / "haushalt").is_dir())
        self.assertFalse((r / "haus").exists())

    def test_both_holding_collections_is_red_and_nothing_moves(self):
        d = tempfile.mkdtemp()
        r = self.root(d)
        (r / "haus" / "haushalt").mkdir(parents=True)
        (r / "homes" / "abo-muell").mkdir(parents=True)
        p = self.run_hook(d)
        self.assertNotEqual(p.returncode, 0)
        self.assertIn("merge them by hand", p.stderr)
        self.assertTrue((r / "haus" / "haushalt").is_dir())
        self.assertEqual(sorted(x.name for x in (r / "homes").iterdir()), ["abo-muell"])

    def test_a_box_without_haus_is_left_alone(self):
        d = tempfile.mkdtemp()
        p = self.run_hook(d)
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertFalse(self.root(d).exists())
