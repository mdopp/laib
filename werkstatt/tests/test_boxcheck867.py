"""#867: after a release lands on the box, the issues it closed that name a
box probe are checked: a read-only laib command runs, green comments, red
comments and reopens; a probe that is no command gets „nicht automatisch
prüfbar“; one comment per issue and release; the first run floods nothing.
gh and laib are fakes that answer from a fixture and log their calls."""
import json, os, subprocess, sys, tempfile, unittest
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent.parent / "boxcheck.py"
FAKE_GH = r'''#!/usr/bin/env python3
import json, os, sys
a = sys.argv[1:]
d = json.load(open(os.environ["FIX"]))
open(os.environ["LOG"], "a").write(json.dumps(a) + "\n")
if a[:2] == ["release", "list"]: print(json.dumps(d["releases"]))
elif a[:2] == ["release", "view"]: print(json.dumps({"body": d["notes"][a[2]]}))
elif a[0] == "api" and a[1].endswith("/comments"): print(json.dumps(d["comments"].get(a[1].split("/")[-2], [])))
elif a[0] == "api": print(json.dumps(d["issues"][a[1].split("/")[-1]]))
'''
FAKE_LAIB = r'''#!/bin/sh
echo "$*" >> "$LOG"
case "$*" in
version) echo "{\"version\": \"$BOX_VERSION\"}" ;;
"status beets") echo "Bestand: fertig" ;;
"status stimme") echo "Weckwort: 8 ohne Befehl"; exit 3 ;;
*) exit 9 ;;
esac
'''


class BoxCheck(unittest.TestCase):
    def setUp(self):
        self.d = Path(tempfile.mkdtemp())
        for name, body in (("gh", FAKE_GH), ("laib", FAKE_LAIB)):
            (self.d / name).write_text(body)
            (self.d / name).chmod(0o755)
        self.fix, self.log = self.d / "fix.json", self.d / "log"
        issue = lambda body, labels=(), closed="2026-10-29T10:00:00Z", state="closed": {
            "state": state, "closed_at": closed, "body": body, "labels": [{"name": l} for l in labels]}
        self.fix.write_text(json.dumps({
            "releases": [{"tagName": "v0.56.0", "publishedAt": "2026-10-30T01:00:00Z"},
                         {"tagName": "v0.55.0", "publishedAt": "2026-10-28T01:00:00Z"},
                         {"tagName": "v0.54.0", "publishedAt": "2026-10-26T01:00:00Z"}],
            "notes": {"v0.56.0": "fix (#11) (#12) (#13) (#14) (#15) (#16) (#17)", "v0.55.0": "fix (#20)"},
            "issues": {
                "11": issue("Auf der Box: `laib status beets` zeigt „Bestand: fertig“"),
                "12": issue("Auf der Box: `laib status stimme` zählt die Fehlalarme"),
                "13": issue("Auf der Box: das Handy zeigt die Seite", ["box-check"]),
                "14": issue("kein Prüfsatz"),
                "15": issue("Auf der Box: `laib apply beets` ist kein Lesen"),
                "16": {**issue("Auf der Box: `laib status beets`"), "pull_request": {}},
                "17": issue("Auf der Box: `laib status beets`", closed="2026-10-20T00:00:00Z"),
                "20": issue("Auf der Box: `laib status beets`")},
            "comments": {"13": ["schon geprüft\n<!-- laib-boxcheck v0.56.0 -->"]}}))
        self.state = self.d / "seen.json"

    def run_(self, version):
        env = dict(os.environ, BOXCHECK_GH=str(self.d / "gh"), BOXCHECK_LAIB=str(self.d / "laib"), BOXCHECK_STATE=str(self.state),
                   FIX=str(self.fix), LOG=str(self.log), BOX_VERSION=version)
        p = subprocess.run([sys.executable, str(SCRIPT)], env=env, capture_output=True, text=True, timeout=30)
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        return p.stdout

    def calls(self, verb):
        return [json.loads(l) for l in self.log.read_text().splitlines() if l.startswith("[") and json.loads(l)[:2] == ["issue", verb]]

    def test_the_first_run_only_records_the_version(self):
        self.run_("v0.55.0")
        self.assertEqual(json.loads(self.state.read_text()), {"version": "v0.55.0"})
        self.assertEqual(self.calls("comment"), [])

    def test_a_new_release_checks_what_it_closed(self):
        self.state.write_text(json.dumps({"version": "v0.55.0"}))
        out = self.run_("v0.56.0")
        comments = {c[2]: c[c.index("--body") + 1] for c in self.calls("comment")}
        self.assertEqual(sorted(comments), ["11", "12", "15"], out)   # 13 already commented, 14 no probe, 16 a PR, 17 older, 20 an older release
        self.assertIn("grün", comments["11"])
        self.assertIn("Bestand: fertig", comments["11"])
        self.assertIn("**rot**", comments["12"])
        self.assertIn("nicht automatisch prüfbar", comments["15"], "apply is no read verb: never run")
        self.assertNotIn("apply beets", self.log.read_text().replace('"', ""), "a write verb ran")
        self.assertEqual([c[2] for c in self.calls("reopen")], ["12"])
        for c in comments.values():
            self.assertIn("<!-- laib-boxcheck v0.56.0 -->", c)
        self.assertEqual(json.loads(self.state.read_text()), {"version": "v0.56.0"})
        self.log.write_text("")
        self.run_("v0.56.0")   # same version again: nothing
        self.assertEqual(self.calls("comment"), [])


if __name__ == "__main__":
    unittest.main()
