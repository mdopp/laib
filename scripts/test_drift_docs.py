"""scripts/drift.sh checks 8 and 9 (#865): docs say what is true today.

8: a retired name from ADR 0021's table "Retired names" stands in a current
doc (README.md, docs/*.md; the ADRs are history by nature) only
where the line, its section or its file says it is past.
9: a line marked x-release-please-version carries version.txt's version,
and every file release-please bumps (extra-files) has such a line."""
import json
import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

DRIFT = Path(__file__).resolve().parent / "drift.sh"
ADR = """# ADR 0021

### Retired names

| Name | Ended | Instead |
|---|---|---|
| Solaris | 2026-10-14, ADR 0022 | the Bote, Haus |
| relay | 2026-10-26, #778 | the Inbox |
"""


class Docs(unittest.TestCase):
    def setUp(self):
        self.repo = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.repo)
        (self.repo / "scripts").mkdir()
        shutil.copy(DRIFT, self.repo / "scripts" / "drift.sh")
        (self.repo / "docs" / "adr").mkdir(parents=True)
        (self.repo / "docs" / "adr" / "0021-names.md").write_text(ADR)
        (self.repo / "version.txt").write_text("0.49.0\n")
        (self.repo / "release-please-config.json").write_text(json.dumps({"packages": {".": {}}}))
        (self.repo / "README.md").write_text("# x\n")

    def drift(self, label, files):
        for name, text in files.items():
            p = self.repo / name
            p.parent.mkdir(parents=True, exist_ok=True)
            p.write_text(text)
        p = subprocess.run(["sh", "scripts/drift.sh"], cwd=self.repo, capture_output=True, text=True)
        return [l for l in p.stdout.splitlines() if label in l]

    def test_a_retired_name_in_a_current_doc_is_drift(self):
        got = self.drift("retired name", {"docs/DESIGN.md": "# Design\n\nSolaris hosts the model.\n"})
        self.assertTrue(got and "docs/DESIGN.md:3" in got[0] and "Solaris" in got[0], got)
        got = self.drift("retired name", {"docs/DESIGN.md": "# Design\n\nThe relay reads the vote.\n"})
        self.assertTrue(got and "relay" in got[0], got)

    def test_a_retired_name_said_to_be_past_is_none(self):
        self.assertEqual(self.drift("retired name", {"docs/DESIGN.md": "# Design\n\nSolaris is retired (ADR 0015).\nThe relay is gone since #778.\n"}), [])
        self.assertEqual(self.drift("retired name", {"docs/DESIGN.md": "# Design\n\n## History\n\nSolaris hosted the model.\n\n## Now\n\nThe Bote.\n"}), [])
        self.assertEqual(self.drift("retired name", {"docs/REVIEW.md": "# Review\n\n**History.** As it was on 2026-10-11.\n\nSolaris keeps /napi.\n"}), [])
        self.assertEqual(self.drift("retired name", {"docs/adr/0016.md": "Solaris hosts the model.\n"}), [])

    def test_a_word_inside_another_is_no_name(self):
        self.assertEqual(self.drift("retired name", {"docs/OUTSIDE.md": "iCloud Private Relay and solaris-whisper and relaying.\n"}), [])

    def test_the_retired_names_come_from_adr_0021(self):
        (self.repo / "docs" / "adr" / "0021-names.md").write_text("# ADR 0021\n")
        got = self.drift("retired name", {"docs/DESIGN.md": "Solaris.\n"})
        self.assertTrue(got and "no table" in got[0], got)

    def test_the_marked_version_is_version_txt(self):
        cfg = json.dumps({"packages": {".": {"extra-files": ["docs/ZIELBILD.md"]}}})
        self.assertEqual(self.drift("docs version", {"release-please-config.json": cfg, "docs/ZIELBILD.md": "- Laib: release v0.49.0 <!-- x-release-please-version -->\n"}), [])
        got = self.drift("docs version", {"docs/ZIELBILD.md": "- Laib: release v0.9.0 <!-- x-release-please-version -->\n"})
        self.assertTrue(got and "0.49.0" in got[0], got)
        got = self.drift("docs version", {"docs/ZIELBILD.md": "- Laib: release v0.9.0\n"})
        self.assertTrue(got and "no x-release-please-version" in got[0], got)


if __name__ == "__main__":
    unittest.main()
