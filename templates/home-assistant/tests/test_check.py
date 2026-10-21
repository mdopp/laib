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
                                                             ("caldav", ""), ("music_assistant", ""), ("mqtt", ""), ("mcp_server", ""),
                                                             ("ollama", "http://127.0.0.1:8710")]]
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
        self.assertIn("-e LAIB_SERVICE", unit)   # #528: the HA_INSTALL proposal names the service
        self.assertNotIn("OnCalendar=hourly", (SCRIPT.parent / "home-assistant-check.timer").read_text(),
                         "a dead Z-Wave network must not wait an hour to turn red")

    def test_a_box_without_the_zwave_unit_checks_no_zwave(self):
        # the VM and a box without a stick drop home-assistant-zwave.container: no ZWAVE_URL, no red
        import re, subprocess, tempfile
        unit = (SCRIPT.parent / "home-assistant-check.service").read_text()
        cmd = re.search(r"^ExecStart=/bin/sh -c '(.*)'$", unit, re.M).group(1)
        with tempfile.TemporaryDirectory() as home:
            fake = Path(home) / "podman"
            fake.write_text('#!/bin/sh\necho "ZWAVE_URL=${ZWAVE_URL-unset}"\n')
            fake.chmod(0o755)
            run = lambda: subprocess.run(["/bin/sh", "-c", cmd.replace("%h", home).replace("/usr/bin/podman", str(fake))],
                                         env={"PATH": "/usr/bin:/bin", "ZWAVE_URL": "http://127.0.0.1:8091"},
                                         capture_output=True, text=True).stdout.strip()
            self.assertEqual(run(), "ZWAVE_URL=unset")
            d = Path(home) / ".config/containers/systemd/home-assistant"
            d.mkdir(parents=True)
            (d / "home-assistant-zwave.container").write_text("")
            self.assertEqual(run(), "ZWAVE_URL=http://127.0.0.1:8091")

    def test_names_each_missing_integration(self):
        srv, h = fake_ha([{"domain": "wyoming", "title": "faster-whisper"}, {"domain": "mqtt", "title": "Mosquitto"},
                          {"domain": "ollama", "title": "http://127.0.0.1:8787/ollama"}])
        r = self.run_check("tok", f"http://127.0.0.1:{srv.server_port}")
        self.assertEqual(h.seen[0], ("/api/config/config_entries/entry", "Bearer tok"))
        missing = [f["detail"] for f in r["findings"]]
        self.assertIn("Wyoming Piper: fehlt", missing)
        self.assertIn("CalDAV: fehlt", missing)
        self.assertNotIn("Wyoming Whisper: fehlt", missing)
        self.assertNotIn("MQTT: fehlt", missing)
        # #491: Frage's agent is the Ollama entry on 8710; Solaris's on 8787 is not it, and its fix says how
        self.assertIn("Frage als Gesprächsagent: fehlt", missing)
        self.assertIn("laib apply home-assistant", next(f["fix"] for f in r["findings"] if f["detail"].startswith("Frage")))
        self.assertTrue(all(f["severity"] == "gelb" for f in r["findings"]))
        self.assertTrue(r["at"].endswith("Z") and r["detail"].startswith("fehlt: "))

    def test_complete_and_unreachable_and_no_token(self):
        everything = [{"domain": d, "title": t} for _, d, t in
                      [("", "wyoming", "whisper"), ("", "wyoming", "piper"), ("", "wyoming", "openwakeword"), ("", "caldav", ""),
                       ("", "music_assistant", ""), ("", "mqtt", ""), ("", "mcp_server", ""), ("", "ollama", "http://127.0.0.1:8710")]]
        srv, _ = fake_ha(everything)
        r = self.run_check("tok", f"http://127.0.0.1:{srv.server_port}")
        self.assertEqual((r["findings"], r["detail"]), ([], "alle 8 Integrationen eingerichtet"))
        srv, _ = fake_ha([], status=401)
        r = self.run_check("bad", f"http://127.0.0.1:{srv.server_port}")
        self.assertIn("antwortet nicht", r["findings"][0]["detail"])
        r = self.run_check("", "http://127.0.0.1:1")
        self.assertEqual((r["findings"], r["detail"]), ([], ""))

    def test_updates_are_reported_and_the_extensions_offered_as_one_proposal(self):
        # #528: "Einige Plugins in Home Assistant sind schon 4 Monate alt." HA's update entities that are
        # on: the extensions as "Update: …" with their release notes, the installable ones as one HA_INSTALL
        # proposal (the fix the Operator files as it stands), the firmware as "Firmware: …", only reported.
        upd = lambda eid, title, old, new, feat=1, cls=None, url=None: {"entity_id": eid, "state": "on", "attributes": {
            "title": title, "installed_version": old, "latest_version": new, "supported_features": feat, "device_class": cls, "release_url": url}}
        states = [upd("update.senec_update", "SENEC.Home", "2026.6.3", "2026.10.0", url="https://github.com/x/senec/releases/tag/2026.10.0"),
                  upd("update.meross_lan_update", "Meross LAN", "5.3.0", "5.4.1"),
                  upd("update.plug_firmware", "Steckdose", "9.5.52", "9.5.60", cls="firmware"),
                  upd("update.no_install", "Nur Anzeige", "1", "2", feat=0),
                  {"entity_id": "update.hacs_update", "state": "off", "attributes": {"installed_version": "2.0", "latest_version": "2.0"}},
                  {"entity_id": "sun.sun", "state": "above_horizon", "attributes": {}}]

        class H(BaseHTTPRequestHandler):
            def do_GET(self):
                body = json.dumps(states if self.path == "/api/states" else []).encode()
                self.send_response(200)
                self.end_headers()
                self.wfile.write(body)

            def log_message(self, *a):
                pass
        srv = HTTPServer(("127.0.0.1", 0), H)
        threading.Thread(target=srv.serve_forever, daemon=True).start()
        r = self.run_check("tok", f"http://127.0.0.1:{srv.server_port}")
        by = {f["detail"]: f for f in r["findings"] if f["severity"] == "info"}
        self.assertIn("Update: SENEC.Home 2026.6.3 → 2026.10.0 · https://github.com/x/senec/releases/tag/2026.10.0", by)
        self.assertIn("Update: Meross LAN 5.3.0 → 5.4.1", by)
        self.assertIn("Firmware: Steckdose 9.5.52 → 9.5.60 (nur gemeldet)", by)
        self.assertIn("Update: Nur Anzeige 1 → 2", by)
        offer = [f for f in r["findings"] if f["fix"].startswith("laib propose")]
        self.assertEqual(len(offer), 1, r)
        self.assertEqual(offer[0]["fix"], "laib propose --set services/home-assistant/values.yml:HA_INSTALL=update.senec_update=2026.10.0,update.meross_lan_update=5.4.1")
        self.assertIn("https://github.com/x/senec/releases/tag/2026.10.0", offer[0]["detail"], "the proposal carries the release notes")
        self.assertNotIn("firmware", offer[0]["fix"])
        self.assertFalse([f for f in r["findings"] if "hacs" in f["detail"].lower()], "an extension that is current is no finding")


if __name__ == "__main__":
    unittest.main()
