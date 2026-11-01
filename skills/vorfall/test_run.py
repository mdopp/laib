"""vorfall's first aid (#792, #839). The runner-level tests are operator/vorfall792_test.go."""

import importlib.util
import json
import os
import sys
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(HERE))
import laibskill  # noqa: E402

spec = importlib.util.spec_from_file_location("vorfall", os.path.join(HERE, "run.py"))
run = importlib.util.module_from_spec(spec)
spec.loader.exec_module(run)

RED = '[{"severity":"rot","probe":"unit","service":"bote","detail":"bote-check.service ist failed"}]'


class Fake:
    def __init__(self, trigger, diag_after=RED, granted=("diag", "logs", "restart", "note")):
        self.trigger, self.skill, self.now = trigger, {"granted": list(granted)}, "2026-10-27T10:31:00+01:00"
        self.calls, self.diag, self.data = [], diag_after, {}

    def call(self, tool, **args):
        self.calls.append((tool, args))
        return laibskill.Reply({"laib_logs": "a\nError: exit status 125\n", "laib_restart": "grün: bote", "laib_diag": self.diag}.get(tool, "ok"))


OPENED = {"kind": "event", "service": "bote", "detail": "bote: unit bote-check.service ist failed", "restart": ["bote"], "opened": True}


class FirstAid(unittest.TestCase):
    def test_a_kernel_red_goes_to_the_model_as_it_came(self):
        for tr in ({"kind": "event", "service": "bote", "detail": "x"}, dict(OPENED, aided=True)):
            s = Fake(tr)
            self.assertEqual((run.run(s)["trigger"], s.calls), (None, []))

    def test_still_red_hands_the_aided_trigger_to_the_model(self):
        s = Fake(dict(OPENED))
        r = run.run(s)
        self.assertEqual([t for t, _ in s.calls], ["laib_logs", "laib_restart", "laib_diag", "laib_note"])
        self.assertEqual(r["outcome"], "")
        self.assertEqual((r["trigger"]["aided"], r["trigger"]["saw"], r["trigger"]["done"]), (True, "bote: „Error: exit status 125“", "bote neu gestartet (grün: bote)"))
        note = s.calls[-1][1]["text"]
        self.assertTrue(note.startswith("Vorfall 10:31: bote: unit bote-check.service ist failed. Im Log: bote: „Error: exit status 125“."), note)
        self.assertIn("ist es um 11:01 noch rot, eskaliere ich", note)

    def test_the_boxs_json_gives_the_last_real_error_and_the_restarts_result(self):
        # #978: on the box laib logs answers {unit: lines} and laib restart {checks, green}; the
        # note of 2026-11-01 04:09 read „Im Log: home-assistant: „}“ … neu gestartet ({)“
        logs = {"home-assistant.service": "2026-11-01T03:09:20+00:00 atHome-Server home-assistant[7]: 2026-11-01 04:09:20 ERROR (MainThread) [homeassistant.setup] Setup failed for zwave_js\n"
                                          "2026-11-01T03:09:30+00:00 atHome-Server home-assistant[7]: 2026-11-01 04:09:30 INFO (MainThread) [homeassistant.core] Starting",
                "home-assistant-check.service": "2026-11-01T03:09:25+00:00 atHome-Server pasta[9]: Flow 0: ERROR on epoll_ctl()"}
        s = Fake(dict(OPENED, service="home-assistant", restart=["home-assistant"]), diag_after="[]")
        s.call = lambda tool, **a: s.calls.append((tool, a)) or laibskill.Reply({"laib_logs": json.dumps(logs), "laib_restart": '{"checks": [], "green": true}', "laib_diag": "[]"}.get(tool, "ok"))
        r = run.run(s)
        self.assertIn("Im Log: home-assistant: „2026-11-01 04:09:20 ERROR (MainThread) [homeassistant.setup] Setup failed for zwave_js“. Schon getan: home-assistant neu gestartet (grün).", r["note"])

    def test_green_after_the_restart_ends_without_a_model(self):
        s = Fake(dict(OPENED), diag_after="[]")
        r = run.run(s)
        self.assertEqual(r["outcome"], "green")
        self.assertIn("Danach ist nichts davon mehr rot.", r["note"])

    def test_the_lesson_for_the_finding_is_named(self):  # #861
        s = Fake(dict(OPENED, finding=["unit bote"]), diag_after="[]")
        s.data = {"lessons": ["# Lehre: unit bote\n\n- Ursache: x\n- Lösung: Den Pod neu starten, nicht den Check.", "# Lehre: unit caddy\n\n- Lösung: y"]}
        r = run.run(s)
        self.assertIn("Lehre zu unit bote: Lösung: Den Pod neu starten, nicht den Check.", r["note"])
        self.assertNotIn("caddy", r["note"])
        self.assertEqual(r["lessons"], ["unit bote"])

    def test_unreadable_diag_or_no_restart_is_not_green(self):
        self.assertEqual(run.run(Fake(dict(OPENED), diag_after="laib away"))["outcome"], "")
        s = Fake(dict(OPENED), granted=("diag", "logs", "note"))
        r = run.run(s)
        self.assertEqual((r["outcome"], r["trigger"]["done"], [t for t, _ in s.calls]), ("", "Logs gelesen", ["laib_logs", "laib_note"]))


if __name__ == "__main__":
    unittest.main()
