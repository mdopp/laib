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

class FalseWakes(unittest.TestCase):
    """#825: „Hey Joshua“ woke on room talk at 0.95. WAKE_CUTOFF goes into a trained model's
    manifest without training, and the false wakes stimme-wakes kept under negativ/ are
    negatives on the next run, never positives; they start no run themselves."""

    def setUp(self):
        self.out, self.rec = pathlib.Path(tempfile.mkdtemp()), pathlib.Path(tempfile.mkdtemp())
        (self.out / "hey_joshua.tflite").write_bytes(b"x")
        (self.out / "hey_joshua.json").write_text(json.dumps({"model": "hey_joshua.tflite", "micro": {"probability_cutoff": 0.95, "sliding_window_size": 5}}))

    def cut(self):
        return json.loads((self.out / "hey_joshua.json").read_text())["micro"]

    def test_the_cutoff_is_rewritten_without_training(self):
        self.assertTrue(trainer.retune(self.out, "hey_joshua", 0.97))
        self.assertEqual(self.cut(), {"probability_cutoff": 0.97, "sliding_window_size": 5})
        self.assertFalse(trainer.retune(self.out, "hey_joshua", 0.97), "the same cutoff wrote the file again")
        self.assertEqual(trainer.decide(self.out, "Hey Joshua", "hey_joshua", "r1"), "done")

    def test_the_cutoff_comes_from_the_values(self):
        for value, want in (("0.98", 0.98), ("", 0.97), ("hoch", 0.97), ("1", 0.97), ("0.2", 0.97)):
            with mock.patch.dict(os.environ, {"WAKE_CUTOFF": value}):
                self.assertEqual(trainer.cutoff(), want, value)

    def test_new_negatives_start_no_run(self):
        neg = self.rec / "hey_joshua" / "negativ"
        neg.mkdir(parents=True)
        (neg / "a.wav").write_bytes(b"RIFF")
        self.assertEqual(trainer.decide(self.out, "Hey Joshua", "hey_joshua", "r1"), "done")

    def run_train(self, negatives):
        if negatives:
            neg = self.rec / "hey_x" / "negativ"
            neg.mkdir(parents=True)
            (neg / "a.wav").write_bytes(b"RIFF")
        seen = {}
        with mock.patch.dict(os.environ, {"WAKEWORD_RECORDINGS": str(self.rec), "WAKE_CUTOFF": "0.97"}), \
                mock.patch.object(trainer, "_run", lambda cmd, cwd=None, env=None: seen.update(cmd=cmd)):
            trainer.train(pathlib.Path(tempfile.mkdtemp()), self.out, "Hey X", "hey_x", [])
        return seen["cmd"]

    def test_the_next_run_trains_on_them(self):
        cmd = self.run_train(True)
        self.assertEqual(cmd[cmd.index("--negatives") + 1], str(self.rec / "hey_x" / "negativ"))
        self.assertEqual(cmd[cmd.index("--cutoff") + 1], "0.97")

    def test_a_done_run_remembers_when_it_began(self):
        # #828: stimme-names counts the recordings that came after the run began as new
        trainer.write_status(self.out, "hey_x", "done", "Hey X", "r1", "ok", begonnen="2026-10-27T10:00:00Z")
        self.assertEqual(json.loads(trainer.status_path(self.out, "hey_x").read_text())["begonnen"], "2026-10-27T10:00:00Z")

    def test_a_model_moved_aside_trains_again(self):
        # #828: stimme-retrain moves <id>.tflite and <id>.json into vorher/; the status still says done
        trainer.write_status(self.out, "hey_joshua", "done", "Hey Joshua", "r1", "ok")
        vorher = self.out / "vorher"
        vorher.mkdir()
        for f in ("hey_joshua.tflite", "hey_joshua.json"):
            (self.out / f).rename(vorher / f)
        self.assertEqual(trainer.decide(self.out, "Hey Joshua", "hey_joshua", "r1"), "train")

    def test_without_false_wakes_the_run_has_no_negatives(self):
        self.assertNotIn("--negatives", self.run_train(False))


