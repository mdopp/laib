"""sicherung's script (#287, #858): the weekly sample, decided by code.
The runner-level test is operator/duties858_test.go."""

import importlib.util
import os
import sys
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(HERE))
spec = importlib.util.spec_from_file_location("sicherung", os.path.join(HERE, "run.py"))
run = importlib.util.module_from_spec(spec)
spec.loader.exec_module(run)

import laibskill  # noqa: E402

NOW = run.when("2026-10-25T05:00:00+01:00")
GREEN = laibskill.Reply("grün · repo: restic check 5% ok; 12 Dateien gleich")


def backup(t, result="green", reason=""):
    return {"t": t, "actor": "laib", "verb": "backup", "result": result, "reason": reason}


class Verdict(unittest.TestCase):
    def test_green(self):
        kind, text = run.verdict(GREEN, [backup("2026-10-25T02:00:00Z")], NOW)
        self.assertEqual(kind, "watch")
        self.assertEqual(text, "Wochenprobe der Sicherung 05:00: grün (repo: restic check 5% ok; 12 Dateien gleich); letzte Sicherung 25.10. 03:00.")

    def test_red_check(self):
        for check in (laibskill.Reply("laib: compare /mnt/data/radicale: differ", error=True), laibskill.Reply("restic check: Fatal: pack damaged")):
            kind, text = run.verdict(check, [backup("2026-10-25T02:00:00Z")], NOW)
            self.assertEqual(kind, "incident")
            self.assertIn("die Probe ist rot", text)

    def test_stale_or_none(self):
        # a green check is no backup; a red run and a check do not count as one
        old = [backup("2026-10-22T02:00:00Z"), backup("2026-10-24T02:00:00Z", "red"), backup("2026-10-25T04:00:00Z", reason="check")]
        kind, text = run.verdict(GREEN, old, NOW)
        self.assertEqual((kind, "keine grüne Sicherung seit 22.10. 03:00" in text), ("incident", True), text)
        kind, text = run.verdict(GREEN, [], NOW)
        self.assertEqual((kind, "seit mindestens sieben Tagen" in text), ("incident", True), text)


if __name__ == "__main__":
    unittest.main()
