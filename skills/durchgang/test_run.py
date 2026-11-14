"""durchgang's script (Durchgang rot, owner 2026-11-14), on the box's shapes
of that morning: #542 (five images), #543 (Home Assistant's extensions) and
#546 (Homes) in one run, red on #543, rolled back as a whole, with the two
follow-ups #547 and #548. The runner-level test is operator/durchgang_test.go."""

import importlib.util
import json
import os
import sys
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(HERE))
import laibskill  # noqa: E402

spec = importlib.util.spec_from_file_location("durchgang", os.path.join(HERE, "run.py"))
run = importlib.util.module_from_spec(spec)
spec.loader.exec_module(run)
run.sleep = lambda _: None

RUN = "#542, #543, #546 (f548fec..7fc9485)"
RED = {"t": "2026-11-14T09:55:20.964781015Z", "actor": "human:mdopp", "verb": "apply", "result": "red", "reason": "Freigaben " + RUN,
       "detail": "home-assistant: post-deploy exit status 1\nhome-assistant: update.install update.meross_lan_update v6.0.1: HTTP 500\nhome-assistant: its home is box.yml zuhause"}
BACK = {"t": "2026-11-14T09:57:01.9Z", "actor": "human:mdopp", "verb": "apply", "result": "green", "reason": "rollback " + RUN}
CARDS = [
    {"id": 542, "status": "approved", "outcome": "rolled-back", "batch": RUN, "reason": "Neue Versionen: beets, frage", "restore": 547, "forward": 548,
     "files": ["services/beets/values.yml", "services/frage/values.yml"], "notes": {"beets/IMAGE": "Ein Fehler beim Import ist behoben."},
     "intent": {"set": {"services/beets/values.yml:IMAGE": "docker.io/linuxserver/beets:latest@sha256:ee3c", "services/frage/values.yml:SEARX_IMAGE": "docker.io/searxng/searxng:latest@sha256:048e"}}},
    {"id": 543, "status": "approved", "outcome": "rolled-back", "batch": RUN, "reason": "home-assistant: Erweiterungen installieren: Meross LAN v6.0.1", "restore": 547, "forward": 548,
     "files": ["services/home-assistant/values.yml"], "intent": {"set": {"services/home-assistant/values.yml:HA_INSTALL": "update.meross_lan_update=v6.0.1"}}},
    {"id": 546, "status": "approved", "outcome": "rolled-back", "batch": RUN, "reason": "Homes: Unter Geräte steht beim Raum des Druckers, ob er erreichbar ist.", "restore": 547, "forward": 548,
     "files": ["services/homes/values.yml"], "intent": {"set": {"services/homes/values.yml:HOMES_IMAGE": "ghcr.io/mdopp/homes:latest@sha256:a979"}}},
    {"id": 547, "status": "open", "actor": "laib", "reason": "Das Update von Home Assistant lief nicht."},
    {"id": 548, "status": "open", "actor": "laib", "reason": "Das Update von Home Assistant lief nicht. Statt die Sicherung zurückzuspielen …"},
]
LOGS = {"home-assistant.service": "2026-11-14T09:54:49+00:00 box systemd-home-assistant[7]: 2026-11-14 10:54:49+01:00 ERROR (MainThread) [habluetooth.manager] Missing required permissions for Bluetooth management\n"
                                  "2026-11-14T09:55:19+00:00 box systemd-home-assistant[7]: 2026-11-14 10:55:19+01:00 ERROR (MainThread) [homeassistant.components.api] Error during service call to update.install: Open the Marketplace and read the warning first, installing and updating work once it is accepted\n"
                                  "2026-11-14T09:30:00+00:00 box systemd-home-assistant[7]: 2026-11-14 10:30:00+01:00 ERROR (MainThread) [x] long before\n"}
CAUSE = "Home Assistant hat die Erweiterungen abgelehnt: der Marktplatz-Hinweis ist für Laibs Benutzer nicht bestätigt."
OWNER = "Den Marktplatz-Hinweis in Home Assistant für Laibs Benutzer bestätigen."


