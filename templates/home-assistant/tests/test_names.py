"""#760: the names as Whisper writes them. „Schalte Sofa-Licht aus“ was heard
right and missed HA's own intents, because the light is named „Sofalicht“:
home-assistant-names.py adds the hyphenated and the split spelling of every
compound as an alias, keeps the owner's, and changes only its own."""
import importlib.util, json, tempfile, unittest
from pathlib import Path

spec = importlib.util.spec_from_file_location("names", Path(__file__).resolve().parent.parent / "home-assistant-names.py")
names = importlib.util.module_from_spec(spec)
spec.loader.exec_module(names)


class Forms(unittest.TestCase):
    def test_a_compound_gets_its_hyphenated_and_its_split_spelling(self):
        self.assertEqual(names.forms("Sofalicht"), ["Sofa-Licht", "Sofa Licht"])
        self.assertEqual(names.forms("Deckenlampe"), ["Decken-Lampe", "Decken Lampe"])
        self.assertEqual(names.forms("Haustür"), ["Haus-Tür", "Haus Tür"])
        self.assertEqual(names.forms("Steckdosenleiste"), ["Steckdosen-Leiste", "Steckdosen Leiste"], "the longest noun at the end only")

    def test_a_hyphenated_name_gets_the_joined_and_the_split_one(self):
        self.assertEqual(names.forms("Sofa-Licht"), ["Sofalicht", "Sofa Licht"])

    def test_a_word_of_a_longer_name_and_the_case(self):
        self.assertEqual(names.forms("Kleine Tischlampe"), ["Kleine Tisch-Lampe", "Kleine Tisch Lampe"])
        self.assertEqual(names.forms("sofalicht"), ["sofa-licht", "sofa licht"])

    def test_nothing_is_guessed_beyond_the_listed_nouns(self):
        for name in ("Spots", "Couch", "Licht", "Lampe", "Fenster", "Schlicht", "Ventilator", "Monitor", "Rote Lampe", "Bad 2", "Sofa Licht"):
            self.assertEqual(names.forms(name), [], name)
        self.assertEqual(names.forms("Deckenventilator"), ["Decken-Ventilator", "Decken Ventilator"])


class Plan(unittest.TestCase):
    def test_laib_adds_its_spellings_after_the_owners_and_keeps_has_name_entry(self):
        changes, state = names.plan({"light.a": ("Sofalicht", [None, "Spots", "Couch"])}, {})
        self.assertEqual(changes, {"light.a": [None, "Spots", "Couch", "Sofa-Licht", "Sofa Licht"]})
        self.assertEqual(state, {"light.a": {"added": ["Sofa-Licht", "Sofa Licht"], "declined": []}})

    def test_a_second_run_changes_nothing(self):
        aliases = [None, "Spots", "Sofa-Licht", "Sofa Licht"]
        changes, state = names.plan({"light.a": ("Sofalicht", aliases)}, {"light.a": {"added": ["Sofa-Licht", "Sofa Licht"]}})
        self.assertEqual(changes, {})
        self.assertEqual(state["light.a"]["added"], ["Sofa-Licht", "Sofa Licht"])

    def test_an_owner_alias_is_a_source_and_never_doubled(self):
        changes, _ = names.plan({"light.a": ("Lampe", ["Leselicht", "sofa licht", "Sofalicht"])}, {})
        self.assertEqual(changes, {"light.a": ["Leselicht", "sofa licht", "Sofalicht", "Lese-Licht", "Lese Licht", "Sofa-Licht"]})

    def test_a_renamed_entity_loses_only_laibs_old_spellings(self):
        changes, state = names.plan({"light.a": ("Stehlampe", ["Spots", "Sofa-Licht", "Sofa Licht"])},
                                    {"light.a": {"added": ["Sofa-Licht", "Sofa Licht"]}})
        self.assertEqual(changes, {"light.a": ["Spots", "Steh-Lampe", "Steh Lampe"]})
        self.assertEqual(state["light.a"]["added"], ["Steh-Lampe", "Steh Lampe"])

    def test_one_the_owner_deleted_is_not_added_again(self):
        changes, state = names.plan({"light.a": ("Sofalicht", ["Sofa Licht"])}, {"light.a": {"added": ["Sofa-Licht", "Sofa Licht"]}})
        self.assertEqual(changes, {})
        self.assertEqual(state["light.a"], {"added": ["Sofa Licht"], "declined": ["Sofa-Licht"]})

    def test_an_entity_no_longer_exposed_loses_laibs_and_keeps_the_owners(self):
        changes, state = names.plan({"light.a": (None, ["Spots", "Sofa-Licht"])}, {"light.a": {"added": ["Sofa-Licht"]}})
        self.assertEqual(changes, {"light.a": ["Spots"]})
        self.assertEqual(state, {})

    def test_an_entity_gone_from_the_registry_is_forgotten(self):
        self.assertEqual(names.plan({}, {"light.gone": {"added": ["Sofa-Licht"]}}), ({}, {}))


