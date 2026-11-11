"""#785: file-share-check.py writes the share's detail line for the Box page:
the space left on the share's disk (statvfs), Syncthing's devices and how many
are connected (its REST API with the key from its own config.xml, never
printed), and who has a network-drive password (samba-accounts.sh's status
file). A fake Syncthing stands in; a part that cannot be measured says so,
never a number."""
import json, os, subprocess, sys, tempfile, threading, unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent.parent / "file-share-check.py"
KEY = "-st-apikey-Wq"
ME, LAPTOP, PHONE, TABLET = "MYID-AAAA", "LAPTOP-BBBB", "PHONE-CCCC", "TABLET-DDDD"
CONFIG = f"""<configuration version="37">
    <device id="{ME}" name="box" compression="metadata"></device>
    <device id="{LAPTOP}" name="laptop"></device>
    <device id="{PHONE}" name="phone"></device>
    <device id="{TABLET}" name="tablet"></device>
    <gui enabled="true" tls="false">
        <address>127.0.0.1:8384</address>
        <apikey>{KEY}</apikey>
    </gui>
</configuration>
"""


class Fake:
    def __init__(self, connected=(LAPTOP, PHONE)):
        outer = self
        self.connected = connected

        class H(BaseHTTPRequestHandler):
            def log_message(self, *a): pass

            def do_GET(self):
                if self.headers.get("X-API-Key") != KEY:
                    body, code = {"error": "forbidden"}, 403
                elif self.path == "/rest/system/status":
                    body, code = {"myID": ME}, 200
                elif self.path == "/rest/config/devices":
                    body, code = [{"deviceID": d} for d in (ME, LAPTOP, PHONE, TABLET)], 200
                elif self.path == "/rest/system/connections":
                    body, code = {"connections": {d: {"connected": d in outer.connected} for d in (LAPTOP, PHONE, TABLET)}}, 200
                else:
                    body, code = None, 404
                b = json.dumps(body).encode()
                self.send_response(code); self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(b))); self.end_headers(); self.wfile.write(b)

        self.srv = ThreadingHTTPServer(("127.0.0.1", 0), H)
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()
        self.url = f"http://127.0.0.1:{self.srv.server_address[1]}"


def run(url, config=CONFIG, accounts="admin 0\ncdopp 1761400000\nmdopp 1761500000\n", share=None):
    d, st, acc = tempfile.mkdtemp(), tempfile.mkdtemp(), tempfile.mkdtemp()
    if config is not None:
        Path(st, "config.xml").write_text(config)
    if accounts is not None:
        Path(acc, "status").write_text(accounts)
    env = {"PATH": os.environ["PATH"], "SYNCTHING_URL": url, "SYNCTHING_CONFIG": str(Path(st, "config.xml")),
           "ACCOUNTS_DIR": acc, "SHARE_DIR": share or tempfile.gettempdir(), "STATUS_DIR": d}
    r = subprocess.run([sys.executable, "-"], input=SCRIPT.read_text(), text=True, capture_output=True, env=env, timeout=60)
    with open(os.path.join(d, "laib-status.json")) as f:
        return r, json.load(f)


class Check(unittest.TestCase):
    def test_space_syncthing_and_accounts(self):
        r, st = run(Fake().url)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertRegex(st["detail"], r"^\d+(,\d)? (TB|GB) frei von \d+(,\d)? (TB|GB) · Syncthing: 2 von 3 Geräten verbunden · "
                                       r"Netzlaufwerk: 2 von 3 Personen mit Passwort$")
        self.assertEqual([f["topic"] for f in st["findings"]], ["netzlaufwerk.admin"])   # #1168: admin has none
        self.assertNotIn(KEY, r.stdout + r.stderr + json.dumps(st))

    def test_a_member_without_password_asks_for_their_task(self):
        # #1168: one ask per member without a password, for that person, the way to Homes' page;
        # the finding's words name nobody (the admins read the count), and a name the kernel
        # could not take as a login asks for nothing
        _, st = run(Fake().url, accounts="anna 0\nbert 1761400000\nCarl 0\nmdopp 0\n")
        self.assertIn("Netzlaufwerk: 1 von 4 Personen mit Passwort", st["detail"])
        asks = {f["topic"]: f for f in st["findings"]}
        self.assertEqual(sorted(asks), ["netzlaufwerk.anna", "netzlaufwerk.mdopp"])
        a = asks["netzlaufwerk.anna"]
        self.assertEqual((a["severity"], a["neue_aufgabe"]["fuer"], a["neue_aufgabe"]["weg"]), ("info", "person:anna", {"seite": "/netzlaufwerk"}))
        self.assertEqual(a["neue_aufgabe"]["titel"], "Netzlaufwerk-Passwort festlegen")
        self.assertNotIn("anna", a["detail"])
        self.assertFalse(a.get("unmeasured"))
        _, st = run(Fake().url, accounts="anna 1761400000\n")
        self.assertEqual(st["findings"], [], "everyone has one: nothing asked, an open task closes")

    def test_size_words(self):
        sys.path.insert(0, str(SCRIPT.parent))
        import importlib.util
        spec = importlib.util.spec_from_file_location("fsc", SCRIPT)
        m = importlib.util.module_from_spec(spec); spec.loader.exec_module(m)
        self.assertEqual(m.size(1_234_000_000_000), "1,2 TB")
        self.assertEqual(m.size(420_800_000_000), "420,8 GB")
        self.assertEqual(m.size(3_600_000_000_000), "3,6 TB")

    def test_syncthing_silent_is_not_measured_the_rest_stays(self):
        _, st = run("http://127.0.0.1:9")
        self.assertIn("Syncthing nicht gemessen", st["detail"])
        self.assertIn("frei von", st["detail"])
        self.assertIn("Netzlaufwerk: 2 von 3 Personen mit Passwort", st["detail"])
        self.assertTrue(any(f.get("unmeasured") for f in st["findings"]))

    def test_no_key_and_no_accounts(self):
        _, st = run(Fake().url, config="<configuration></configuration>", accounts=None)
        self.assertIn("Syncthing nicht gemessen", st["detail"])
        self.assertNotIn("Netzlaufwerk", st["detail"], "no status file yet: nothing to say about passwords")
        self.assertNotIn(KEY, json.dumps(st))

    def test_wrong_key(self):
        _, st = run(Fake().url, config=CONFIG.replace(KEY, "other"))
        self.assertIn("Syncthing nicht gemessen", st["detail"])
        self.assertNotIn("von 3 Geräten", st["detail"])


if __name__ == "__main__":
    unittest.main()
