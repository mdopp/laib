"""#828: Haus records the family saying the wake word into recordings/<id>/haus as
<person>-<YYYYMMDD-HHMMSS>.wav. stimme-names counts them in Stimme's status (positives per
person, the false wakes under negativ/, and what is new since the model's run began) and,
with enough new ones, offers „neu trainieren“: `laib retrain stimme`, which the owner
approves in the Inbox. Nothing trains by itself. After the run the flash follows (#759)."""
import importlib.util, io, json, os, tempfile, time, unittest, wave
from contextlib import redirect_stdout
from pathlib import Path

T = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location("stimme_names", T / "stimme-names.py")
names = importlib.util.module_from_spec(spec)
spec.loader.exec_module(names)

SELECT = "select.home_assistant_voice_0907c9_aktivierungswort"
BEGAN = "2026-10-20T10:00:00Z"
OLD = time.mktime((2026, 10, 1, 12, 0, 0, 0, 0, -1))


def house(state="Hey Joshua"):
    return {"registry": [{"entity_id": SELECT, "translation_key": "wake_word"}],
            "states": [{"entity_id": SELECT, "state": state, "attributes": {"friendly_name": "Voice 0907c9 Aktivierungswort", "options": ["Okay Nabu", "Hey Joshua"]}}]}


class FakeWS:
    def __init__(self, h):
        self.h = h

    def __call__(self, type, **kw):
        return None if type == "call_service" else {"config/entity_registry/list": self.h["registry"], "get_states": self.h["states"]}[type]

    def close(self):
        pass