class Fake:
    def __init__(self, trigger=None, journal=(RED, BACK), cards=CARDS, answer=f"Ursache: {CAUSE}\nBesitzer: {OWNER}", steps=0, state=None):
        self.trigger = trigger or {"kind": "event", "service": "", "detail": ": apply: home-assistant: post-deploy exit status 1"}
        self.now, self.state, self.skill, self.data = "2026-11-14T09:56:21Z", state, {"granted": ["journal", "inbox", "logs", "propose", "aufgabe", "note"]}, {}
        self.journal, self.cards, self.answer, self.steps = list(journal), [dict(c) for c in cards], answer, steps
        self.calls, self.asked, self.next = [], [], 570

    def call(self, tool, **args):
        self.calls.append((tool, args))
        if tool == "laib_journal":
            # the rollback's line comes only once its steps are done
            return laibskill.Reply(json.dumps(self.journal if self.steps <= 0 else self.journal[:1]))
        if tool == "laib_inbox":
            self.steps -= 1
            return laibskill.Reply(json.dumps([dict(c, step="macht rückgängig: startet beets") if self.steps >= 0 and c["id"] < 547 else c for c in self.cards]))
        if tool == "laib_logs":
            return laibskill.Reply(json.dumps(LOGS))
        if tool == "laib_propose":
            self.next += 1
            return laibskill.Reply(f"#{self.next} {args['reason']}")
        if tool == "laib_aufgabe":
            return laibskill.Reply('{"aufgaben": []}' if args["action"] == "list" else f"Box-Aufgabe #31 angelegt: {args['titel']}")
        return laibskill.Reply("notiert: incident auf den Karten #542, #543, #546, #547, #548")

    def ask(self, prompt):
        self.asked.append(prompt)
        return laibskill.Reply(self.answer, self.answer.startswith("error"))

    def tools(self, name):
        return [a for t, a in self.calls if t == name]


