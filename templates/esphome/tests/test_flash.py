"""#759: the trained „Hey Joshua“ never reached the Voice PE. Its YAML in ESPHome
still loaded only Solaris's model and the README's step was by hand. esphome-flash.py
does it: it finds the devices by their YAML files, points their micro_wake_word at the
house persona's model (Solaris's goes: it is dead), and compiles and uploads each over
the air. Nothing runs it but `laib flash esphome`, which a human approves."""
import importlib.util, io, os, subprocess, sys, tempfile, unittest
from contextlib import redirect_stdout
from pathlib import Path

T = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location("esphome_flash", T / "esphome-flash.py")
flash = importlib.util.module_from_spec(spec)
spec.loader.exec_module(flash)

# the box's device YAML on 2026-10-26 (no secret in it)
BOX = '''# Take-control config for the Nabu Casa Voice PE "home-assistant-voice-0907c9".
packages:
  voice_pe:
    url: https://github.com/esphome/home-assistant-voice-pe
    ref: "26.6.0"
    files: [home-assistant-voice.yaml]
    refresh: 1d

api:
  encryption: !remove

wifi:
  ap:
    ssid: "Solaris Voice Setup"

captive_portal:

micro_wake_word:
  models:
    - model: /config/models/solaris-micro.json
      id: solaris
'''

OURS = "    - model: /config/models/hey_joshua.json\n      id: hey_joshua\n"


class TheYaml(unittest.TestCase):
    def test_the_boxs_device_wakes_on_the_persona_not_on_solaris(self):
        got = flash.with_model(BOX, "hey_joshua")
        self.assertTrue(got.endswith("micro_wake_word:\n  models:\n" + OURS), got)
        self.assertNotIn("solaris-micro", got)
        self.assertEqual(got.split("micro_wake_word:")[0], BOX.split("micro_wake_word:")[0], "the rest of the file stays as it was")

    def test_again_changes_nothing(self):
        once = flash.with_model(BOX, "hey_joshua")
        self.assertEqual(flash.with_model(once, "hey_joshua"), once)

    def test_the_owners_own_models_and_settings_stay(self):
        text = "esphome:\n  name: x\nmicro_wake_word:\n  vad:\n    model: github://esphome/micro-wake-word-models/models/v2/vad.json\n  models:\n" \
               "    - model: okay_nabu\n    - model: /config/models/solaris-micro.json\n      id: solaris\n    - model: hey_mycroft\nlogger:\n"
        got = flash.with_model(text, "hey_joshua")
        self.assertIn("  vad:\n    model: github://esphome/micro-wake-word-models/models/v2/vad.json\n", got)
        self.assertIn("    - model: okay_nabu\n    - model: hey_mycroft\n" + OURS + "logger:\n", got)

    def test_a_device_without_the_block_gets_one(self):
        got = flash.with_model("packages:\n  voice_pe: github://esphome/home-assistant-voice-pe/home-assistant-voice.yaml\n", "hey_joshua")
        self.assertTrue(got.endswith("\nmicro_wake_word:\n  models:\n" + OURS), got)
        got = flash.with_model("micro_wake_word:\n  stop_after_detection: false\nota:\n", "hey_joshua")
        self.assertEqual(got, "micro_wake_word:\n  stop_after_detection: false\n  models:\n" + OURS + "ota:\n")

    def test_a_shape_it_does_not_know_is_left_to_a_person(self):
        with self.assertRaises(ValueError):
            flash.with_model("micro_wake_word:\n  models: [okay_nabu]\n", "hey_joshua")


