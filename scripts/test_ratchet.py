"""scripts/ratchet.py: a new finding is red, a fixed one asks for a lower
baseline, --update lowers only, --accept writes a new one."""
import importlib.util
import io
import tempfile
import unittest
from collections import Counter
from contextlib import redirect_stdout
from pathlib import Path

spec = importlib.util.spec_from_file_location("ratchet", Path(__file__).parent / "ratchet.py")
ratchet = importlib.util.module_from_spec(spec)
spec.loader.exec_module(ratchet)


class Ratchet(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        ratchet.BASELINE = Path(self.dir.name)
        self.now = Counter()
        ratchet.TOOLS = {"t": lambda: Counter(self.now)}

    def tearDown(self):
        self.dir.cleanup()

    def main(self, *args):
        out = io.StringIO()
        with redirect_stdout(out):
            code = ratchet.main(list(args))
        return code, out.getvalue()

    def test_same_findings_are_green(self):
        self.now.update({"a.go: f": 1, "b.go: g": 40})
        self.assertEqual(self.main("--update", "--accept")[0], 0)
        self.assertEqual(self.main(), (0, "t: 2 findings (baseline 2)\n"))

    def test_a_new_finding_is_red(self):
        self.now["a.go: f"] = 1
        self.main("--update", "--accept")
        self.now["c.go: h"] = 1
        code, out = self.main()
        self.assertEqual(code, 1)
        self.assertIn("new or higher: 1 c.go: h (baseline 0)", out)

    def test_a_higher_number_is_red(self):
        self.now["a.go: f"] = 35
        self.main("--update", "--accept")
        self.now["a.go: f"] = 36
        self.assertEqual(self.main()[0], 1)

    def test_a_fixed_finding_asks_for_a_lower_baseline(self):
        self.now.update({"a.go: f": 1, "b.go: g": 1})
        self.main("--update", "--accept")
        del self.now["b.go: g"]
        code, out = self.main()
        self.assertEqual(code, 1)
        self.assertIn("lower the baseline in this PR", out)
        self.assertEqual(self.main("--update")[0], 0)
        self.assertEqual(self.main()[0], 0)

    def test_update_lowers_only(self):
        self.now.update({"a.go: f": 40, "b.go: g": 1})
        self.main("--update", "--accept")
        self.now.update({"a.go: f": 2, "c.go: new": 1})  # a.go goes up to 42
        del self.now["b.go: g"]
        code, out = self.main("--update")
        self.assertEqual(code, 1)
        self.assertIn("not written: 1 c.go: new", out)
        self.assertEqual(ratchet.read("t"), Counter({"a.go: f": 40}))

    def test_a_line_the_ratchet_cannot_read_is_an_error(self):
        with self.assertRaises(SystemExit):
            ratchet.lines_of("internal/x.go:1:1: something new\n", ratchet.DEAD, "deadcode")


if __name__ == "__main__":
    unittest.main()
