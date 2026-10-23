"""#469: what pre-start must have done before the Bote's containers start.
Hermes writes its stock SOUL.md ("You are Hermes Agent …") on its first
start when there is none, and post-deploy, which runs after the units,
used to keep any SOUL.md without the laib marker: the Bote introduced
itself as Hermes. pre-start now writes the marked SOUL first, and Hermes'
stock text is replaced as if it were laib's."""
import json, os, subprocess, tempfile, unittest
from pathlib import Path

HOOK = Path(__file__).resolve().parent.parent / "pre-start"


def run(data):
    env = dict(os.environ, LAIB_DATA_DIR=str(data), SIGNAL_ACCOUNT="", MATRIX_HOMESERVER="", TELEGRAM_ALLOWED_USERS="",
               DISCORD_ALLOWED_USERS="1", LAIB_SECRET_BOTE_DISCORD_TOKEN="t", LAIB_SECRET_BOTE_LAIB_TOKEN="x")
    p = subprocess.run(["bash", str(HOOK)], env=env, capture_output=True, text=True, timeout=30)
    assert p.returncode == 0, p.stdout + p.stderr


class Soul(unittest.TestCase):
    def test_a_first_start_finds_laibs_soul_not_hermes_default(self):
        with tempfile.TemporaryDirectory() as d:
            run(d)
            soul = Path(d) / "bote" / "hermes" / "SOUL.md"
            self.assertIn("rendered by laib", soul.read_text().splitlines()[0])

    def test_hermes_stock_soul_is_replaced_and_a_persons_own_kept(self):
        with tempfile.TemporaryDirectory() as d:
            soul = Path(d) / "bote" / "hermes" / "SOUL.md"
            soul.parent.mkdir(parents=True)
            soul.write_text("You are Hermes Agent, built by Nous Research.\n")
            run(d)
            self.assertIn("rendered by laib", soul.read_text().splitlines()[0])
            soul.write_text("# meine eigene Seele\nDu bist Kalle.\n")
            run(d)
            self.assertEqual(soul.read_text(), "# meine eigene Seele\nDu bist Kalle.\n", "a SOUL the owner made his own was overwritten")


def env_file(data, people=None, **values):
    """pre-start with Signal on and box.yml people rendered as laib renders them; Hermes' .env as a dict"""
    render = Path(data) / "render"
    if people is not None:
        (render / "people").mkdir(parents=True)
        (render / "people" / "people.json").write_text(json.dumps(people))
    env = dict(os.environ, LAIB_DATA_DIR=str(data), LAIB_RENDER_DIR=str(render), SIGNAL_ACCOUNT="+4900000001", SIGNAL_ALLOWED_USERS="",
               MATRIX_HOMESERVER="", TELEGRAM_ALLOWED_USERS="", DISCORD_ALLOWED_USERS="", LAIB_SECRET_BOTE_LAIB_TOKEN="x")
    env.update(values)
    p = subprocess.run(["bash", str(HOOK)], env=env, capture_output=True, text=True, timeout=30)
    assert p.returncode == 0, p.stdout + p.stderr
    lines = (Path(data) / "bote" / "hermes" / ".env").read_text().splitlines()
    return dict(l.split("=", 1) for l in lines if "=" in l and not l.startswith("#"))


class AllowedUsers(unittest.TestCase):
    """#586: box.yml people had two people with a signal: id, values.yml SIGNAL_ALLOWED_USERS only the
    owner's number, and Hermes dropped the other person's message. Everyone in people with a signal:
    (or telegram:) id may talk to the Bote, plus what the value adds; the value alone switches no channel on."""

    PEOPLE = {"mama": ["signal:+4900000002", "telegram:123"], "papa": ["telegram:456", "signal:+4900000003"], "oma": ["email:oma@example.org"]}

    def test_everyone_in_people_with_a_signal_id_may_talk(self):
        with tempfile.TemporaryDirectory() as d:
            got = env_file(d, self.PEOPLE, SIGNAL_ALLOWED_USERS="+4900000009, +4900000002")
        self.assertEqual(got["SIGNAL_ALLOWED_USERS"].split(","), ["+4900000009", "+4900000002", "+4900000003"])

    def test_without_people_the_value_is_the_list(self):
        with tempfile.TemporaryDirectory() as d:
            self.assertEqual(env_file(d, None, SIGNAL_ALLOWED_USERS="+4900000009")["SIGNAL_ALLOWED_USERS"], "+4900000009")
        with tempfile.TemporaryDirectory() as d:
            self.assertEqual(env_file(d, self.PEOPLE)["SIGNAL_ALLOWED_USERS"], "+4900000002,+4900000003")

    def test_telegram_ids_join_only_a_telegram_that_is_on(self):
        with tempfile.TemporaryDirectory() as d:
            got = env_file(d, self.PEOPLE, TELEGRAM_ALLOWED_USERS="789", LAIB_SECRET_BOTE_TELEGRAM_TOKEN="t")
        self.assertEqual(got["TELEGRAM_ALLOWED_USERS"], "789,123,456")
        with tempfile.TemporaryDirectory() as d:
            self.assertNotIn("TELEGRAM_ALLOWED_USERS", env_file(d, self.PEOPLE), "people switched Telegram on")
        with tempfile.TemporaryDirectory() as d:
            self.assertNotIn("+4900000002", env_file(d, self.PEOPLE, SIGNAL_ACCOUNT="", DISCORD_ALLOWED_USERS="1",
                                                      LAIB_SECRET_BOTE_DISCORD_TOKEN="t").get("SIGNAL_ALLOWED_USERS", ""))


class OwnerNotice(unittest.TestCase):
    """#586: Hermes' notice to the owner about a dropped sender says "add the ID to SIGNAL_ALLOWED_USERS
    in ~/.env" or "hermes pairing", neither of which is the way on Laib. It is Hermes' own text
    (locales/*.yaml, gateway.unauthorized.owner_notice); Hermes reads an overlay from
    $HERMES_HOME/locales/<lang>.yaml, so pre-start writes one that names box.yml people."""

    def test_the_overlay_names_the_laib_way(self):
        with tempfile.TemporaryDirectory() as d:
            env_file(d, {})
            for lang in ("en", "de"):
                text = (Path(d) / "bote" / "hermes" / "locales" / (lang + ".yaml")).read_text()
                self.assertIn("owner_notice:", text)
                self.assertIn("box.yml", text)
                self.assertIn("people", text)
                self.assertIn("{who}", text)
                for wrong in (".env", "hermes pairing", "unauthorized_dm_behavior"):
                    self.assertNotIn(wrong, text)


if __name__ == "__main__":
    unittest.main()
