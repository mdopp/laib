"""#128: the wake-word trainer trains the house persona's phrase, decided from
the files it leaves, not from a queue in a database (Solaris's is gone)."""

import os
import json
import pathlib
import sys
import tempfile
import unittest
from unittest import mock

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

    def test_laib_reads_where_the_wake_word_stands(self):
        # #309: the Box page and diag read laib-status.json, which the trainer
        # writes on every change; laib names no template
        for state, reason, want in (("training", "", "wird trainiert"), ("done", "Modell gebaut", "trainiert, noch nicht auf dem Gerät"),
                                    ("failed", "beim Schritt „Modell trainieren“: der Speicher hat nicht gereicht.",
                                     "Training fehlgeschlagen beim Schritt „Modell trainieren“")):
            trainer.write_status(self.out, "hey_joshua", state, "Hey Joshua", "r1", reason)
            s = json.loads((self.out / "laib-status.json").read_text())
            self.assertIn(want, s["detail"])
            self.assertTrue(s["at"].endswith("Z"))
            self.assertEqual(len(s["findings"]), 0 if state == "training" else 1)
        self.assertEqual(s["findings"][0]["severity"], "gelb")

    def test_done_is_not_on_the_device_until_it_is_measured(self):
        # #759: „trainiert“ read as done, and the Voice PE still had only Solaris
        trainer.write_status(self.out, "hey_joshua", "done", "Hey Joshua", "r1", "Modell gebaut")
        s = json.loads((self.out / "laib-status.json").read_text())
        self.assertEqual(s["detail"], "Wake-Word „Hey Joshua“ trainiert, noch nicht auf dem Gerät")
        self.assertEqual(s["phrase"], "Hey Joshua")
        f = s["findings"][0]
        self.assertEqual((f["severity"], f["topic"], f["unmeasured"]), ("info", "wakeword", True), "no Box-Aufgabe closes on the trainer's word")

    def test_a_restart_keeps_what_stimme_names_measured(self):
        measured = {"detail": "Wake-Word „Hey Joshua“ auf dem Gerät", "phrase": "Hey Joshua", "geraet": True, "findings": []}
        (self.out / "laib-status.json").write_text(json.dumps(measured))
        trainer.show_done(self.out, "Hey Joshua")
        self.assertEqual(json.loads((self.out / "laib-status.json").read_text()), measured)
        trainer.show_done(self.out, "Hallo Hal")   # a new phrase: the old measurement is not its
        self.assertIn("Hallo Hal", json.loads((self.out / "laib-status.json").read_text())["detail"])


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


# What the box's run of 2026-10-22 printed before it died (#627): the real
# error two levels down, then train.py's own CalledProcessError about it.
BUILD_FEATURES_DIED = r"""
print("== phase: features")
print("+ /usr/bin/python /work/runs/hey_joshua/_build_features.py")
print("Traceback (most recent call last):")
print('  File "/opt/microWakeWord/microwakeword/audio/augmentation.py", line 136, in __init__')
print("    audiomentations.AddColorNoise(")
print("AttributeError: module 'audiomentations' has no attribute 'AddColorNoise'. Did you mean: 'AddShortNoises'?")
print("Traceback (most recent call last):")
print('  File "/opt/train.py", line 72, in _run')
print("subprocess.CalledProcessError: Command '['/usr/bin/python', '/work/runs/hey_joshua/_build_features.py']' returned non-zero exit status 1.")
raise SystemExit(1)
"""


class AFailedRunSaysWhy(unittest.TestCase):
    """#627, #588: the family reads a German sentence, the admin the error
    line and the tail; the line is never a command cut off at 500 chars."""

    def setUp(self):
        self.out = pathlib.Path(tempfile.mkdtemp())

    def fail_with(self, script, rc_cmd=None):
        cmd = rc_cmd or [sys.executable, "-c", script, "--phrase", "Hey Joshua"] + ["--voice", "de_DE-x.onnx"] * 80
        with self.assertRaises(trainer.StepFailed) as cm, mock.patch("sys.stdout"):
            trainer._run(cmd)
        trainer.write_status(self.out, "hey_joshua", "failed", "Hey Joshua", "r1", cm.exception.grund, cm.exception.einzelheiten)
        return json.loads((self.out / "laib-status.json").read_text())

    def test_the_cause_is_the_error_line_not_the_command(self):
        s = self.fail_with(BUILD_FEATURES_DIED)
        self.assertIn("Merkmale", s["detail"], "the step, in German")
        self.assertIn("passen nicht zusammen", s["detail"])
        for raw in ("CalledProcessError", "Command '[", "AttributeError", "--voice"):
            self.assertNotIn(raw, s["detail"], "no raw error in front of the family")
            self.assertNotIn(raw, s["findings"][0]["detail"])
        self.assertTrue(s["einzelheiten"].startswith("AttributeError: module 'audiomentations' has no attribute 'AddColorNoise'"))
        self.assertIn("augmentation.py", s["einzelheiten"], "the tail stays for the admin")
        self.assertEqual(s["findings"][0]["einzelheiten"], s["einzelheiten"])
        self.assertLess(len(s["detail"]), 200)

    def test_the_trainers_own_status_keeps_it_too(self):
        self.fail_with(BUILD_FEATURES_DIED)
        s = json.loads((self.out / "hey_joshua.status.json").read_text())
        self.assertIn("Merkmale", s["reason"])
        self.assertIn("AddColorNoise", s["einzelheiten"])

    def test_a_killed_run_says_memory(self):
        s = self.fail_with("import os, signal; print('== phase: train', flush=True); os.kill(os.getpid(), signal.SIGKILL)")
        self.assertIn("Speicher", s["detail"])
        self.assertIn("Modell trainieren", s["detail"])

    def test_a_failed_download_says_download(self):
        s = self.fail_with("", rc_cmd=["wget", "-q", "-O", "/dev/null", "http://127.0.0.1:9/nope.onnx"])
        self.assertIn("Download", s["detail"])

    def test_the_child_still_reaches_the_journal(self):
        with mock.patch("sys.stdout") as out:
            trainer._run([sys.executable, "-c", "print('hello journal')"])
        self.assertIn("hello journal", "".join(c.args[0] for c in out.write.call_args_list))

    def test_a_waiting_run_shows_the_same(self):
        self.fail_with(BUILD_FEATURES_DIED)
        before = json.loads((self.out / "laib-status.json").read_text())
        (self.out / "laib-status.json").unlink()
        trainer.show_waiting(self.out, "hey_joshua", "Hey Joshua")
        s = json.loads((self.out / "laib-status.json").read_text())
        self.assertEqual((s["detail"], s["einzelheiten"]), (before["detail"], before["einzelheiten"]))


