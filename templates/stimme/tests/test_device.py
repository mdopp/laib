"""#759: the trained „Hey Joshua“ never reached the Voice PE, and `laib status stimme`
said „trainiert“ as if it were done. stimme-names, which reads Home Assistant every 15
minutes anyway, now also looks at the satellites' wake-word selects: until a device
offers the phrase, Stimme's status says „trainiert, noch nicht auf dem Gerät“ with the
repair that flashes it (`laib flash esphome`, a human approves). Once the device offers
it, the select is set to it, once, and the status says „auf dem Gerät“."""
import importlib.util, io, json, tempfile, time, unittest
from contextlib import redirect_stdout
from pathlib import Path

T = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location("stimme_names", T / "stimme-names.py")
names = importlib.util.module_from_spec(spec)
spec.loader.exec_module(names)

SELECT = "select.home_assistant_voice_0907c9_aktivierungswort"


def house(options, state="Okay Nabu"):
    return {"registry": [{"entity_id": SELECT, "translation_key": "wake_word"}, {"entity_id": "select.x_wake_word_2", "translation_key": "wake_word_n"},
                         {"entity_id": "light.bambi", "translation_key": None}],
            "states": [{"entity_id": SELECT, "state": state, "attributes": {"friendly_name": "Voice 0907c9 Aktivierungswort", "options": options}},
                       {"entity_id": "light.bambi", "state": "on", "attributes": {}}]}


class FakeWS:
    def __init__(self, h):
        self.h, self.selected = h, []

    def __call__(self, type, **kw):
        if type == "call_service":
            self.selected.append((kw["target"]["entity_id"], kw["service_data"]["option"]))
            return None
        return {"config/entity_registry/list": self.h["registry"], "get_states": self.h["states"]}[type]

    def close(self):
        pass


