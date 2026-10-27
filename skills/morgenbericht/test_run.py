"""morgenbericht's script (#544, #667, #839): the report's closing lines.
The runner-level tests are operator/record544_test.go and lessons544_test.go."""

import importlib.util
import os
import sys
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(HERE))
spec = importlib.util.spec_from_file_location("morgenbericht", os.path.join(HERE, "run.py"))
run = importlib.util.module_from_spec(spec)
spec.loader.exec_module(run)

DAY = "2026-10-21T07:00:00Z"


def ops(pid, outcome, days, why=""):
    return {"id": pid, "actor": "ops", "reason": "Vorschlag", "status": "approved", "created": f"2026-10-{21 - days:02d}T06:00:00.123456789Z",
            "decided": f"2026-10-{21 - days:02d}T06:00:00Z", "files": ["services/whoami/values.yml"], "finding": "unit whoami", "outcome": outcome, "rejectReason": why}


class Lines(unittest.TestCase):
    def test_rate(self):
        h = [ops(9, "effective", 0), ops(8, "effective", 0), ops(7, "rolled-back", 0), ops(6, "rejected", 0, "der falsche Dienst"), ops(5, "later", 0), ops(4, "", 0),
             {"id": 3, "actor": "human:mdopp", "outcome": "rejected"}]
        old = ops(2, "no-effect", 0)
        old["created"] = "2026-09-01T06:00:00Z"
        self.assertEqual(run.rate(h + [old], run.when(DAY)), "Vorschläge des Operators, 30 Tage: 2 von 4 wirksam (50 %); 2 wirksam, 1 zurückgenommen, 1 abgelehnt.")
        self.assertEqual(run.rate([ops(1, "later", 0)], run.when(DAY)), "")  # a free no is no rate

    def test_stale_lesson(self):
        lesson = {"id": 20, "actor": "ops", "reason": "Lehre: unit whoami — zurück auf v1.10", "status": "approved", "decided": "2026-10-11T07:00:00Z", "files": ["lessons/unit-whoami.md"]}
        before, a, b, ok = ops(19, "no-effect", 11), ops(21, "no-effect", 5), ops(22, "rejected", 2, "wieder dasselbe"), ops(23, "effective", 0)
        self.assertEqual(run.stale([ok, b, a, lesson, before]), "Lehre lessons/unit-whoami.md hilft nicht: 2 spätere Vorschläge ohne Erfolg (#21, #22), zum Entfernen.")
        self.assertEqual(run.stale([ok, a, lesson, before]), "")  # one failure is no pattern

    def test_due(self):
        listed = '{"offen": 3, "faellig": 2, "aufgaben": [{"id": 1, "titel": "Wächter von außen"}, {"id": 4, "titel": "TOTP für mdopp prüfen"}]}'
        self.assertEqual(run.due_line(listed), "Fällige Box-Aufgaben: #1 Wächter von außen, #4 TOTP für mdopp prüfen.")
        for none in ['{"offen": 2, "faellig": 0, "aufgaben": []}', "", "laib: unknown tool", None]:
            self.assertEqual(run.due_line(none), "")


if __name__ == "__main__":
    unittest.main()