class FakeWS:
    def __init__(self, registry, states, entries, admin=True):
        self.registry, self.states, self.entries, self.admin, self.updates = registry, states, entries, admin, []

    def __call__(self, type, **kw):
        if type == "config/entity_registry/update":
            if not self.admin:
                raise RuntimeError("config/entity_registry/update: Unauthorized")
            self.updates.append((kw["entity_id"], kw["aliases"]))
            self.entries[kw["entity_id"]]["aliases"] = kw["aliases"]
            return {}
        return {"config/entity_registry/list": self.registry, "get_states": self.states,
                "config/entity_registry/get_entries": {k: v for k, v in self.entries.items() if k in kw.get("entity_ids", [])}}[type]

    def close(self):
        pass


def house():
    exposed = {"conversation": {"should_expose": True}}
    registry = [{"entity_id": "light.a", "options": exposed}, {"entity_id": "switch.b", "options": exposed},
                {"entity_id": "light.hidden", "options": {"conversation": {"should_expose": False}}},
                {"entity_id": "light.off", "options": exposed, "disabled_by": "user"}]
    states = [{"entity_id": "light.a", "attributes": {"friendly_name": "Sofalicht"}},
              {"entity_id": "switch.b", "attributes": {"friendly_name": "Kaffeemaschine"}},
              {"entity_id": "light.hidden", "attributes": {"friendly_name": "Gartenlicht"}}]
    entries = {"light.a": {"aliases": [None, "Spots"]}, "switch.b": {"aliases": []}, "light.hidden": {"aliases": []},
               "light.off": {"aliases": []}}
    return registry, states, entries


class Run(unittest.TestCase):
    def test_only_exposed_entities_get_spellings_and_the_state_is_written(self):
        reg, states, entries = house()
        ws = FakeWS(reg, states, entries)
        with tempfile.TemporaryDirectory() as d:
            path = str(Path(d) / ".laib-aliases.json")
            self.assertEqual(names.main(path, "tok", lambda: ws), 0)
            self.assertEqual(ws.updates, [("light.a", [None, "Spots", "Sofa-Licht", "Sofa Licht"])])
            self.assertEqual(json.loads(Path(path).read_text())["entities"], {"light.a": {"added": ["Sofa-Licht", "Sofa Licht"], "declined": []}})
            ws.updates.clear()
            self.assertEqual(names.main(path, "tok", lambda: ws), 0)
            self.assertEqual(ws.updates, [], "a second run changed something")

    def test_without_a_token_nothing_happens_and_a_refusal_is_red(self):
        reg, states, entries = house()
        self.assertEqual(names.main("/nonexistent/x.json", "", lambda: self.fail("connected without a token")), 0)
        with tempfile.TemporaryDirectory() as d:
            self.assertEqual(names.main(str(Path(d) / "s.json"), "tok", lambda: FakeWS(reg, states, entries, admin=False)), 1)

    def test_home_assistant_away_is_no_failure(self):
        def away():
            raise ConnectionRefusedError("refused")
        self.assertEqual(names.main("/nonexistent/x.json", "tok", away), 0)


if __name__ == "__main__":
    unittest.main()
