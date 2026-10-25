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
    def test_the_exposed_names_in_upstreams_order_areas_in_use_then_the_named_things(self):
        p = names.prompt(names.fetch(FakeWS(HOUSE)))
        # areas and floors holding something exposed, then lights/switches/media players with their aliases,
        # then the areas with nothing exposed, then the rest (sensors) — wyoming-faster-whisper's tiers
        self.assertEqual(p, "Wohnzimmer, Stube, Badezimmer, PV Anlage, Erdgeschoss, Sofalicht, Sofa, Couch, Bambi, Badmini, Küche, Obergeschoss, PV Leistung.")

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
