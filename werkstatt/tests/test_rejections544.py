"""#544: a real rejection becomes a draft eval case — the Operator's own
anchored proposal, rejected with a reason; "nicht jetzt" stays free and a
repair the runner filed is code, not the model."""
import importlib.util, json, unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location("rejections", HERE / "evals" / "rejections.py")
rejections = importlib.util.module_from_spec(spec)
spec.loader.exec_module(rejections)

ops = {"actor": "ops", "status": "rejected", "outcome": "rejected", "files": ["services/whoami/values.yml"],
       "finding": "unit whoami", "evidence": "03:12 manifest unknown", "reason": "IMAGE auf v1.10"}


class Rejections(unittest.TestCase):
    def test_a_reasoned_no_becomes_a_case(self):
        out = rejections.drafts([dict(ops, id=12, rejectReason="v1.10 hat die Lücke, die wir loswerden wollten")])
        self.assertEqual(len(out), 1)
        case = out[0]
        self.assertEqual(case["id"], "abgelehnt-12")
        self.assertEqual(case["skill"], "vorfall")
        self.assertEqual(case["expect"]["calls_lack"], ['"finding":"unit whoami"'])
        self.assertIn("laib_propose", case["expect"]["allowed"])
        self.assertIn("laib_apply", case["expect"]["forbidden"])
        # the record the Operator reads carries the reason, so the model can know it
        self.assertIn("Lücke", case["replies"]["laib_inbox"])
        self.assertEqual(json.loads(case["replies"]["laib_diag"])[0]["probe"], "unit")
        json.dumps(out)  # the draft is JSON as incidents.json holds it

    def test_free_nos_others_and_repairs_are_not_cases(self):
        out = rejections.drafts([
            dict(ops, id=1, rejectReason="nicht jetzt"),
            dict(ops, id=2, rejectReason=""),
            dict(ops, id=3, rejectReason="nein", actor="claude"),
            dict(ops, id=4, rejectReason="nein", files=["repairs/x.md"]),
            dict(ops, id=5, rejectReason="nein", outcome="later"),
            dict(ops, id=6, rejectReason="nein", finding=""),
        ])
        self.assertEqual(out, [])


if __name__ == "__main__":
    unittest.main()