class TrainOnNegatives(unittest.TestCase):
    """train.py: the negatives are their own feature set, never folded in as positives"""

    @classmethod
    def setUpClass(cls):
        import types
        stub = types.SimpleNamespace(safe_dump=lambda d, sort_keys=False: json.dumps(d))   # the config read back as JSON
        # PyYAML lives in the image, not necessarily here; the stub must not outlive this class,
        # or a later test that imports the real yaml gets the stub (one unittest process in CI)
        cls.modules = mock.patch.dict(sys.modules, {} if "yaml" in sys.modules else {"yaml": stub})
        cls.modules.start()
        import train
        cls.train = train
        cls.yaml = mock.patch.object(train, "yaml", stub)
        cls.yaml.start()

    @classmethod
    def tearDownClass(cls):
        cls.yaml.stop()
        cls.modules.stop()

    def wav(self, path, frames=8000):
        import wave
        path.parent.mkdir(parents=True, exist_ok=True)
        with wave.open(str(path), "wb") as w:
            w.setnchannels(1)
            w.setsampwidth(2)
            w.setframerate(16000)
            w.writeframes(b"\x00\x30" * frames)

    def test_a_false_wake_is_never_a_positive(self):
        rec, run = pathlib.Path(tempfile.mkdtemp()), pathlib.Path(tempfile.mkdtemp())
        self.wav(rec / "mama.wav")
        self.wav(rec / "negativ" / "20261027T173935000.wav")
        with mock.patch("sys.stdout"):
            self.assertEqual(self.train.fold_real_positives(run, rec, 2), 1)
        self.assertEqual(len(list((run / "generated_samples").glob("real_*.wav"))), 2)

    def test_haus_recordings_are_positives_and_its_temp_files_are_not(self):
        # #828: Haus writes recordings/<id>/haus/<person>-<YYYYMMDD-HHMMSS>.wav through .<name>.part
        rec, run = pathlib.Path(tempfile.mkdtemp()), pathlib.Path(tempfile.mkdtemp())
        self.wav(rec / "haus" / "anna-20261027-120000.wav")
        self.wav(rec / "haus" / "ben-20261027-120100.wav")
        self.wav(rec / "haus" / ".ben-20261027-120200.wav.part")
        self.wav(rec / "haus" / ".ben-20261027-120300.wav")
        self.wav(rec / "negativ" / "20261027T173935000.wav")
        with mock.patch("sys.stdout"):
            self.assertEqual(self.train.fold_real_positives(run, rec, 1), 2)
        (rec / "haus" / "anna-20261027-120000.wav").unlink()
        (rec / "haus" / "ben-20261027-120100.wav").unlink()
        with mock.patch("sys.stdout"):
            self.assertEqual(self.train.fold_real_positives(run, rec, 1), 0, "negativ/ and the temp files are never positives")

    def test_the_household_negatives_are_a_negative_feature_set(self):
        neg, run = pathlib.Path(tempfile.mkdtemp()), pathlib.Path(tempfile.mkdtemp())
        self.wav(neg / "a.wav", frames=16000 * 5)
        self.wav(neg / "b.wav", frames=100)   # too short
        built = []
        with mock.patch("sys.stdout"), mock.patch.object(self.train, "build_features",
                                                          lambda work, r, source, target: built.append((source, target)) or (r / target).mkdir()):
            self.assertEqual(self.train.household_negatives(run, run, neg), 1)
        self.assertEqual(built, [("household_negatives", "household_negative_features")])
        cfg = json.loads(self.train.write_training_config(run, run, 100).read_text())
        own = [f for f in cfg["features"] if f["features_dir"].endswith("household_negative_features")]
        self.assertEqual(len(own), 1)
        self.assertIs(own[0]["truth"], False)
        with mock.patch("sys.stdout"):
            self.assertEqual(self.train.household_negatives(run, run, None), 0)
        cfg = json.loads(self.train.write_training_config(run, run, 100).read_text())
        self.assertFalse([f for f in cfg["features"] if "household" in f["features_dir"]], "a set from a run before stayed")

    def test_one_false_wake_still_leaves_a_training_set(self):
        # #825, 2026-10-28: one false wake, and Clips' split (test 0.2) left the train set empty
        neg, run = pathlib.Path(tempfile.mkdtemp()), pathlib.Path(tempfile.mkdtemp())
        self.wav(neg / "a.wav", frames=16000)
        seen = []
        with mock.patch("sys.stdout"), mock.patch.object(self.train, "build_features",
                                                          lambda work, r, source, target: seen.extend(sorted((r / source).glob("*.wav")))):
            self.assertEqual(self.train.household_negatives(run, run, neg), 1, "the count is the household's, not the copies")
        self.assertGreaterEqual(len(seen), self.train.MIN_SPLIT_CLIPS)
        self.assertGreaterEqual(int(len(seen) * 0.8), 1)

    def test_the_features_script_reads_the_set_it_is_given(self):
        script = self.train._FEATURES_SCRIPT
        self.assertIn('input_directory="@SOURCE@"', script)
        self.assertIn('out_root = "@TARGET@"', script)


if __name__ == "__main__":
    unittest.main()
