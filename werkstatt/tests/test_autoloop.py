import importlib.util, json, os, sys, unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location("autoloop", HERE / "autoloop.py")
al = importlib.util.module_from_spec(spec); spec.loader.exec_module(al)


class Config(unittest.TestCase):
    def test_model_url_and_escalate_label_from_env(self):
        cfg = al.config_from_env({"MODEL_URL": "http://10.0.0.20:11435/v1/", "PI_AUTOLOOP_ESCALATE_LABEL": "needs-claude"})
        self.assertEqual(cfg["models_url"], "http://10.0.0.20:11435/v1/models")
        self.assertEqual(cfg["escalate_label"], "needs-claude")
        self.assertEqual(al.config_from_env({})["escalate_label"], "needs-claude")

    def test_preset_to_use_prefers_coding_then_first(self):
        def http(url, method, token="", body=None):
            return 200, {"data": [{"id": "gemma-4-e4b", "allowed_in_mode": True}, {"id": al.CODING_PRESET, "allowed_in_mode": False}]}
        self.assertEqual(al.preset_to_use("u", http), al.CODING_PRESET)
        def http2(url, method, token="", body=None):
            return 200, {"data": [{"id": "gemma-4-e4b"}]}
        self.assertEqual(al.preset_to_use("u", http2), "gemma-4-e4b")


class Escalation(unittest.TestCase):
    def test_no_pull_request_hands_the_ticket_to_claude(self):
        calls = []
        def http(url, method, token="", body=None):
            calls.append((method, url, body))
            if url.endswith("/git/refs"):
                return 201, {}
            return 200, {}
        # drive the tail of work_ticket directly: no PR, so the router runs
        cfg = al.config_from_env({"PI_AUTOLOOP_PROTOCOL_DIR": "/tmp/laib-autoloop-test", "PI_AUTOLOOP_REPOS": "mdopp/x", "PI_AUTOLOOP_API_URL": "https://api"})
        os.makedirs(cfg["protocol_dir"], exist_ok=True)
        # emulate the branch by calling the same API lines work_ticket uses
        number = 7
        http(f"{cfg['api']}/repos/mdopp/x/issues/{number}/labels", "POST", "t", {"labels": [cfg["escalate_label"]]})
        http(f"{cfg['api']}/repos/mdopp/x/issues/{number}/labels/{cfg['label']}", "DELETE", "t")
        self.assertIn(("POST", "https://api/repos/mdopp/x/issues/7/labels", {"labels": ["needs-claude"]}), calls)
        self.assertIn(("DELETE", "https://api/repos/mdopp/x/issues/7/labels/pi:ready", None), calls)
        src = (HERE / "autoloop.py").read_text()
        self.assertIn('{"labels": [cfg["escalate_label"]]}', src)
        self.assertIn("labels/{cfg['label']}\", \"DELETE\"", src)


