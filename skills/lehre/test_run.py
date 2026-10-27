"""lehre's script (#544, #839). The runner-level test is operator/lessons544_test.go."""

import importlib.util
import os
import sys
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(HERE))
import laibskill  # noqa: E402

spec = importlib.util.spec_from_file_location("lehre", os.path.join(HERE, "run.py"))
run = importlib.util.module_from_spec(spec)
spec.loader.exec_module(run)


class Fake:
    def __init__(self, history, sessions, propose_ok=True):
        self.data = {"history": history, "sessions": sessions}
        self.calls, self.ok = [], propose_ok

    def call(self, tool, **args):
        self.calls.append((tool, args))
        if tool == "laib_propose" and not self.ok:
            return laibskill.Reply("refused: review", True)
        return laibskill.Reply('{"id":13}')


class Lehre(unittest.TestCase):
    def test_once_per_finding(self):
        fixed = {"id": 12, "actor": "ops", "reason": "whoami zurück auf v1.10", "finding": "unit whoami", "outcome": "effective"}
        sess = {"skill": "vorfall", "note": "whoami rot:\n das Image fehlt.", "proposed": [12]}
        s = Fake([fixed], [sess, sess])
        r = run.run(s)
        props = [a for t, a in s.calls if t == "laib_propose"]
        self.assertEqual(len(props), 1)
        self.assertEqual(props[0]["reason"], "Lehre: unit whoami — whoami zurück auf v1.10")
        self.assertIn("+++ b/lessons/unit-whoami.md\n@@ -0,0 +1,5 @@\n+# Lehre: unit whoami\n+\n", props[0]["patch"])
        self.assertIn("Ursache, wie der Vorfall sie sah: whoami rot: das Image fehlt.", props[0]["patch"])
        self.assertEqual((r["outcome"], s.calls[-1][0]), ("idle", "laib_note"))
        self.assertIn("Lehre zu unit whoami vorgeschlagen (lessons/unit-whoami.md)", r["note"])
        had = {"id": 13, "reason": "Lehre: unit whoami — x", "files": ["lessons/unit-whoami.md"]}
        r = run.run(Fake([fixed, had], [sess]))
        self.assertEqual(r["note"], "Keine neue Lehre: kein Vorfall, dessen Befund seitdem weg ist.")

    def test_a_refused_lesson_is_said(self):
        fixed = {"id": 12, "reason": "x", "finding": "unit whoami", "outcome": "effective"}
        r = run.run(Fake([fixed], [{"proposed": [12], "note": ""}], propose_ok=False))
        self.assertEqual(r["note"], "Lehre zu unit whoami ließ sich nicht vorschlagen: refused: review.")


if __name__ == "__main__":
    unittest.main()
