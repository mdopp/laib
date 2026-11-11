"""#1169: a doc line the box can answer carries its probe; werkstatt-docprobe
writes the answer and its date, turns a new task into its Box-Aufgabe
number, opens one pull request, and opens or comments one issue per no, not
measured or done task the docs still name. gh and laib are fakes that
answer from a fixture and log their calls; origin is a bare repo."""
import json, os, subprocess, sys, tempfile, unittest
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent.parent / "docprobe.py"
sys.path.insert(0, str(SCRIPT.parent))
import docprobe  # noqa: E402

FAKE_GH = r'''#!/usr/bin/env python3
import json, os, sys
a = sys.argv[1:]
open(os.environ["LOG"], "a").write(json.dumps(a) + "\n")
if a[:2] == ["issue", "list"]: print(json.dumps(json.load(open(os.environ["FIX"]))["issues"]))
elif a[:2] == ["pr", "list"]: print("")
'''
FAKE_LAIB = r'''#!/bin/sh
echo "$*" >> "$LOG"
case "$*" in
"backups") echo '{"LastGreen": "2026-11-10T03:02:47Z", "Offsite": "0001-01-01T00:00:00Z"}' ;;
"diag") echo '[{"probe": "os-update", "detail": "OS-Updates pausiert (/etc/zincati/config.d/90-x.toml)"}]' ;;
"version") exit 9 ;;
"state aufgaben.yml") echo '{"data": {"aufgaben": [{"id": 3, "titel": "Platte einbauen"}, {"id": 4, "titel": "Flashen", "erledigt": {"am": "2026-10-26"}}]}}' ;;
aufgabe\ add*) echo "Box-Aufgabe #16 angelegt: Nachsehen" ;;
*) exit 9 ;;
esac
'''
DOC = """# Z

A snapshot exists <!-- box: laib backups ~ "LastGreen": "(\\d{4}-\\d\\d-\\d\\d) -->.
No pause <!-- box: laib diag ~ ^\\[ !~ OS-Updates pausiert --> *(box 2026-01-01: yes)*.
The version <!-- box: laib version ~ v\\d -->.
The disk: <!-- aufgabe: Platte einbauen -->. The scope: <!-- aufgabe: Nachsehen -->.
- <!-- aufgabe: Nachsehen | In File Browser nachsehen -->
- Voice: Box-Aufgabe #4.
- An example: `<!-- box: laib diag ~ x -->`, `<!-- aufgabe: <Titel> | <warum> -->`.
"""


