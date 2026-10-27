"""#785: radicale-check.py writes Radicale's detail line for the Box page: how
many calendars, lists and address books the household has and how many
entries are in them, read from Radicale's own storage (read-only, no login).
Home Assistant's copies (kalender.py's homeassistant/ home) are not counted
twice. A storage that is not there is "nicht gemessen", never a number."""
import json, os, subprocess, sys, tempfile, unittest
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent.parent / "radicale-check.py"


def coll(root, path, props, items=0, ext=".ics"):
    d = os.path.join(root, "collections", "collection-root", path)
    os.makedirs(os.path.join(d, ".Radicale.cache"), exist_ok=True)
    with open(os.path.join(d, ".Radicale.props"), "w") as f:
        json.dump(props, f)
    for i in range(items):
        open(os.path.join(d, f"item-{i}{ext}"), "w").write("BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n")


def store():
    root = tempfile.mkdtemp()
    os.makedirs(os.path.join(root, "collections", "collection-root", "mdopp"))   # a home without collections
    coll(root, "haus/haushalt", {"tag": "VCALENDAR", "C:supported-calendar-component-set": "VEVENT,VTODO", "D:displayname": "Haushalt"}, 1200)
    coll(root, "haus/abo-muellabfuhr", {"tag": "VCALENDAR", "D:displayname": "Müllabfuhr"}, 20)
    coll(root, "haus/einkauf", {"tag": "VCALENDAR", "C:supported-calendar-component-set": "VTODO"}, 3)
    coll(root, "haus/liste-musik", {"tag": "VCALENDAR", "C:supported-calendar-component-set": "VTODO"}, 0)
    coll(root, "haus/haushalt-kontakte", {"tag": "VADDRESSBOOK", "D:displayname": "Haushalt"}, 40, ".vcf")
    coll(root, "homeassistant/haus-haushalt", {"tag": "VCALENDAR", "{urn:laib:kalender}abgleich": "x"}, 1200)
    os.makedirs(os.path.join(root, "collections", "collection-root", "haus", "kein-props"))   # not a collection
    return root


def run(data):
    d = tempfile.mkdtemp()
    env = {"PATH": os.environ["PATH"], "RADICALE_DATA": data, "STATUS_DIR": d}
    r = subprocess.run([sys.executable, "-"], input=SCRIPT.read_text(), text=True, capture_output=True, env=env, timeout=60)
    with open(os.path.join(d, "laib-status.json")) as f:
        return r, json.load(f)


class Check(unittest.TestCase):
    def test_counts(self):
        r, st = run(store())
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(st["detail"], "2 Kalender, 2 Listen, 1 Adressbuch · 1.263 Einträge")
        self.assertEqual(st["findings"], [])
        self.assertRegex(st["at"], r"^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$")

    def test_empty_storage(self):
        root = tempfile.mkdtemp()
        os.makedirs(os.path.join(root, "collections", "collection-root"))
        _, st = run(root)
        self.assertEqual(st["detail"], "noch kein Kalender und kein Adressbuch")

    def test_missing_storage_is_not_measured(self):
        _, st = run("/nonexistent/radicale")
        self.assertTrue(st["detail"].startswith("nicht gemessen: Radicales Ablage fehlt"), st["detail"])
        self.assertTrue(st["findings"][0]["unmeasured"])


if __name__ == "__main__":
    unittest.main()
