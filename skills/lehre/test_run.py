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
    def __init__(self, history, sessions, propose_ok=True, answer=None):
        self.data = {"history": history, "sessions": sessions}
        self.calls, self.ok, self.answer, self.asked = [], propose_ok, answer, []

    def ask(self, prompt):
        self.asked.append(prompt)
        if self.answer is None:
            return laibskill.Reply("model: 503 slot unavailable", True)
        return laibskill.Reply(self.answer)

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

    def test_the_model_condenses_it(self):  # #861
        fixed = {"id": 12, "actor": "ops", "reason": "whoami zurück auf v1.10", "finding": "unit whoami", "outcome": "effective"}
        sess = {"skill": "vorfall", "note": "whoami rot: das Image v1.10.9 fehlt.", "proposed": [12]}
        answer = ("Hier die Lehre:\n- Ursache: Ein Image-Tag, den es in der Registry nicht gibt.\nZeichen: Die Unit scheitert beim Pull mit manifest unknown.\n"
                  "Lösung: Auf die letzte Version zurück, die lief.\nNicht anwenden, wenn: die Registry selbst nicht erreichbar ist.")
        s = Fake([fixed], [sess], answer=answer)
        r = run.run(s)
        self.assertIn("Befund: unit whoami", s.asked[0])
        self.assertIn("Notiz der Sitzung: whoami rot: das Image v1.10.9 fehlt.", s.asked[0])
        patch = [a for t, a in s.calls if t == "laib_propose"][0]["patch"]
        self.assertIn("@@ -0,0 +1,7 @@\n+# Lehre: unit whoami\n+\n+- Ursache: Ein Image-Tag, den es in der Registry nicht gibt.\n+- Zeichen:", patch)
        self.assertIn("+- Nicht anwenden, wenn: die Registry selbst nicht erreichbar ist.\n+- Quelle: Vorschlag #12", patch)
        self.assertNotIn("nicht verdichtet", r["note"])

    def test_an_answer_without_the_four_lines_keeps_the_transcription(self):
        fixed = {"id": 12, "reason": "x", "finding": "unit whoami", "outcome": "effective"}
        for answer, why in (("Ursache: kaputt.", "die Antwort hatte nicht die vier Zeilen"), (None, "das Modell antwortete nicht")):
            r = run.run(Fake([fixed], [{"proposed": [12], "note": "n"}], answer=answer))
            self.assertIn(f"(nicht verdichtet: {why})", r["note"])

    def test_a_refused_lesson_is_said(self):
        fixed = {"id": 12, "reason": "x", "finding": "unit whoami", "outcome": "effective"}
        r = run.run(Fake([fixed], [{"proposed": [12], "note": ""}], propose_ok=False))
        self.assertEqual(r["note"], "Lehre zu unit whoami ließ sich nicht vorschlagen: refused: review.")


if __name__ == "__main__":
    unittest.main()