class Provision(unittest.TestCase):
    """#627: piper-sample-generator pins audiomentations==0.33.0; installing it
    with its deps downgraded the image's 0.43.1 and microWakeWord's
    augmentation lost AddColorNoise."""

    def test_piper_sample_generator_leaves_microwakewords_packages_alone(self):
        src, work = pathlib.Path(tempfile.mkdtemp()), pathlib.Path(tempfile.mkdtemp())
        for repo in trainer.SOURCE_REPOS:
            (src / repo).mkdir()
        cmds = []
        with mock.patch.object(trainer, "IMAGE_SOURCES", src), mock.patch.object(trainer, "_run", lambda cmd, cwd=None: cmds.append(cmd)):
            trainer.provision(work, [])
        pips = [c for c in cmds if "pip" in c]
        psg = [c for c in pips if str(work / "piper-sample-generator") in c]
        self.assertEqual(len(psg), 1)
        self.assertIn("--no-deps", psg[0])
        for c in pips:
            self.assertFalse(any("audiomentations" in a for a in c), c)


    def test_datasets_stays_below_4(self):
        # 2026-10-24: datasets 4 decodes audio only with torchcodec, which the image lacks
        src, work = pathlib.Path(tempfile.mkdtemp()), pathlib.Path(tempfile.mkdtemp())
        for repo in trainer.SOURCE_REPOS:
            (src / repo).mkdir()
        cmds = []
        with mock.patch.object(trainer, "IMAGE_SOURCES", src), mock.patch.object(trainer, "_run", lambda cmd, cwd=None: cmds.append(cmd)):
            trainer.provision(work, [])
        self.assertTrue(any("datasets>=2.18,<4" in c for c in cmds), cmds)
        cf = (pathlib.Path(trainer.__file__).parent / "Containerfile").read_text()
        self.assertIn('"datasets>=2.18,<4"', cf)
        self.assertIn("assert int(datasets.__version__", cf, "the image build must fail on datasets 4")

class RetryOnANewTrainer(unittest.TestCase):
    """2026-10-24: a fixed trainer image never retried a run that failed in the old one."""

    def test_a_new_trainer_build_retries_a_failed_run(self):
        out = pathlib.Path(tempfile.mkdtemp())
        with mock.patch.dict(os.environ, {"PERSONA_REV": "r1", "TRAINER_BUILD": "aaa"}):
            rev = trainer.revision()
        trainer.write_status(out, "hey_x", "failed", "Hey X", rev, "kaputt")
        self.assertEqual(trainer.decide(out, "Hey X", "hey_x", rev), "wait")
        with mock.patch.dict(os.environ, {"PERSONA_REV": "r1", "TRAINER_BUILD": "bbb"}):
            self.assertEqual(trainer.decide(out, "Hey X", "hey_x", trainer.revision()), "train")

class TrainsOnTheCPU(unittest.TestCase):
    """2026-10-24: the run trained and then died validating, the card full with
    the household model and Whisper (LargestFreeBlock: 0). It trains on the CPU
    unless WAKEWORD_DEVICE says gpu."""

    def run_train(self, **env):
        seen = {}
        with mock.patch.dict(os.environ, env, clear=False), \
                mock.patch.object(trainer, "_run", lambda cmd, cwd=None, env=None: seen.update(env=env or {})):
            trainer.train(pathlib.Path(tempfile.mkdtemp()), pathlib.Path(tempfile.mkdtemp()), "Hey X", "hey_x", [])
        return seen["env"]

    def test_the_default_hides_the_card(self):
        os.environ.pop("WAKEWORD_DEVICE", None)
        self.assertEqual(self.run_train().get("CUDA_VISIBLE_DEVICES"), "-1")

    def test_gpu_on_request(self):
        self.assertNotEqual(self.run_train(WAKEWORD_DEVICE="gpu").get("CUDA_VISIBLE_DEVICES"), "-1")

if __name__ == "__main__":
    unittest.main()
