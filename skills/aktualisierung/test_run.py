"""aktualisierung's script (#376, #528, #368, #839): only the patches, exactly.
The runner-level tests are operator/bounds376_test.go and aktualisierung528_test.go."""

import importlib.util
import json
import os
import sys
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(HERE))
import laibskill  # noqa: E402

spec = importlib.util.spec_from_file_location("aktualisierung", os.path.join(HERE, "run.py"))
run = importlib.util.module_from_spec(spec)
spec.loader.exec_module(run)


class Fake:
    def __init__(self, replies):
        self.replies, self.calls = replies, []

    def call(self, tool, **args):
        self.calls.append((tool, args))
        r = self.replies.get(tool, "ok")
        return laibskill.Reply(r, r.startswith("error"))


class Aktualisierung(unittest.TestCase):
    def test_offers_and_the_check(self):
        ups = [{"Service": "whoami", "Key": "IMAGE", "Image": "docker.io/traefik/whoami", "Current": "v1.10.1", "Patch": "v1.10.3", "Minor": "v1.11.2"},
               {"Service": "laib", "Key": "laib", "Patch": "0.47.0"}, {"Service": "nas", "Key": "IMAGE", "Error": "timeout"}]
        s = Fake({"laib_updates": json.dumps(ups), "laib_inbox": "[]", "laib_diag": "[]"})
        r = run.run(s)
        self.assertEqual(r["checks"], ["laib_propose"])
        self.assertIn("- service whoami, key IMAGE, value docker.io/traefik/whoami:v1.10.3 (jetzt v1.10.1)", r["context"])
        self.assertIn("whoami: Minor-Version v1.11.2 (jetzt v1.10.1) für einen Menschen.", r["human"])
        self.assertIn("nas: Registry nicht lesbar (timeout).", r["human"])
        self.assertNotIn("service laib", r["context"])  # laib's own update is self-update.sh's
        self.assertEqual(run.check(s, "laib_propose", {"service": "whoami", "key": "IMAGE", "value": "docker.io/traefik/whoami:v1.10.3"}), "")
        for bad in [{"service": "whoami", "key": "IMAGE", "value": "docker.io/traefik/whoami:v1.11.2"}, {"service": "whoami", "key": "IMAGE"}, {}]:
            self.assertIn("nur die Patch-Versionen", run.check(s, "laib_propose", bad))

    def test_nothing_to_propose_is_idle(self):
        s = Fake({"laib_updates": "error: registry", "laib_inbox": "[]"})
        r = run.run(s)
        self.assertEqual(r["outcome"], "idle")
        self.assertEqual(r["note"], "Keine Patch-Versionen zum Vorschlagen. Aktualisierungen nicht lesbar: error: registry.")
        self.assertEqual(s.calls[-1][0], "laib_note")


if __name__ == "__main__":
    unittest.main()
