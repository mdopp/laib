"""#785: haus-check.py writes Haus's detail line for the Box page: how many
devices are signed in to the Haus app, for how many people, and when one was
last active, read from Haus's devices.json (read-only; the token hashes are
never written anywhere). No file yet is "noch kein Gerät angemeldet"; a file
that cannot be read is "nicht gemessen", never a number."""
import datetime, json, os, subprocess, sys, tempfile, unittest
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent.parent / "haus-check.py"
HASH = "d0950804ca3d0a897d5c1663ff6a00585889624096bbc751563cd25f624175e3"


def ago(**kw):
    return (datetime.datetime.now(datetime.timezone.utc) - datetime.timedelta(**kw)).strftime("%Y-%m-%dT%H:%M:%S.123456789Z")


def run(devices):
    data, d = tempfile.mkdtemp(), tempfile.mkdtemp()
    if devices is not None:
        with open(os.path.join(data, "devices.json"), "w") as f:
            f.write(devices if isinstance(devices, str) else json.dumps(devices))
    env = {"PATH": os.environ["PATH"], "HAUS_DATA": data, "STATUS_DIR": d}
    r = subprocess.run([sys.executable, "-"], input=SCRIPT.read_text(), text=True, capture_output=True, env=env, timeout=60)
    with open(os.path.join(d, "laib-status.json")) as f:
        return r, json.load(f)


def dev(uid, name, seen):
    return {"id": name.lower(), "name": name, "uid": uid, "hash": HASH, "created": ago(days=9), "lastSeen": seen}


class Check(unittest.TestCase):
    def test_devices_people_last_active(self):
        r, st = run([dev("mdopp", "Fold8", ago(minutes=3, seconds=10)), dev("lea", "Pixel", ago(days=2)), dev("mdopp", "Tablet", ago(hours=5))])
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(st["detail"], "3 Geräte angemeldet, von 2 Personen · zuletzt aktiv vor 3 min")
        self.assertEqual(st["findings"], [])
        self.assertNotIn(HASH, r.stdout + r.stderr + json.dumps(st))
        self.assertRegex(st["at"], r"^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$")

    def test_one_device(self):
        _, st = run([dev("mdopp", "Fold8", ago(hours=2, minutes=5))])
        self.assertEqual(st["detail"], "1 Gerät angemeldet, von 1 Person · zuletzt aktiv vor 2 h")

    def test_never_seen(self):
        _, st = run([dev("mdopp", "Fold8", "")])
        self.assertEqual(st["detail"], "1 Gerät angemeldet, von 1 Person")

    def test_no_file_yet(self):
        _, st = run(None)
        self.assertEqual(st["detail"], "noch kein Gerät angemeldet")

    def test_unreadable_is_not_measured(self):
        _, st = run("{kaputt")
        self.assertTrue(st["detail"].startswith("nicht gemessen: Haus' Geräteliste ist nicht lesbar"), st["detail"])
        self.assertTrue(st["findings"][0]["unmeasured"])


if __name__ == "__main__":
    unittest.main()
