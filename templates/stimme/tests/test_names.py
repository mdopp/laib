"""Whisper hears the household's names: stimme-names.py builds Whisper's initial prompt
from what the household exposed to Assist in Home Assistant, and the units hand it to
Whisper, restarting it only when the list changed. On the box „Schalte Bambi an“
came back as „Schaut er beim Jahn.“: Bambi is a device's name, a word Whisper has
never heard as one."""
import importlib.util, io, json, os, subprocess, sys, tempfile, unittest
from contextlib import redirect_stdout
from pathlib import Path

T = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location("stimme_names", T / "stimme-names.py")
names = importlib.util.module_from_spec(spec)
spec.loader.exec_module(names)


def ent(entity_id, expose=True, area=None, device=None, name=None, disabled=None):
    opts = {} if expose is None else {"conversation": {"should_expose": expose}}
    return {"entity_id": entity_id, "options": opts, "area_id": area, "device_id": device, "name": name, "disabled_by": disabled}


HOUSE = {
    "registry": [ent("light.wohnzimmer_bambi", device="d1"), ent("light.dimmer_2_5", area="wohnzimmer"),
                 ent("media_player.badmini", area="bad"), ent("sensor.wohnzimmer_bambi_signalpegel", expose=False, device="d1"),
                 ent("switch.keller", expose=None), ent("light.alt", disabled="user"), ent("sensor.pv_leistung", area="pv")],
    "states": [{"entity_id": "light.wohnzimmer_bambi", "attributes": {"friendly_name": "Bambi"}},
               {"entity_id": "light.dimmer_2_5", "attributes": {"friendly_name": "Sofalicht"}},
               {"entity_id": "media_player.badmini", "attributes": {"friendly_name": "Badmini"}},
               {"entity_id": "sensor.wohnzimmer_bambi_signalpegel", "attributes": {"friendly_name": "Bambi Signalpegel"}},
               {"entity_id": "switch.keller", "attributes": {"friendly_name": "Kellerpumpe"}},
               {"entity_id": "light.alt", "attributes": {"friendly_name": "Alte Lampe"}},
               {"entity_id": "sensor.pv_leistung", "attributes": {"friendly_name": "PV Leistung"}}],
    "entries": {"light.dimmer_2_5": {"aliases": ["Sofa", None, "Couch"]}, "light.wohnzimmer_bambi": {"aliases": []}},
    "devices": [{"id": "d1", "area_id": "wohnzimmer"}],
    "areas": [{"area_id": "kueche", "name": "Küche", "aliases": [], "floor_id": "eg"}, {"area_id": "wohnzimmer", "name": "Wohnzimmer", "aliases": ["Stube"], "floor_id": "eg"},
              {"area_id": "bad", "name": "Badezimmer", "aliases": []}, {"area_id": "pv", "name": "PV Anlage", "aliases": []}],
    "floors": [{"floor_id": "eg", "name": "Erdgeschoss", "aliases": []}, {"floor_id": "og", "name": "Obergeschoss", "aliases": []}],
}


class FakeWS:
    def __init__(self, house):
        self.house, self.calls = house, []

    def __call__(self, type, **kw):
        self.calls.append(type)
        return {"config/entity_registry/list": self.house["registry"], "get_states": self.house["states"],
                "config/entity_registry/get_entries": {k: v for k, v in self.house["entries"].items() if k in kw.get("entity_ids", [])},
                "config/device_registry/list": self.house["devices"], "config/area_registry/list": self.house["areas"],
                "config/floor_registry/list": self.house["floors"]}[type]

    def close(self):
        pass