class DocProbe(unittest.TestCase):
    def setUp(self):
        self.d = Path(tempfile.mkdtemp())
        for name, body in (("gh", FAKE_GH), ("laib", FAKE_LAIB)):
            (self.d / name).write_text(body)
            (self.d / name).chmod(0o755)
        self.log, self.fix = self.d / "log", self.d / "fix.json"
        key = docprobe.hashlib.sha1('docs/ZIELBILD.md laib diag ~ ^\\[ !~ OS-Updates pausiert'.encode()).hexdigest()[:12]
        self.fix.write_text(json.dumps({"issues": [{"number": 70, "body": "x\n" + docprobe.MARK.format(key)}]}))
        origin, self.repo = self.d / "origin.git", self.d / "repo"
        git = lambda *a, cwd=None: subprocess.run(["git", "-c", "user.name=t", "-c", "user.email=t@t", *a], cwd=cwd, check=True, capture_output=True)
        git("init", "-q", "--bare", "-b", "main", str(origin))
        git("clone", "-q", str(origin), str(self.repo))
        (self.repo / "docs").mkdir()
        (self.repo / "docs" / "ZIELBILD.md").write_text(DOC)
        (self.repo / "README.md").write_text("# L\n")
        git("add", "-A", cwd=self.repo)
        git("commit", "-qm", "init", cwd=self.repo)
        git("push", "-q", "origin", "HEAD:main", cwd=self.repo)
        self.origin = origin

    def run_(self, *args):
        env = dict(os.environ, DOCPROBE_GH=str(self.d / "gh"), DOCPROBE_LAIB=str(self.d / "laib"), DOCPROBE_STATE=str(self.d / "seen.json"),
                   DOCPROBE_DIR=str(self.repo), FIX=str(self.fix), LOG=str(self.log), GH_TOKEN="x",
                   GIT_AUTHOR_NAME="t", GIT_AUTHOR_EMAIL="t@t", GIT_COMMITTER_NAME="t", GIT_COMMITTER_EMAIL="t@t")
        return subprocess.run([sys.executable, str(SCRIPT), *args], env=env, capture_output=True, text=True, timeout=60)

    def calls(self, tool=None):
        lines = self.log.read_text().splitlines() if self.log.exists() else []
        return [l for l in lines if l.startswith("[")] if tool == "gh" else [l for l in lines if not l.startswith("[")]

    def test_one_pass_answers_numbers_and_files(self):
        p = self.run_("--now")
        self.assertEqual(p.returncode, 0, p.stderr)
        doc = (self.repo / "docs" / "ZIELBILD.md").read_text()
        today = docprobe.datetime.datetime.now(docprobe.datetime.timezone.utc).date().isoformat()
        self.assertIn(f'(\\d{{4}}-\\d\\d-\\d\\d) --> *(box {today}: yes, 2026-11-10)*.', doc)
        self.assertIn(f"OS-Updates pausiert --> *(box {today}: no)*.", doc)
        self.assertNotIn("2026-01-01", doc)  # the old answer is replaced, not kept beside
        self.assertIn(f"v\\d --> *(box {today}: not measured)*.", doc)
        self.assertIn("The disk: Box-Aufgabe #3. The scope: Box-Aufgabe #16.", doc)
        self.assertIn("- Box-Aufgabe #16\n", doc)
        self.assertIn("`<!-- box: laib diag ~ x -->`, `<!-- aufgabe: <Titel> | <warum> -->`.", doc)  # examples stay
        adds = [c for c in self.calls() if c.startswith("aufgabe add")]
        self.assertEqual(adds, ["aufgabe add Nachsehen --warum In File Browser nachsehen --fuer admins"])  # once, with its warum
        gh = [json.loads(c) for c in self.calls("gh")]
        self.assertEqual([c[2] for c in gh if c[:2] == ["issue", "comment"]], ["70"])  # the open issue of the no
        created = [c for c in gh if c[:2] == ["issue", "create"]]
        self.assertEqual(len(created), 2, created)  # not measured, and the done Box-Aufgabe #4
        self.assertTrue(any("Box-Aufgabe #4 is done" in " ".join(c) for c in created))
        self.assertFalse(any("OS-Updates pausiert (/etc" in " ".join(c) for c in gh))  # no probe output in an issue
        self.assertTrue([c for c in gh if c[:2] == ["pr", "create"]])
        log = subprocess.run(["git", "log", "--oneline", "bot/boxfragen"], cwd=self.origin, capture_output=True, text=True).stdout
        self.assertIn("the box's answers", log)

    def test_the_second_answer_replaces_the_first(self):
        self.run_("--now")
        subprocess.run(["git", "switch", "-q", "main"], cwd=self.repo, check=True)
        subprocess.run(["git", "reset", "-q", "--hard", "bot/boxfragen"], cwd=self.repo, check=True)
        self.run_("--now")
        doc = (self.repo / "docs" / "ZIELBILD.md").read_text()
        self.assertEqual(doc.count("*(box "), 3, doc)

    def test_a_week_has_one_pass(self):
        self.run_("--now")
        self.log.unlink()
        self.assertEqual(self.run_().returncode, 0)
        self.assertFalse(self.log.exists())

    def test_only_a_read_verb_with_a_match_is_a_probe(self):
        self.assertEqual(docprobe.parse("laib template list ~ beets")[0], ["laib", "template", "list"])
        for bad in ("laib apply beets ~ x", "laib exec beets -- ls ~ x", "laib diag !~ red", "curl x ~ y", "laib template update x ~ y"):
            with self.assertRaises(ValueError, msg=bad):
                docprobe.parse(bad)

    def test_check_names_a_bad_probe(self):
        (self.repo / "docs" / "DATA.md").write_text("x <!-- box: laib apply ~ y -->\n")
        p = self.run_("--check", str(self.repo))
        self.assertEqual(p.returncode, 1)
        self.assertIn("docs/DATA.md:1", p.stdout)

    def test_the_docs_probes_parse(self):
        self.assertEqual(docprobe.check(str(SCRIPT.parent.parent)), 0)


if __name__ == "__main__":
    unittest.main()
