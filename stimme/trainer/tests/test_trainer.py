"""#128: the wake-word trainer trains the house persona's phrase, decided from
the files it leaves, not from a queue in a database (Solaris's is gone)."""

import json
import pathlib
import sys
import tempfile
import unittest

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent.parent))
import trainer  # noqa: E402


class Decide(unittest.TestCase):
    def setUp(self):
        self.out = pathlib.Path(tempfile.mkdtemp())

    def test_no_model_trains(self):
        self.assertEqual(trainer.decide(self.out, "Hey Joshua", "hey_joshua", "r1"), "train")

    def test_a_model_for_the_phrase_is_done(self):
        (self.out / "hey_joshua.tflite").write_bytes(b"x")
        (self.out / "hey_joshua.json").write_text("{}")
        self.assertEqual(trainer.decide(self.out, "Hey Joshua", "hey_joshua", "r1"), "done")

    def test_a_failed_run_waits_for_a_persona_change(self):
        trainer.write_status(self.out, "hey_joshua", "failed", "Hey Joshua", "r1", "OOM")
        self.assertEqual(trainer.decide(self.out, "Hey Joshua", "hey_joshua", "r1"), "wait")
        self.assertEqual(trainer.decide(self.out, "Hey Joshua", "hey_joshua", "r2"), "train")

    def test_a_run_cut_by_a_restart_trains_again(self):
        trainer.write_status(self.out, "hey_joshua", "training", "Hey Joshua", "r1", "")
        self.assertEqual(trainer.decide(self.out, "Hey Joshua", "hey_joshua", "r1"), "train")

    def test_status_is_readable_json(self):
        trainer.write_status(self.out, "hey_joshua", "done", "Hey Joshua", "r1", "Modell gebaut")
        s = json.loads((self.out / "hey_joshua.status.json").read_text())
        self.assertEqual((s["state"], s["phrase"], s["rev"]), ("done", "Hey Joshua", "r1"))


class Positives(unittest.TestCase):
    def test_every_language_of_the_persona_says_the_phrase(self):
        sets = trainer.positive_sets("Hey Joshua", ["de", "en"])
        self.assertEqual([s.lang for s in sets], ["de", "en"])
        self.assertTrue(all(s.phrase == "Hey Joshua" for s in sets))
        self.assertTrue(all(v.startswith("de_DE-") for v in sets[0].voices))
        self.assertTrue(all(v.startswith("en_") for v in sets[1].voices))

    def test_an_unknown_language_falls_back_to_german(self):
        self.assertEqual([s.lang for s in trainer.positive_sets("Hallo", ["fr"])], ["de"])

    def test_each_wake_word_has_its_own_work_dir(self):
        work = pathlib.Path("/work")
        self.assertNotEqual(trainer.run_dir(work, "hey_joshua"), trainer.run_dir(work, "hello_hal"))


if __name__ == "__main__":
    unittest.main()
