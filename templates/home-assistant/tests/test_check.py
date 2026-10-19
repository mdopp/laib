"""#55, #309: the integrations checklist moved out of laib's kernel into this
template. home-assistant-check.py reads HA's config entries with HA_TOKEN and
writes the status file laib shows; here against a fake Home Assistant."""
import json, os, subprocess, sys, tempfile, threading, unittest
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent.parent / "home-assistant-check.py"


def fake_ha(entries, status=200):
    class H(BaseHTTPRequestHandler):
        seen = []

        def do_GET(self):
            H.seen.append((self.path, self.headers.get("Authorization")))
            body = json.dumps(entries).encode()
            self.send_response(status)
            self.end_headers()
            self.wfile.write(body)

        def log_message(self, *a):
            pass
    srv = HTTPServer(("127.0.0.1", 0), H)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    return srv, H


def fake_zwave(code):
    class Z(BaseHTTPRequestHandler):
        seen = []

        def do_GET(self):
            Z.seen.append(self.path)
            self.send_response(code if self.path == "/health/zwave" else 200)
            self.end_headers()
            self.wfile.write(b"{}")

        def log_message(self, *a):
            pass
    srv = HTTPServer(("127.0.0.1", 0), Z)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    return srv, Z


class Check(unittest.TestCase):
    def run_check(self, token, url, zwave="", rc=0):
        d = tempfile.mkdtemp()
        out = os.path.join(d, ".laib-status.json")
        # the unit feeds the script on stdin, as `podman exec -i … python3 -` does
        p = subprocess.run([sys.executable, "-"], stdin=open(SCRIPT), capture_output=True, text=True,
                           env=dict(os.environ, HA_TOKEN=token, HA_URL=url, LAIB_STATUS_FILE=out, ZWAVE_URL=zwave))
        self.assertEqual(p.returncode, rc, p.stdout + p.stderr)
        return json.loads(Path(out).read_text())

    def test_a_zwave_without_its_controller_is_red(self):
        # #417: zwave-js-ui retried the stick all night (SELinux EACCES), Home Assistant answered 200,
        # laib status stayed green. The check asks zwave-js-ui whether its driver is ready; when not, a
        # red finding and a failed unit, which laib status shows red.
        everything = [{"domain": d, "title": t} for d, t in [("wyoming", "whisper"), ("wyoming", "piper"), ("wyoming", "openwakeword"),
                                                             ("caldav", ""), ("music_assistant", ""), ("mqtt", ""), ("mcp_server", "")]]
        ha, _ = fake_ha(everything)
        zw, seen = fake_zwave(500)
        r = self.run_check("tok", f"http://127.0.0.1:{ha.server_port}", f"http://127.0.0.1:{zw.server_port}", rc=1)
        self.assertIn("/health/zwave", seen.seen)
        rot = [f for f in r["findings"] if f["severity"] == "rot"]
        self.assertEqual(len(rot), 1, r)
        self.assertIn("Z-Wave", rot[0]["detail"])
        self.assertIn("Z-Wave", r["detail"])
        zw, _ = fake_zwave(200)
        r = self.run_check("tok", f"http://127.0.0.1:{ha.server_port}", f"http://127.0.0.1:{zw.server_port}")
        self.assertFalse([f for f in r["findings"] if f["severity"] == "rot"], r)
        # zwave-js-ui not answering at all is red too
        r = self.run_check("tok", f"http://127.0.0.1:{ha.server_port}", "http://127.0.0.1:1", rc=1)
        self.assertTrue([f for f in r["findings"] if f["severity"] == "rot"], r)

    def test_the_unit_asks_zwave_and_runs_often(self):
        unit = (SCRIPT.parent / "home-assistant-check.service").read_text()
        self.assertIn("ZWAVE_URL=http://127.0.0.1:8091", unit)
        self.assertIn("-e ZWAVE_URL", unit)
        self.assertNotIn("OnCalendar=hourly", (SCRIPT.parent / "home-assistant-check.timer").read_text(),
                         "a dead Z-Wave network must not wait an hour to turn red")

    def test_names_each_missing_integration(self):
        srv, h = fake_ha([{"domain": "wyoming", "title": "faster-whisper"}, {"domain": "mqtt", "title": "Mosquitto"}])
        r = self.run_check("tok", f"http://127.0.0.1:{srv.server_port}")
        self.assertEqual(h.seen[0], ("/api/config/config_entries/entry", "Bearer tok"))
        missing = [f["detail"] for f in r["findings"]]
        self.assertIn("Wyoming Piper: fehlt", missing)
        self.assertIn("CalDAV: fehlt", missing)
        self.assertNotIn("Wyoming Whisper: fehlt", missing)
        self.assertNotIn("MQTT: fehlt", missing)
        self.assertTrue(all(f["severity"] == "gelb" for f in r["findings"]))
        self.assertTrue(r["at"].endswith("Z") and r["detail"].startswith("fehlt: "))

    def test_complete_and_unreachable_and_no_token(self):
        everything = [{"domain": d, "title": t} for _, d, t in
                      [("", "wyoming", "whisper"), ("", "wyoming", "piper"), ("", "wyoming", "openwakeword"), ("", "caldav", ""),
                       ("", "music_assistant", ""), ("", "mqtt", ""), ("", "mcp_server", "")]]
        srv, _ = fake_ha(everything)
        r = self.run_check("tok", f"http://127.0.0.1:{srv.server_port}")
        self.assertEqual((r["findings"], r["detail"]), ([], "alle 7 Integrationen eingerichtet"))
        srv, _ = fake_ha([], status=401)
        r = self.run_check("bad", f"http://127.0.0.1:{srv.server_port}")
        self.assertIn("antwortet nicht", r["findings"][0]["detail"])
        r = self.run_check("", "http://127.0.0.1:1")
        self.assertEqual((r["findings"], r["detail"]), ([], ""))


if __name__ == "__main__":
    unittest.main()