class Prompt(unittest.TestCase):
    def test_the_named_things_first_then_the_areas_in_use(self):
        p = names.prompt(names.fetch(FakeWS(HOUSE)))
        # lights/switches/media players with their aliases first (Whisper knows „Küche“, not „Bambi“), then the
        # areas and floors holding something exposed, then the areas with nothing exposed, then the rest (sensors)
        self.assertEqual(p, "Sofalicht, Sofa, Couch, Bambi, Badmini, Wohnzimmer, Stube, Badezimmer, PV Anlage, Erdgeschoss, Küche, Obergeschoss, PV Leistung.")

    def test_the_spellings_home_assistant_adds_cost_no_budget(self):
        # #760: home-assistant-names.py adds „Sofa-Licht“ and „Sofa Licht“ for „Sofalicht“, for HA's intents;
        # Whisper writes them anyway, and each would take a name's place in its 200 tokens
        house = json.loads(json.dumps(HOUSE))
        house["entries"]["light.dimmer_2_5"]["aliases"] += ["Sofa-Licht", "Sofa Licht"]
        self.assertEqual(names.prompt(names.fetch(FakeWS(house))), names.prompt(names.fetch(FakeWS(HOUSE))))

    def test_a_name_late_in_the_alphabet_survives_a_full_prompt(self):
        """the box: 50 exposed things, areas first, cut at the T of the entity ids — light.wohnzimmer_bambi lost"""
        many = dict(HOUSE, registry=[ent(f"light.a{i:02}", area="wohnzimmer") for i in range(40)] + [ent("light.wohnzimmer_bambi", area="wohnzimmer")],
                    states=[{"entity_id": f"light.a{i:02}", "attributes": {"friendly_name": f"Licht {i}"}} for i in range(40)]
                    + [{"entity_id": "light.wohnzimmer_bambi", "attributes": {"friendly_name": "Bambi"}}])
        self.assertIn("Bambi", names.prompt(names.fetch(FakeWS(many)), lambda text: len(text) // 3))

    def test_the_budget_is_counted_with_whispers_tokenizer_when_it_is_there(self):
        with tempfile.TemporaryDirectory() as d:
            self.assertEqual(names.counter(d)("abcdefgh"), 4, "no tokenizer.json: two characters a token")

    def test_what_the_household_did_not_expose_is_not_said(self):
        p = names.prompt(names.fetch(FakeWS(HOUSE)))
        for hidden in ("Signalpegel", "Kellerpumpe", "Alte Lampe"):
            self.assertNotIn(hidden, p, "an entity not exposed to Assist (or disabled) in the prompt")

    def test_the_prompt_stays_inside_whispers_budget_and_cuts_whole_names(self):
        many = dict(HOUSE, registry=[ent(f"light.l{i}", area="wohnzimmer") for i in range(300)],
                    states=[{"entity_id": f"light.l{i}", "attributes": {"friendly_name": f"Leuchte Nummer {i}"}} for i in range(300)])
        p = names.prompt(names.fetch(FakeWS(many)))
        self.assertLessEqual(len(p) // 2, names.BUDGET, "longer than the token budget (upstream's over-estimate: 2 characters a token)")
        self.assertTrue(p.endswith("."), p[-40:])
        self.assertRegex(p, r"Leuchte Nummer \d+\.$", "a name cut in half")

    def test_no_names_no_prompt(self):
        self.assertEqual(names.prompt(names.fetch(FakeWS(dict(HOUSE, registry=[])))), "Küche, Wohnzimmer, Stube, Badezimmer, PV Anlage, Erdgeschoss, Obergeschoss.")
        self.assertEqual(names.prompt(names.fetch(FakeWS(dict(HOUSE, registry=[], areas=[], floors=[])))), "")


# home-assistant's pre-start renders the stations of the share's radio/ as LaibRadio's list (#933): this is the box's
# custom_sentences/de/laib_medien.yaml of 2026-10-31, the lists only; stimme-names.service hands it in as LAIB_SENDER
RENDERED = "# rendered by laib pre-start (templates/home-assistant, #933); do not edit by hand\n" + json.dumps({
    "language": "de", "intents": {}, "lists": {"laib_kuenstler": {"wildcard": True}, "laib_sender": {"values": [
        {"in": "(1LIVE|eins LIVE|Einslive)", "out": "1LIVE"}, {"in": "(Antenne Niedersachsen)", "out": "Antenne Niedersachsen"},
        {"in": "(Deutschlandfunk)", "out": "Deutschlandfunk"}, {"in": "(NDR 2|NDR zwei)", "out": "NDR 2"},
        {"in": "(NDR 2 Niedersachsen|NDR zwei Niedersachsen)", "out": "NDR 2 Niedersachsen"}, {"in": "(radio ffn)", "out": "radio ffn"},
        {"in": "(WDR 2|WDR zwei)", "out": "WDR 2"}, {"in": "(SWR3|SWR drei|Swrdrei)", "out": "SWR3"},
        {"in": "(RADIO BOB)", "out": "RADIO BOB!"}]}}}, ensure_ascii=False, indent=2) + "\n"
SAID = ["Einslive", "Antenne Niedersachsen", "Deutschlandfunk", "NDR zwei", "NDR zwei Niedersachsen", "radio ffn", "WDR zwei", "SWR drei", "Radio Bob"]


class Stations(unittest.TestCase):
    """#961: „Spiele Radio Bob“ came back as „Schade van Bion.“: the prompt had no station and no verb"""

    def test_each_station_once_as_it_is_said(self):
        # a leading number is said with the word (1LIVE: Einslive), a trailing one apart (SWR3: SWR drei), a shouted
        # name as a name (RADIO BOB: Radio Bob); each form is one Assist's LaibRadio list accepts
        self.assertEqual(names.stations(RENDERED), SAID)
        accepted = {f.lower() for v in json.loads(RENDERED.split("\n", 1)[1])["lists"]["laib_sender"]["values"] for f in v["in"].strip("()").split("|")}
        self.assertEqual([s for s in SAID if s.lower() not in accepted], [])

    def test_no_station_list_is_no_station(self):
        for text in ("", "# rendered by laib pre-start\n", "not json", RENDERED.replace("laib_sender", "andere")):
            self.assertEqual(names.stations(text), [], text[:30])

    def test_the_verb_comes_first_then_the_named_things(self):
        p = names.prompt(names.fetch(FakeWS(HOUSE)), first=names.frame(names.stations(RENDERED)))
        self.assertTrue(p.startswith("Spiele Einslive, Antenne Niedersachsen, Deutschlandfunk, NDR zwei, NDR zwei Niedersachsen, radio ffn, "
                                     "WDR zwei, SWR drei, Radio Bob, Spiele Musik von, Sofalicht, Sofa, Couch, Bambi, Badmini, Wohnzimmer"), p)

    def test_without_stations_the_prompt_is_unchanged(self):
        before = names.prompt(names.fetch(FakeWS(HOUSE)))
        self.assertEqual(names.frame([]), [])
        self.assertEqual(names.prompt(names.fetch(FakeWS(HOUSE)), first=names.frame(names.stations(""))), before)

    def test_the_device_names_keep_their_place_and_the_budget_holds(self):
        """the box: 137 of the 200 tokens are the device tier; the stations take what is left, never a device's place"""
        count = lambda text: len(text) // 3
        many = dict(HOUSE, registry=[ent(f"light.a{i:02}", area="wohnzimmer") for i in range(55)] + [ent("light.wohnzimmer_bambi", area="wohnzimmer")],
                    states=[{"entity_id": f"light.a{i:02}", "attributes": {"friendly_name": f"Licht {i}"}} for i in range(55)]
                    + [{"entity_id": "light.wohnzimmer_bambi", "attributes": {"friendly_name": "Bambi"}}])
        tiers = names.fetch(FakeWS(many))
        alone, framed = names.prompt(tiers, count), names.prompt(tiers, count, first=names.frame(names.stations(RENDERED)))
        devices = [n for n in tiers[0] if n + "," in alone + "," or n + "." in alone]
        self.assertIn("Bambi", devices)
        for n in devices:
            self.assertIn(n, framed, "a device name lost its place to a station")
        self.assertIn("Spiele Einslive", framed, "a full budget took every station, the first fits")
        self.assertNotIn("Radio Bob", framed, "the test's budget was not full: it measured nothing")
        self.assertLessEqual(count(framed), names.BUDGET)
        self.assertTrue(framed.startswith("Spiele Einslive, "), framed)
        for budget_text in (alone, framed):
            self.assertLessEqual(count(budget_text), names.BUDGET)

    def test_the_station_list_reaches_the_file(self):
        with tempfile.TemporaryDirectory() as d:
            with redirect_stdout(io.StringIO()):
                names.main(os.path.join(d, "laib-names.txt"), "tok", lambda: FakeWS(HOUSE), RENDERED)
            self.assertTrue(Path(d, "laib-names.txt").read_text().startswith("Spiele Einslive, "))
            out = io.StringIO()
            with redirect_stdout(out):
                names.main(os.path.join(d, "laib-names.txt"), "tok", lambda: FakeWS(HOUSE))
            self.assertIn("names changed", out.getvalue(), "the stations gone, the prompt kept them")
            self.assertEqual(Path(d, "laib-names.txt").read_text(), names.prompt(names.fetch(FakeWS(HOUSE))))


class Run(unittest.TestCase):
    def run_main(self, d, ws):
        out = io.StringIO()
        with redirect_stdout(out):
            code = names.main(os.path.join(d, "laib-names.txt"), "tok", lambda: ws)
        return code, out.getvalue()

    def test_written_once_and_changed_only_when_the_names_change(self):
        with tempfile.TemporaryDirectory() as d:
            code, out = self.run_main(d, FakeWS(HOUSE))
            self.assertEqual(code, 0, out)
            self.assertIn("names changed", out)
            self.assertIn("Bambi", Path(d, "laib-names.txt").read_text())
            code, out = self.run_main(d, FakeWS(HOUSE))
            self.assertEqual((code, "names changed" in out), (0, False), "the same names wrote the file again: Whisper would restart for nothing")
            renamed = json.loads(json.dumps(HOUSE))
            renamed["states"][0]["attributes"]["friendly_name"] = "Rehkitz"
            code, out = self.run_main(d, FakeWS(renamed))
            self.assertIn("names changed", out)
            self.assertIn("Rehkitz", Path(d, "laib-names.txt").read_text())

    def test_home_assistant_away_keeps_the_last_names(self):
        with tempfile.TemporaryDirectory() as d:
            self.run_main(d, FakeWS(HOUSE))

            def away():
                raise ConnectionRefusedError("connection refused")
            out = io.StringIO()
            with redirect_stdout(out):
                code = names.main(os.path.join(d, "laib-names.txt"), "tok", away)
            self.assertEqual(code, 0, "Home Assistant down is its own finding, not Stimme's")
            self.assertNotIn("names changed", out.getvalue())
            self.assertIn("Bambi", Path(d, "laib-names.txt").read_text(), "the last good names were dropped")

    def test_a_refused_token_fails_the_unit(self):
        with tempfile.TemporaryDirectory() as d:
            def refused():
                raise PermissionError("the WebSocket API refused the token")
            with redirect_stdout(io.StringIO()):
                self.assertEqual(names.main(os.path.join(d, "laib-names.txt"), "tok", refused), 1)

    def test_without_a_token_nothing_is_asked(self):
        with tempfile.TemporaryDirectory() as d:
            with redirect_stdout(io.StringIO()):
                self.assertEqual(names.main(os.path.join(d, "laib-names.txt"), "", lambda: self.fail("asked HA without a token")), 0)


class Units(unittest.TestCase):
    def test_whisper_reads_the_names_file_and_it_exists_before_whisper_starts(self):
        u = (T / "stimme-whisper.container").read_text()
        self.assertIn("Environment=WYO_WHISPER_INITIAL_PROMPT_FILE=/config/laib-names.txt", u)
        # a missing _FILE stops wyoming-faster-whisper at its argument parser (env_args._read_env)
        self.assertIn("ExecStartPre=/usr/bin/podman unshare touch ${DATA_DIR}/stimme/whisper/laib-names.txt", u)

    def test_the_names_unit_restarts_whisper_only_on_a_change_and_never_starts_a_stopped_one(self):
        u = (T / "stimme-names.service").read_text()
        self.assertIn("Environment=HA_TOKEN=${HA_TOKEN}", u)
        self.assertIn("HA_TOKEN: !secret STIMME_HA_TOKEN", (T / "values.example.yml").read_text())
        self.assertIn("StandardInput=file:%h/.config/containers/systemd/stimme/stimme-names.py", u)
        self.assertIn("systemctl --user try-restart stimme-whisper.service", u, "restart would start a Whisper the lease stopped")
        self.assertIn('*"names changed"*)', u)
        self.assertIn("-e HA_TOKEN", u)
        self.assertNotIn("${HA_TOKEN}", u.split("ExecStart=", 1)[1], "the token on podman's argv")
        self.assertIn("OnCalendar=", (T / "stimme-names.timer").read_text())

    def test_the_names_unit_hands_in_assists_station_list(self):
        """#961: the stations as LaibRadio renders them, read on the host (Whisper does not mount Home Assistant's config);
        a box without Home Assistant or without stations hands in nothing"""
        u = (T / "stimme-names.service").read_text()
        run = u.split("ExecStart=", 1)[1]
        self.assertIn("${DATA_DIR}/home-assistant/homeassistant/custom_sentences/de/laib_medien.yaml", run)
        self.assertIn("2>/dev/null", run.split("laib_medien.yaml", 1)[1].split(";", 1)[0])
        self.assertIn("-e LAIB_SENDER", run)
        self.assertIn('os.environ.get("LAIB_SENDER"', (T / "stimme-names.py").read_text())

    def test_the_token_is_declared_external_and_optional(self):
        c = (T / "laib.yml").read_text()
        self.assertIn("- name: STIMME_HA_TOKEN", c)
        block = c.split("- name: STIMME_HA_TOKEN", 1)[1].split("- name:", 1)[0]
        self.assertIn("external: true", block)
        self.assertIn("optional: true", block)

    def test_the_names_script_runs_in_whispers_python_with_the_stdlib_only(self):
        src = (T / "stimme-names.py").read_text()
        for line in src.splitlines():
            if line.startswith(("import ", "from ")):
                for mod in line.replace("from ", "").replace("import ", "").split(" ")[0].split(","):
                    self.assertIn(mod.strip().split(".")[0], sys.stdlib_module_names, line)


if __name__ == "__main__":
    unittest.main()
