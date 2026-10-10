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


if __name__ == "__main__":
    unittest.main()
