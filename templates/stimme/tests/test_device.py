"""#759, ADR 0030: the wake word is „Hey Mycroft“, which the Voice PE offers from the factory.
stimme-names, which reads Home Assistant every 15 minutes anyway, also looks at the
satellites' wake-word selects: a device that offers it gets it selected, once, and the
status says „auf dem Gerät“; one that does not is named with what to do."""
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

    def look(self, ws):
        with redirect_stdout(io.StringIO()):
            names.wake(lambda: ws, self.micro, "Hey Mycroft")
        return json.loads((self.micro / "laib-status.json").read_text())

    def test_offered_it_is_selected_once_and_the_status_says_so(self):
        ws = FakeWS(house(["Okay Nabu", "Hey Jarvis", "Hey Mycroft"]))
        s = self.look(ws)
        self.assertEqual(ws.selected, [(SELECT, "Hey Mycroft")])
        self.assertEqual((s["detail"], s["findings"], s["geraet"]), ("Wake-Word „Hey Mycroft“ auf dem Gerät „Voice 0907c9“", [], True))
        again = FakeWS(house(["Okay Nabu", "Hey Mycroft"], state="Okay Nabu"))
        self.look(again)
        self.assertEqual(again.selected, [], "the owner set it back by hand: Stimme selects only when the device first offers it")

    def test_a_device_without_it_is_named_with_what_to_do_and_no_repair(self):
        s = self.look(FakeWS(house(["Okay Nabu", "Hey Joshua"])))
        self.assertEqual(s["detail"], "Wake-Word „Hey Mycroft“ noch auf keinem Gerät")
        self.assertIs(s["geraet"], False)
        f = s["findings"][0]
        self.assertEqual((f["severity"], f["topic"]), ("gelb", "wakeword"))
        self.assertNotIn("repair", f, "nothing is trained or flashed any more")
        self.assertIn("Voice 0907c9", f["detail"])
        self.assertIn("hey_mycroft", f["fix"])

    def test_no_satellite_or_one_away_is_no_measurement(self):
        for h in (dict(house([]), registry=[]), house([], state="unavailable")):
            s = self.look(FakeWS(h))
            self.assertEqual(s["detail"], "Wake-Word „Hey Mycroft“ noch auf keinem Gerät")
            self.assertTrue(s["findings"][0]["unmeasured"], s)
            self.assertNotIn("repair", s["findings"][0])

    def test_home_assistant_away_keeps_the_last_look(self):
        (self.micro / "laib-status.json").write_text('{"detail": "vorher"}')

        def away():
            raise ConnectionRefusedError("refused")
        with redirect_stdout(io.StringIO()):
            names.wake(away, self.micro, "Hey Mycroft")
        self.assertEqual((self.micro / "laib-status.json").read_text(), '{"detail": "vorher"}')

    def test_the_status_line_counts_the_wakes(self):
        # #825: „heute 2× geweckt, 2 ohne Befehl“ says how often it wakes for nothing
        today, before = time.strftime("%Y-%m-%d"), time.strftime("%Y-%m-%d", time.localtime(time.time() - 86400))
        (self.micro / "laib-wakes.json").write_text(json.dumps({"tage": {before: {"geweckt": 5, "ohne": 1}, today: {"geweckt": 2, "ohne": 2}}}))
        s = self.look(FakeWS(house(["Okay Nabu", "Hey Mycroft"], state="Hey Mycroft")))
        self.assertEqual(s["detail"], "Wake-Word „Hey Mycroft“ auf dem Gerät „Voice 0907c9“; heute 2× geweckt, 2 ohne Befehl")
        self.assertIn("5× geweckt, 1 ohne Befehl", s["einzelheiten"])
        (self.micro / "laib-wakes.json").write_text(json.dumps({"tage": {before: {"geweckt": 5, "ohne": 1}}}))
        s = self.look(FakeWS(house(["Okay Nabu", "Hey Mycroft"], state="Hey Mycroft")))
        self.assertTrue(s["detail"].endswith("; heute nicht geweckt"), s["detail"])

    def test_without_counts_the_line_claims_none(self):
        s = self.look(FakeWS(house(["Okay Nabu", "Hey Mycroft"], state="Hey Mycroft")))
        self.assertNotIn("geweckt", s["detail"])

    def test_the_units_hand_it_over(self):
        u, w = (T / "stimme-names.service").read_text(), (T / "stimme-whisper.container").read_text()
        self.assertIn('Environment="WAKE_PHRASE=Hey Mycroft"', u, "quoted: systemd splits at the space (#627)")
        self.assertIn("-e WAKE_PHRASE -e TZ", u, "#825: „heute“ is the box's day")
        self.assertIn("Environment=TZ=${TZ}", u)
        self.assertIn("Volume=${DATA_DIR}/stimme/micro:/micro:z", w)
        self.assertNotIn("recordings", w + u, "no household audio is kept")


if __name__ == "__main__":
    unittest.main()
