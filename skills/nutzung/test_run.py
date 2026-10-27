"""nutzung's script (#270, #839): the week rendered, and no quote in a wish.
The runner-level tests are operator/nutzung_test.go."""

import importlib.util
import os
import sys
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(HERE))
spec = importlib.util.spec_from_file_location("nutzung", os.path.join(HERE, "run.py"))
run = importlib.util.module_from_spec(spec)
spec.loader.exec_module(run)

WEEK = {"days": 7, "summary": {"from": "2026-10-10", "total": 182, "apps": [{"app": "haus", "total": 182, "events": [{"event": "start.view", "count": 140}]}], "knownApps": ["energie", "haus"]},
        "misses": {"misses": [{"source": "bote", "text": "Wann kommt der Müll?", "count": 9, "lastDay": "2026-10-15"}]}}


class S:
    pass


class Nutzung(unittest.TestCase):
    def test_render(self):
        text, sentences = run.render(WEEK)
        self.assertIn("Nutzung der letzten 7 Tage (anonyme Zählungen, seit 2026-10-10): 182 insgesamt.\n- haus: 182 (start.view 140)\nOhne Nutzung in diesen 7 Tagen: energie", text)
        self.assertIn("- 9× bote: „Wann kommt der Müll?“ (zuletzt 2026-10-15)", text)
        self.assertEqual(sentences, ["Wann kommt der Müll?"])
        text, sentences = run.render({"days": 7, "summary": WEEK["summary"], "missesError": "/misses?days=7: 404 Not Found"})
        self.assertIn("Unbeantwortete Sätze: nicht abrufbar (/misses?days=7: 404 Not Found).", text)
        self.assertEqual(sentences, [])
        self.assertIn("- keine", run.render({"summary": {}, "misses": []})[0])

    def test_a_wish_never_quotes_the_family(self):
        s = S()
        s.sentences = ["Wann kommt der Müll?", "Licht"]
        self.assertIn("wörtlich", run.check(s, "laib_wish", {"text": "Abfuhrkalender", "saw": "Bote: wann kommt der müll (9×)"}))
        self.assertEqual(run.check(s, "laib_wish", {"text": "Abfuhrkalender anbinden", "saw": "9 Fragen zur Müllabfuhr"}), "")
        self.assertEqual(run.check(s, "laib_wish", {"text": "Licht im Flur", "saw": "2"}), "")  # under eight letters is no sentence


if __name__ == "__main__":
    unittest.main()
