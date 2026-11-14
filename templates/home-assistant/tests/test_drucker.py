"""The household's printers in Home Assistant (owner 2026-11-14): every printer the print server
lists gets one ipp entry, a waiting discovery is confirmed rather than doubled, an entry there or
ignored by a human is left alone, and nothing is ever deleted."""
import importlib.util
import unittest
from pathlib import Path

spec = importlib.util.spec_from_file_location("drucker", Path(__file__).resolve().parent.parent / "home-assistant-drucker.py")
drucker = importlib.util.module_from_spec(spec)
spec.loader.exec_module(drucker)

# druck's ha-drucker.json: the Epson by its own name, a Brother at the USB through the box's queue
LISTE = [
    {"name": "Epson WF-3540", "host": "EPSON0A36A8.local", "port": 631, "pfad": "/Epson_IPP_Printer", "tls": False,
     "uuid": "cfe92100-67c4-11d4-a45f-ac18260a36a8"},
    {"name": "HP OfficeJet Pro 9010", "host": "HP4B2A1C.local", "port": 631, "pfad": "/ipp/print", "tls": False,
     "uuid": "1c852a4d-b800-1f08-abcd-a0481c4b2a1c"},
    {"name": "Brother HL-1110 (über die Box)", "host": "127.0.0.1", "port": 631, "pfad": "/printers/Brother_HL-1110_USB",
     "tls": False, "uuid": "0f3c6a59-0000-3000-8000-0000000000aa"},
]
# HA's .storage/core.config_entries today: the Epson, set up by hand from its discovery, by address
STORAGE = {"data": {"entries": [
    {"domain": "ipp", "source": "zeroconf", "unique_id": "cfe92100-67c4-11d4-a45f-ac18260a36a8",
     "data": {"host": "192.168.178.87", "port": 631, "base_path": "/Epson_IPP_Printer", "uuid": "cfe92100-67c4-11d4-a45f-ac18260a36a8"}},
    {"domain": "mqtt", "source": "user", "unique_id": None, "data": {"broker": "127.0.0.1"}},
]}}
# the flows waiting: the OfficeJet's discovery, and the box's own re-share of the Epson
FLOWS = [
    {"flow_id": "f1", "handler": "ipp", "step_id": "zeroconf_confirm",
     "context": {"source": "zeroconf", "unique_id": "1c852a4d-b800-1f08-abcd-a0481c4b2a1c", "title_placeholders": {"name": "HP OfficeJet Pro 9010"}}},
    {"flow_id": "f2", "handler": "ipp", "step_id": "zeroconf_confirm",
     "context": {"source": "zeroconf", "unique_id": "0f3c6a59-0000-3000-8000-000000000001", "title_placeholders": {"name": "Epson WF-3540 (über die Box) @ drucker"}}},
]


class Plan(unittest.TestCase):
    def setUp(self):
        self.todo = drucker.plan(LISTE, drucker.entries_of(STORAGE), FLOWS)
        self.by = {p["name"]: (a, f) for a, p, f in self.todo}

    def test_the_epson_there_by_its_address_is_not_added_again(self):
        self.assertNotIn("Epson WF-3540", self.by, "the same uuid: the entry by address is the Epson's")

    def test_a_waiting_discovery_is_confirmed_not_doubled(self):
        self.assertEqual(self.by["HP OfficeJet Pro 9010"], ("bestaetigen", "f1"))

    def test_a_usb_printer_goes_in_through_the_box(self):
        self.assertEqual(self.by["Brother HL-1110 (über die Box)"], ("anlegen", None))

    def test_the_box_s_re_share_is_never_confirmed(self):
        self.assertNotIn("f2", [f for _, _, f in self.todo])
        self.assertEqual(len(self.todo), 2)

    def test_one_a_human_ignored_stays_ignored(self):
        st = {"data": {"entries": [{"domain": "ipp", "source": "ignore", "unique_id": "1C852A4D-B800-1F08-ABCD-A0481C4B2A1C", "data": {}}]}}
        self.assertNotIn("HP OfficeJet Pro 9010", {p["name"] for _, p, _ in drucker.plan(LISTE, drucker.entries_of(st), FLOWS)})

    def test_without_a_uuid_the_host_and_path_decide(self):
        p = dict(LISTE[2], uuid="")
        st = {"data": {"entries": [{"domain": "ipp", "source": "user", "unique_id": "x", "data": {"host": "127.0.0.1", "base_path": "/printers/Brother_HL-1110_USB"}}]}}
        self.assertEqual(drucker.plan([p], drucker.entries_of(st), []), [])


class Run(unittest.TestCase):
    def test_a_printer_that_does_not_answer_leaves_no_flow_and_nothing_is_deleted(self):
        calls = []

        def ha(method, path, body=None):
            calls.append((method, path))
            if path.endswith("/flow") and method == "GET":
                return []
            if path.endswith("/flow") and method == "POST":
                return {"flow_id": "n1", "type": "form", "step_id": "user"}
            if method == "POST":
                return {"type": "form", "errors": {"base": "cannot_connect"}}
            return None

        self.assertEqual(drucker.main([LISTE[2]], "t", ha, {}), 0)
        self.assertIn(("DELETE", "/api/config/config_entries/flow/n1"), calls)
        self.assertFalse([c for c in calls if "/entry" in c[1]], "never an entry deleted or changed")

    def test_no_print_server_is_no_change(self):
        self.assertEqual(drucker.main(None, "t", None, {}), 0)


if __name__ == "__main__":
    unittest.main()
