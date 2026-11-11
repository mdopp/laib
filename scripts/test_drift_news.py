"""scripts/drift.sh check 7 (#687, #785, #1166): a template change brings a news
line, except a change of only its title or beschreibung in laib.yml — the Box
page's name, nothing the household notices beyond it — or of its exceptions:
block, the reasons the template lint reads."""
import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

DRIFT = Path(__file__).resolve().parent / "drift.sh"


class News(unittest.TestCase):
    def setUp(self):
        self.repo = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.repo)
        (self.repo / "scripts").mkdir()
        shutil.copy(DRIFT, self.repo / "scripts" / "drift.sh")
        t = self.repo / "templates" / "echo"
        t.mkdir(parents=True)
        (t / "laib.yml").write_text("name: echo\nprovides: [demo]\n")
        (t / "news").write_text("# what each change brings\n")
        self.git("init", "-q", "-b", "main")
        self.git("add", "-A")
        self.git("commit", "-q", "-m", "base")
        self.git("checkout", "-q", "-b", "work")

    def git(self, *args):
        env = dict(os.environ, GIT_AUTHOR_NAME="t", GIT_AUTHOR_EMAIL="t@t", GIT_COMMITTER_NAME="t", GIT_COMMITTER_EMAIL="t@t")
        subprocess.run(["git", *args], cwd=self.repo, check=True, env=env, capture_output=True)

    def news_drift(self, laib_yml):
        (self.repo / "templates" / "echo" / "laib.yml").write_text(laib_yml)
        self.git("commit", "-q", "-am", "change")
        p = subprocess.run(["sh", "scripts/drift.sh"], cwd=self.repo, capture_output=True, text=True, env=dict(os.environ, DRIFT_BASE="main"))
        return [l for l in p.stdout.splitlines() if "template news" in l]

    def test_a_name_alone_needs_no_news(self):
        self.assertEqual(self.news_drift("name: echo\ntitle: Echo\nbeschreibung: Antwortet, was man sagt\nprovides: [demo]\n"), [])

    def test_anything_else_in_laib_yml_still_needs_news(self):
        got = self.news_drift("name: echo\ntitle: Echo\nprovides: [demo, echo]\n")
        self.assertTrue(got and "templates/echo" in got[0], got)

    def test_reasons_alone_need_no_news(self):
        self.assertEqual(self.news_drift("name: echo\nprovides: [demo]\n\nexceptions:   # #1166\n  echo.container:\n    hostNetwork: \"mDNS on the LAN\"\n"), [])

    def test_a_key_after_the_reasons_still_needs_news(self):
        got = self.news_drift("name: echo\nprovides: [demo]\nexceptions:\n  echo.container:\n    unhardened: \"unknown, kept until measured\"\nafter: [auth]\n")
        self.assertTrue(got and "templates/echo" in got[0], got)

    def test_a_unit_change_beside_the_reasons_still_needs_news(self):
        (self.repo / "templates" / "echo" / "echo.container").write_text("[Container]\nImage=echo\n")
        self.git("add", "-A")
        got = self.news_drift("name: echo\nprovides: [demo]\nexceptions:\n  echo.container:\n    unhardened: \"unknown, kept until measured\"\n")
        self.assertTrue(got and "templates/echo" in got[0], got)


if __name__ == "__main__":
    unittest.main()