class TheDevices(unittest.TestCase):
    def setUp(self):
        self.d = Path(tempfile.mkdtemp())
        (self.d / "models").mkdir()
        for f in ("hey_joshua.json", "hey_joshua.tflite"):
            (self.d / "models" / f).write_text("x")
        (self.d / "home-assistant-voice-0907c9.yaml").write_text(BOX)
        (self.d / "secrets.yaml").write_text("wifi_ssid: x\nmicro_wake_word: no\n")
        (self.d / "steckdose.yaml").write_text("esphome:\n  name: steckdose\n")
        self.runs = []

    def run_main(self, rc=0):
        def run(cmd):
            self.runs.append(cmd)
            return rc
        out = io.StringIO()
        with redirect_stdout(out):
            code = flash.main(self.d, "hey_joshua", run)
        return code, out.getvalue()

    def test_only_the_wake_word_devices_by_their_files(self):
        self.assertEqual([p.name for p in flash.devices(self.d)], ["home-assistant-voice-0907c9.yaml"])

    def test_each_device_is_edited_and_uploaded_over_the_air(self):
        code, out = self.run_main()
        self.assertEqual(code, 0, out)
        self.assertEqual(self.runs, [["esphome", "run", "--no-logs", "--device", "OTA", str(self.d / "home-assistant-voice-0907c9.yaml")]])
        self.assertIn("hey_joshua.json", (self.d / "home-assistant-voice-0907c9.yaml").read_text())
        self.assertEqual((self.d / "home-assistant-voice-0907c9.yaml.laib-vorher").read_text(), BOX, "the YAML from before stays beside it")
        self.assertEqual((self.d / "steckdose.yaml").read_text(), "esphome:\n  name: steckdose\n")

    def test_a_failed_upload_fails_the_unit(self):
        code, out = self.run_main(rc=2)
        self.assertEqual(code, 1)
        self.assertIn("home-assistant-voice-0907c9", out)

    def test_without_the_model_nothing_is_touched(self):
        (self.d / "models" / "hey_joshua.tflite").unlink()
        code, out = self.run_main()
        self.assertEqual((code, self.runs), (1, []))
        self.assertEqual((self.d / "home-assistant-voice-0907c9.yaml").read_text(), BOX)

    def test_no_device_is_a_failure_that_says_so(self):
        (self.d / "home-assistant-voice-0907c9.yaml").unlink()
        code, out = self.run_main()
        self.assertEqual((code, self.runs), (1, []))
        self.assertIn("micro_wake_word", out)


class TheUnit(unittest.TestCase):
    def test_only_laib_flash_starts_it(self):
        unit = (T / "esphome-flash.service").read_text()
        self.assertNotIn("[Install]", unit, "a oneshot without [Install]: apply and restart never start it")
        timers = [p.read_text() for p in T.iterdir() if p.suffix == ".timer"]
        self.assertFalse([t for t in timers if "esphome-flash" in t], "no timer starts the flash (esphome-check's, #785, starts only the check)")
        self.assertFalse((T / "esphome-flash.timer").exists())
        self.assertIn("${DATA_DIR}/stimme/micro/${PERSONA_WAKEWORD}.tflite", unit, "the host copies stimme's trained model")
        self.assertIn("${DATA_DIR}/esphome/config/models", unit)
        self.assertIn("Environment=WAKE_ID=${PERSONA_WAKEWORD}", unit)
        self.assertIn("StandardInput=file:%h/.config/containers/systemd/esphome/esphome-flash.py", unit)

    def test_stimme_learns_which_manifest_the_devices_run(self):
        # #825: a new cutoff in the trained manifest offers the flash again; only a flash that went
        # through says what the devices run now (ExecStartPost runs only after a green ExecStart)
        unit = (T / "esphome-flash.service").read_text()
        post = [l for l in unit.splitlines() if l.startswith("ExecStartPost=")]
        self.assertEqual(post, ["ExecStartPost=/usr/bin/podman unshare cp -f ${DATA_DIR}/esphome/config/models/${PERSONA_WAKEWORD}.json "
                                "${DATA_DIR}/stimme/micro/${PERSONA_WAKEWORD}.geflasht.json"])


if __name__ == "__main__":
    unittest.main()