class Durchgang(unittest.TestCase):
    # Ablauf L-50, L-51: a run that went back as a whole (one that never ran alone)
    def test_the_morning_of_2026_11_14(self):
        s = Fake(steps=2)
        r = run.run(s)
        self.assertEqual(r["outcome"], "green")
        # it waited for laib's rollback before it read anything else
        self.assertEqual([t for t, _ in s.calls[:6]], ["laib_journal", "laib_journal", "laib_inbox", "laib_journal", "laib_inbox", "laib_journal"])
        # the model saw the run, its cards, the red line and the error lines of that minute only
        self.assertIn("Error during service call to update.install: Open the Marketplace", s.asked[0])
        self.assertIn("Missing required permissions for Bluetooth", s.asked[0])
        self.assertNotIn("long before", s.asked[0])
        self.assertIn("- #543: home-assistant: Erweiterungen installieren", s.asked[0])
        # the innocent ones again, each on its own, as they were; the guilty one not
        props = s.tools("laib_propose")
        self.assertEqual([p["sets"] for p in props], [["beets/IMAGE=docker.io/linuxserver/beets:latest@sha256:ee3c", "frage/SEARX_IMAGE=docker.io/searxng/searxng:latest@sha256:048e"],
                                                      ["homes/HOMES_IMAGE=ghcr.io/mdopp/homes:latest@sha256:a979"]])
        self.assertEqual(props[0]["notes"], ["beets/IMAGE=Ein Fehler beim Import ist behoben."])
        self.assertTrue(props[1]["reason"].startswith("Homes: Unter Geräte steht") and "mit dem roten Durchgang von #543 zurückgenommen" in props[1]["reason"])
        self.assertFalse(any("approve" in t or "rollback" in t for t, _ in s.calls))
        # what only the owner can do is one Box-Aufgabe
        self.assertEqual(s.tools("laib_aufgabe")[1], {"action": "add", "titel": OWNER, "warum": CAUSE})
        # the cause onto the run's cards and their follow-ups, in one note
        note = s.tools("laib_note")[0]
        self.assertEqual(note["karten"], "542,543,546,547,548")
        self.assertEqual(note["kind"], "incident")
        self.assertTrue(note["text"].startswith("Durchgang #542, #543, #546 um 09:55 rot: Home Assistant hat die Erweiterungen abgelehnt"), note["text"])
        self.assertIn("Unbeteiligt und einzeln neu vorgeschlagen: #542 als #571; #546 als #572.", note["text"])
        self.assertIn(f"Nur du kannst das: {OWNER} (die Box-Aufgabe #31 ist für dich).", note["text"])
        self.assertEqual(r["state"], {"done": [RUN]})

    # Ablauf L-50, L-51
    def test_since_1209_the_green_ones_stay_and_laibs_own_task_is_not_filed_twice(self):
        # #1209: the run went back, each approval was applied again alone; #543 waits for the
        # Box-Aufgabe its hook named, as a fresh card laib filed (#560)
        alone = [RED, {"t": "2026-11-14T09:56:30Z", "verb": "apply", "result": "green", "reason": f"Freigabe #542 allein, aus dem Lauf {RUN}"},
                 {"t": "2026-11-14T09:57:10Z", "verb": "apply", "result": "red", "reason": f"Freigabe #543 allein, aus dem Lauf {RUN}"},
                 {"t": "2026-11-14T09:57:40Z", "verb": "apply", "result": "green", "reason": "rollback #543"},
                 {"t": "2026-11-14T09:58:20Z", "verb": "apply", "result": "green", "reason": f"Freigabe #546 allein, aus dem Lauf {RUN}"},
                 {"t": "2026-11-14T09:58:21Z", "verb": "rollback", "result": "info", "actor": "laib",
                  "detail": f"Lauf {RUN} war rot; jede Freigabe allein angewendet: angewendet #542, #546 zurückgenommen #543"}]
        cards = [dict(CARDS[0], outcome="", restore=None, forward=None), dict(CARDS[1], refiled=560, forward=None,
                 because="wartet auf Box-Aufgabe #31 „Marktplatz-Hinweis bestätigen“; ist sie erledigt, wendet Laib die Änderung von selbst an (Vorschlag #560)"),
                 dict(CARDS[2], outcome="", restore=None, forward=None), CARDS[3]]
        s = Fake(journal=alone, cards=cards)
        self.assertEqual(run.run(s)["outcome"], "green")
        self.assertIn("- #542: Neue Versionen: beets, frage (Dienste: beets, frage; allein angewendet)", s.asked[0])
        self.assertIn("(Dienste: home-assistant; zurückgenommen)", s.asked[0])
        self.assertEqual((s.tools("laib_propose"), s.tools("laib_aufgabe")), ([], []))
        note = s.tools("laib_note")[0]
        self.assertEqual(note["karten"], "543,547")
        self.assertIn("Laib hat jede Freigabe allein angewendet; angewendet ist #542, #546.", note["text"])
        self.assertIn("Laib hat die Box-Aufgabe dazu schon angelegt, die Karte wartet darauf", note["text"])

    def test_once_per_run_and_never_for_another_red(self):
        s = Fake(state={"done": [RUN]})
        self.assertEqual((run.run(s)["outcome"], s.tools("laib_note")), ("idle", []))
        s = Fake(trigger={"kind": "event", "service": "bote", "detail": "bote: unit bote.service ist failed"})
        self.assertEqual((run.run(s)["why"], s.calls), ("kein roter Durchgang", []))
        s = Fake(trigger={"kind": "manual"})  # laib-operator -once durchgang: the newest run
        self.assertEqual(run.run(s)["outcome"], "green")

    def test_a_proposal_already_filed_again_is_not_filed_twice(self):
        again = dict(CARDS[2], id=549, status="approved", outcome="applied", batch="#549 (2dee012..7ba47d5)")
        s = Fake(cards=CARDS + [again])
        run.run(s)
        self.assertEqual(len(s.tools("laib_propose")), 1)
        self.assertIn("#546 ist schon neu vorgeschlagen", s.tools("laib_note")[0]["text"])

    def test_without_the_model_or_a_green_rollback_it_proposes_nothing(self):
        s = Fake(answer="error: die Grafikkarte ist vergeben", journal=(RED, dict(BACK, result="red")))
        run.run(s)
        text = s.tools("laib_note")[0]["text"]
        self.assertIn("home-assistant lief nach dem Einrichten nicht richtig", text)
        self.assertIn("Die Rücknahme ist nicht grün im Journal", text)
        self.assertEqual((s.tools("laib_propose"), s.tools("laib_aufgabe")), ([], []))

    def test_an_edit_it_cannot_redo_is_named_not_guessed(self):
        tree = dict(CARDS[2], intent={"at": {"box.yml:hostNetwork": "[druck]"}}, files=["box.yml"])
        s = Fake(cards=[CARDS[0], CARDS[1], tree] + CARDS[3:], answer=f"Ursache: {CAUSE}\nBesitzer: nein")
        run.run(s)
        text = s.tools("laib_note")[0]["text"]
        self.assertIn("#546 lässt sich nicht von selbst neu vorschlagen", text)
        self.assertEqual((len(s.tools("laib_propose")), s.tools("laib_aufgabe")), (1, []))


if __name__ == "__main__":
    unittest.main()