class Recordings(unittest.TestCase):
    def setUp(self):
        self.micro, root = Path(tempfile.mkdtemp()), Path(tempfile.mkdtemp())
        self.root = root / "hey_joshua"
        (self.micro / "hey_joshua.status.json").write_text(json.dumps({"state": "done", "phrase": "Hey Joshua", "begonnen": BEGAN}))
        (self.micro / "hey_joshua.tflite").write_text("x")
        manifest = json.dumps({"micro": {"probability_cutoff": 0.97}})
        (self.micro / "hey_joshua.json").write_text(manifest)
        (self.micro / "hey_joshua.geflasht.json").write_text(manifest)

    def wav(self, rel, old=False, channels=1, frames=24000):
        p = self.root / rel
        p.parent.mkdir(parents=True, exist_ok=True)
        with wave.open(str(p), "wb") as w:
            w.setnchannels(channels)
            w.setsampwidth(2)
            w.setframerate(16000)
            w.writeframes(b"\x00\x30" * frames * channels)
        if old:
            os.utime(p, (OLD, OLD))

    def look(self):
        out = io.StringIO()
        with redirect_stdout(out):
            names.wake(lambda: FakeWS(house()), self.micro, "hey_joshua", "Hey Joshua", self.root.parent)
        self.printed = out.getvalue()
        return json.loads((self.micro / "laib-status.json").read_text())

    def retrain(self, s):
        return [f for f in s["findings"] if f.get("repair") == ["retrain", "stimme"]]

    def test_the_status_counts_them_per_person_and_what_is_new(self):
        for i in range(3):
            self.wav(f"haus/anna-2026101{i}-120000.wav", old=True)
        self.wav("haus/ben-20261025-120000.wav")
        self.wav("haus/.ben-20261025-120100.wav.part")   # Haus' temp file
        self.wav("mama.wav", old=True)                    # one the owner put there by hand
        for i in range(2):
            self.wav(f"negativ/2026102{i}T120000000.wav")
        s = self.look()
        self.assertIn("Aufnahmen: anna 3, ben 1, 1 weitere, 2 Fehlalarme; seit dem Training 1 neue Aufnahme, 2 neue Fehlalarme", s["detail"])
        self.assertEqual(self.retrain(s), [], "below the thresholds nothing is offered")
        for who in ("anna", "ben", "mama"):
            self.assertNotIn(who, self.printed, "the journal gets counts, never who")

    def test_five_new_recordings_offer_a_retrain_the_owner_approves(self):
        for i in range(4):
            self.wav(f"haus/anna-2026102{i}-120000.wav")
        self.wav("haus/ben-20261025-120000.wav", channels=2)   # not what the trainer takes: not counted as new
        self.assertEqual(self.retrain(self.look()), [])
        self.wav("haus/ben-20261025-120500.wav")
        s = self.look()
        f = self.retrain(s)
        self.assertEqual(len(f), 1, s)
        f = f[0]
        self.assertEqual(s["findings"][0]["repair"], ["retrain", "stimme"], "laib repair status stimme takes the first repair")
        self.assertEqual((f["severity"], f["topic"]), ("gelb", "training"))
        self.assertIn("5 neue Aufnahmen", f["detail"])
        self.assertIn("laib repair status stimme", f["fix"])
        self.assertEqual(f["does"], "„Hey Joshua“ wird mit euren Aufnahmen neu trainiert")
        for said in ("beiseite", "Stunden", "mit dem alten"):
            self.assertIn(said, f["effect"])
        self.assertIn("stimme/micro/vorher", f["undo"])
        self.assertIn("1 unbrauchbar", s["einzelheiten"])

    def test_fifty_new_false_wakes_offer_it_too(self):
        for i in range(49):
            self.wav(f"negativ/20261025T1200{i:02d}000.wav")
        self.assertEqual(self.retrain(self.look()), [])
        self.wav("negativ/20261025T130000000.wav")
        self.assertEqual(len(self.retrain(self.look())), 1)

    def test_without_the_runs_start_the_model_file_is_the_line(self):
        (self.micro / "hey_joshua.status.json").write_text(json.dumps({"state": "done", "phrase": "Hey Joshua"}))
        os.utime(self.micro / "hey_joshua.tflite", (OLD + 86400, OLD + 86400))
        for i in range(5):
            self.wav(f"haus/anna-2026100{i}-120000.wav", old=True)
        self.assertEqual(self.retrain(self.look()), [])
        self.wav("haus/anna-20261026-120000.wav")
        self.assertIn("1 neue Aufnahme", self.look()["detail"])

    def test_moved_aside_it_waits_for_the_trainer_and_offers_nothing(self):
        for f in ("hey_joshua.tflite", "hey_joshua.json", "hey_joshua.geflasht.json"):
            (self.micro / f).unlink()
        with redirect_stdout(io.StringIO()):
            names.wake(lambda: self.fail("asked HA for a word in training"), self.micro, "hey_joshua", "Hey Joshua", self.root.parent)
        s = json.loads((self.micro / "laib-status.json").read_text())
        self.assertEqual(s["findings"], [])
        self.assertIn("wird neu trainiert", s["detail"])

    def test_after_the_run_the_flash_follows(self):
        for i in range(6):
            self.wav(f"haus/anna-2026102{i}-120000.wav")
        (self.micro / "hey_joshua.geflasht.json").unlink()   # stimme-retrain moved it aside with the model
        now = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(time.time() + 60))
        (self.micro / "hey_joshua.status.json").write_text(json.dumps({"state": "done", "phrase": "Hey Joshua", "begonnen": now}))
        s = self.look()
        self.assertEqual(self.retrain(s), [])
        self.assertEqual([f["repair"] for f in s["findings"] if f.get("repair")], [["flash", "esphome"]])

    def test_the_units_hand_them_over(self):
        w, r = (T / "stimme-whisper.container").read_text(), (T / "stimme-retrain.service").read_text()
        self.assertIn("Volume=${DATA_DIR}/stimme/recordings:/recordings:ro\n", w)
        self.assertNotIn("[Install]", r, "only laib retrain starts it")
        self.assertIn("try-restart stimme-wakeword-trainer.service", r)


if __name__ == "__main__":
    unittest.main()