class OnTheDevice(unittest.TestCase):
    def setUp(self):
        self.micro = Path(tempfile.mkdtemp())
        (self.micro / "hey_joshua.status.json").write_text(json.dumps({"state": "done", "phrase": "Hey Joshua"}))
        (self.micro / "hey_joshua.tflite").write_text("x")

    def look(self, ws):
        with redirect_stdout(io.StringIO()):
            names.wake(lambda: ws, self.micro, "hey_joshua", "Hey Joshua")
        return json.loads((self.micro / "laib-status.json").read_text())

    def test_trained_but_not_on_the_device_says_so_with_the_flash(self):
        s = self.look(FakeWS(house(["Okay Nabu", "Hey Jarvis", "Hey Mycroft"])))
        self.assertEqual(s["detail"], "Wake-Word „Hey Joshua“ trainiert, noch nicht auf dem Gerät")
        self.assertIs(s["geraet"], False)
        f = s["findings"][0]
        self.assertEqual((f["severity"], f["topic"], f["repair"]), ("gelb", "wakeword", ["flash", "esphome"]))
        self.assertIn("Voice 0907c9", f["detail"])
        self.assertIn("laib repair status stimme", f["fix"])
        for said in ("startet", "2 Minuten", "„Hey Joshua“"):
            self.assertIn(said, f["effect"], "the Inbox card says what happens to the device")
        self.assertTrue(f["undo"])

    def test_on_the_device_it_is_selected_once_and_the_status_says_so(self):
        ws = FakeWS(house(["Okay Nabu", "Hey Joshua"]))
        s = self.look(ws)
        self.assertEqual(ws.selected, [(SELECT, "Hey Joshua")])
        self.assertEqual((s["detail"], s["findings"], s["geraet"]), ("Wake-Word „Hey Joshua“ auf dem Gerät „Voice 0907c9“", [], True))
        again = FakeWS(house(["Okay Nabu", "Hey Joshua"], state="Okay Nabu"))
        self.look(again)
        self.assertEqual(again.selected, [], "the owner set it back by hand: Stimme selects only when the device first offers it")

    def test_no_satellite_or_one_away_is_no_measurement(self):
        for h in (dict(house([]), registry=[]), house([], state="unavailable")):
            s = self.look(FakeWS(h))
            self.assertEqual(s["detail"], "Wake-Word „Hey Joshua“ trainiert, noch nicht auf dem Gerät")
            self.assertTrue(s["findings"][0]["unmeasured"], s)
            self.assertNotIn("repair", s["findings"][0])

    def test_while_the_trainer_trains_its_status_stays(self):
        (self.micro / "hey_joshua.status.json").write_text(json.dumps({"state": "training"}))
        (self.micro / "laib-status.json").write_text('{"detail": "wird trainiert"}')
        with redirect_stdout(io.StringIO()):
            names.wake(lambda: self.fail("asked HA for an untrained word"), self.micro, "hey_joshua", "Hey Joshua")
        self.assertEqual((self.micro / "laib-status.json").read_text(), '{"detail": "wird trainiert"}')

    def test_home_assistant_away_keeps_the_last_look(self):
        (self.micro / "laib-status.json").write_text('{"detail": "vorher"}')

        def away():
            raise ConnectionRefusedError("refused")
        with redirect_stdout(io.StringIO()):
            names.wake(away, self.micro, "hey_joshua", "Hey Joshua")
        self.assertEqual((self.micro / "laib-status.json").read_text(), '{"detail": "vorher"}')

    def manifest(self, name, cutoff):
        (self.micro / name).write_text(json.dumps({"wake_word": "Hey Joshua", "micro": {"probability_cutoff": cutoff, "sliding_window_size": 5}}))

    def test_a_new_cutoff_offers_the_flash_again(self):
        # #825: the trainer wrote WAKE_CUTOFF 0.97 into the manifest; the device still runs 0.95
        self.manifest("hey_joshua.json", 0.97)
        self.manifest("hey_joshua.geflasht.json", 0.95)
        s = self.look(FakeWS(house(["Okay Nabu", "Hey Joshua"], state="Hey Joshua")))
        self.assertIs(s["geraet"], False)
        f = s["findings"][0]
        self.assertEqual((f["severity"], f["topic"], f["repair"]), ("gelb", "wakeword", ["flash", "esphome"]))
        for said in ("0.95", "0.97", "Voice 0907c9"):
            self.assertIn(said, f["detail"])
        for said in ("startet", "2 Minuten", "seltener"):
            self.assertIn(said, f["effect"])
        self.manifest("hey_joshua.geflasht.json", 0.97)   # esphome's flash unit, after the owner approved
        s = self.look(FakeWS(house(["Okay Nabu", "Hey Joshua"], state="Hey Joshua")))
        self.assertEqual((s["findings"], s["geraet"]), ([], True))

    def test_flashed_before_laib_kept_a_copy_is_flashed_again(self):
        self.manifest("hey_joshua.json", 0.97)
        s = self.look(FakeWS(house(["Okay Nabu", "Hey Joshua"], state="Hey Joshua")))
        self.assertEqual(s["findings"][0]["repair"], ["flash", "esphome"])
        self.assertIn("älterer Stand", s["findings"][0]["detail"])

    def test_not_on_the_device_yet_offers_one_flash_not_two(self):
        self.manifest("hey_joshua.json", 0.97)
        s = self.look(FakeWS(house(["Okay Nabu"])))
        self.assertEqual(len([f for f in s["findings"] if f.get("repair")]), 1)

    def test_the_status_line_counts_the_wakes(self):
        # #825: „heute 2× geweckt, 2 ohne Befehl“ judges the cutoff by numbers
        today, before = time.strftime("%Y-%m-%d"), time.strftime("%Y-%m-%d", time.localtime(time.time() - 86400))
        (self.micro / "laib-wakes.json").write_text(json.dumps({"tage": {before: {"geweckt": 5, "ohne": 1}, today: {"geweckt": 2, "ohne": 2}},
                                                                "gegenbeispiele": 2}))
        s = self.look(FakeWS(house(["Okay Nabu", "Hey Joshua"], state="Hey Joshua")))
        self.assertEqual(s["detail"], "Wake-Word „Hey Joshua“ auf dem Gerät „Voice 0907c9“; heute 2× geweckt, 2 ohne Befehl")
        self.assertIn("5× geweckt, 1 ohne Befehl", s["einzelheiten"])
        self.assertIn("2 Fehlalarme als Gegenbeispiele", s["einzelheiten"])
        (self.micro / "laib-wakes.json").write_text(json.dumps({"tage": {before: {"geweckt": 5, "ohne": 1}}}))
        s = self.look(FakeWS(house(["Okay Nabu", "Hey Joshua"], state="Hey Joshua")))
        self.assertTrue(s["detail"].endswith("; heute nicht geweckt"), s["detail"])

    def test_without_counts_the_line_claims_none(self):
        s = self.look(FakeWS(house(["Okay Nabu", "Hey Joshua"], state="Hey Joshua")))
        self.assertNotIn("geweckt", s["detail"])

    def test_the_units_hand_it_over(self):
        u, w = (T / "stimme-names.service").read_text(), (T / "stimme-whisper.container").read_text()
        self.assertIn("Environment=WAKE_ID=${PERSONA_WAKEWORD}", u)
        self.assertIn('Environment="WAKE_PHRASE=${PERSONA_PHRASE}"', u, "quoted: systemd splits at the space (#627)")
        self.assertIn("-e WAKE_ID -e WAKE_PHRASE -e TZ", u, "#825: „heute“ is the box's day")
        self.assertIn("Environment=TZ=${TZ}", u)
        self.assertIn("Volume=${DATA_DIR}/stimme/micro:/micro:z", w)


if __name__ == "__main__":
    unittest.main()