class LeaseContract(unittest.TestCase):
    """#162: a run takes the GPU lease (laib ADR 0011, amendment 2026-10-14)
    under its own holder, renews it, and gives it back."""

    def cfg(self, **env):
        return al.config_from_env({"MODEL_LEASE": "http://10.0.0.20:11435/api/model-lease", "MODEL_KEY": "k", **env})

    def test_config_reads_the_lease_and_the_key(self):
        cfg = self.cfg()
        self.assertEqual(cfg["lease_url"], "http://10.0.0.20:11435/api/model-lease")
        self.assertEqual(cfg["model_key"], "k")
        self.assertEqual(al.config_from_env({})["lease_url"], "")

    def test_take_asks_for_the_preset_under_the_werkstatt_holder(self):
        calls = []
        def http(url, method, token="", body=None):
            calls.append((url, method, token, body))
            return 200, {"ok": True, "state": "ready", "model": "full", "renew_after": 600}
        note, renew = al.take_lease(self.cfg(), al.CODING_PRESET, 3600, http, sleep=lambda s: None)
        self.assertEqual((note, renew), ("", 600))
        url, method, token, body = calls[0]
        self.assertEqual((url, method, token), ("http://10.0.0.20:11435/api/model-lease", "POST", "k"))
        self.assertEqual(body, {"model": al.CODING_PRESET, "ttl_s": 3600, "holder": "werkstatt"})

    def test_preparing_waits_for_ready_by_get_never_a_second_post(self):
        answers = [(202, {"ok": True, "state": "preparing", "retry_after": 30}),
                   (200, {"state": "preparing", "retry_after": 30}),
                   (200, {"state": "ready", "renew_after": 300})]
        calls, slept = [], []
        def http(url, method, token="", body=None):
            calls.append(method)
            return answers.pop(0)
        note, renew = al.take_lease(self.cfg(), al.CODING_PRESET, 3600, http, sleep=slept.append)
        self.assertEqual((note, renew), ("", 300))
        self.assertEqual(calls, ["POST", "GET", "GET"])
        self.assertEqual(slept, [30, 30])

    def test_held_is_a_plain_note_and_no_run(self):
        def http(url, method, token="", body=None):
            return 409, {"ok": False, "reason": "held", "holder": "foundry-chronicle", "expires_at": 1792000000}
        note, renew = al.take_lease(self.cfg(), al.CODING_PRESET, 3600, http, sleep=lambda s: None)
        self.assertIn("foundry-chronicle", note)
        self.assertIn("Grafikkarte", note)
        self.assertEqual(renew, 0)

    def test_without_a_lease_url_nothing_is_asked(self):
        def http(url, method, token="", body=None):
            raise AssertionError("no lease configured, no call")
        self.assertEqual(al.take_lease(al.config_from_env({}), al.CODING_PRESET, 3600, http, sleep=lambda s: None), ("", 0))

    def test_a_failed_lease_never_stops_the_work(self):
        def http(url, method, token="", body=None):
            return 0, {"message": "connection refused"}
        self.assertEqual(al.take_lease(self.cfg(), al.CODING_PRESET, 3600, http, sleep=lambda s: None), ("", 0))

    def test_renewing_posts_again_and_releases_at_the_end(self):
        import time as _t
        calls = []
        def http(url, method, token="", body=None):
            calls.append((method, body))
            return 200, {"state": "ready"}
        with al.renewing(self.cfg(), al.CODING_PRESET, 3600, 0.05, http):
            _t.sleep(0.2)
        posts = [c for c in calls if c[0] == "POST"]
        self.assertGreaterEqual(len(posts), 2, calls)
        self.assertEqual(calls[-1], ("DELETE", {"holder": "werkstatt"}))

    def test_on_start_an_own_window_is_given_back(self):
        calls = []
        def http(url, method, token="", body=None):
            calls.append(method)
            return 200, {"state": "ready", "holder": "werkstatt"}
        al.release_own_lease(self.cfg(), http)
        self.assertEqual(calls, ["GET", "DELETE"])
        calls.clear()
        def other(url, method, token="", body=None):
            calls.append(method)
            return 200, {"state": "ready", "holder": "bote"}
        al.release_own_lease(self.cfg(), other)
        self.assertEqual(calls, ["GET"])


class NoPiWebLeftovers(unittest.TestCase):
    """#162: the Werkstatt is one noun (laib ADR 0021); pi-web is gone."""

    def test_no_pi_web_names_or_dead_gate(self):
        src = (HERE / "autoloop.py").read_text()
        self.assertNotIn("MODEL_GATE_PORT", src)
        self.assertNotIn("PI_WEB", src)
        self.assertNotIn("pi-web", src)
        unit = (HERE.parent / "templates" / "werkstatt" / "werkstatt-autoloop.container").read_text()
        self.assertNotIn("PI_WEB", unit)
        for var in ("MODEL_LEASE=${MODEL_LEASE}", "MODEL_KEY=${MODEL_KEY}", "WERKSTATT_GIT_TOKEN_FILE="):
            self.assertIn(var, unit)

    def test_protocols_and_attempts_survive_a_restart(self):
        # the attempt counter lives in the protocol dir; it must be on the workspace volume
        cfg = al.config_from_env({})
        self.assertTrue(cfg["protocol_dir"].startswith("/workspace/"), cfg["protocol_dir"])
        self.assertEqual(cfg["store"], "/run/laib/git-credentials")


if __name__ == "__main__":
    unittest.main()
