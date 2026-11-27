"""homes ADR 0004 §6: scan-mqtt.py makes the scanner an ordinary device in
Home Assistant through MQTT discovery, and a press the order `laib scan
start` writes.

The bridge runs as on the box (a process with its environment and the scan
service's folder); the broker is a fake that
speaks enough MQTT 3.1.1 (CONNECT with a will, SUBSCRIBE with + wildcards,
retained PUBLISH delivered on subscribe with the retain flag, PINGREQ) and
plays Home Assistant pressing a button. Never a live box."""
# Ablauf L-65
import json, os, signal, socket, socketserver, subprocess, sys, tempfile, threading, time, unittest
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent.parent / "scan-mqtt.py"
PW = "-scan-mqtt-pw-Q1"
CFG = "homeassistant/{}/laib-scan/{}/config"


def utf8(b, i):
    n = int.from_bytes(b[i:i + 2], "big")
    return b[i + 2:i + 2 + n].decode(), i + 2 + n


def read_packet(f):
    h = f.read(1)
    if not h:
        return None, b""
    mult, n = 1, 0
    while True:
        d = f.read(1)[0]
        n += (d & 127) * mult
        mult *= 128
        if not d & 128:
            break
    return h[0], f.read(n)


def packet(head, body):
    n, ln = len(body), b""
    while True:
        d, n = n % 128, n // 128
        ln += bytes([d | (128 if n else 0)])
        if not n:
            return bytes([head]) + ln + body


def publish_packet(topic, payload, retain):
    t = topic.encode()
    return packet(0x30 | (1 if retain else 0), len(t).to_bytes(2, "big") + t + payload.encode())


def matches(pattern, topic):
    p, t = pattern.split("/"), topic.split("/")
    if len(p) != len(t):
        return False
    return all(a == "+" or a == b for a, b in zip(p, t))


class Broker:
    """retained store, subscriptions, the will; records what the bridge sent"""

    def __init__(self, retained=None):
        self.retained = dict(retained or {})
        self.log, self.subs, self.wills, self.clients, self.lock = [], [], [], [], threading.Lock()
        outer = self

        class H(socketserver.StreamRequestHandler):
            def handle(self):
                t, body = read_packet(self.rfile)
                proto, i = utf8(body, 0)
                level, flags = body[i], body[i + 1]
                i += 4
                _, i = utf8(body, i)
                will = None
                if flags & 0x04:
                    wt, i = utf8(body, i)
                    wp, i = utf8(body, i)
                    will = (wt, wp, bool(flags & 0x20))
                user, i = utf8(body, i) if flags & 0x80 else ("", i)
                pw, i = utf8(body, i) if flags & 0x40 else ("", i)
                ok = proto == "MQTT" and level == 4 and user == "scan" and pw == PW
                self.wfile.write(bytes([0x20, 2, 0, 0 if ok else 5]))
                if not ok:
                    return
                with outer.lock:
                    outer.wills.append(will)
                    outer.clients.append(self)
                clean = False
                try:
                    while True:
                        t, body = read_packet(self.rfile)
                        if t is None:
                            break
                        kind = t >> 4
                        if kind == 14:
                            clean = True
                            break
                        if kind == 12:
                            self.wfile.write(b"\xd0\x00")
                        elif kind == 8:
                            pid = body[:2]
                            topic, _ = utf8(body, 2)
                            with outer.lock:
                                outer.subs.append(topic)
                                hits = [(k, v) for k, v in outer.retained.items() if matches(topic, k)]
                            self.wfile.write(bytes([0x90, 3]) + pid + b"\x00")
                            for k, v in hits:
                                self.wfile.write(publish_packet(k, v, True))
                        elif kind == 3:
                            topic, j = utf8(body, 0)
                            payload = body[j:].decode()
                            outer.deliver(topic, payload, bool(t & 1), sender=self)
                finally:
                    with outer.lock:
                        outer.clients.remove(self)
                    if not clean and will:
                        outer.deliver(*will, sender=None)

        socketserver.ThreadingTCPServer.allow_reuse_address = True
        self.srv = socketserver.ThreadingTCPServer(("127.0.0.1", 0), H)
        self.srv.daemon_threads = True
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()
        self.port = self.srv.server_address[1]

    def deliver(self, topic, payload, retain, sender):
        with self.lock:
            if sender is not None:
                self.log.append((topic, payload, retain))
            if retain:
                if payload:
                    self.retained[topic] = payload
                else:
                    self.retained.pop(topic, None)
            targets = [c for c in self.clients if any(matches(s, topic) for s in self.subs)]
        for c in targets:   # a subscriber gets a live message without the retain flag
            try:
                c.wfile.write(publish_packet(topic, payload, False))
            except OSError:
                pass

    def press(self, payload, retain=False):
        """Home Assistant pressing a button: a publish on the command topic"""
        self.deliver("laib/scan/befehl", payload, retain, sender=None)

    def stop(self):
        self.srv.shutdown()
        self.srv.server_close()


def wait(cond, what, timeout=8):
    end = time.time() + timeout
    while time.time() < end:
        if cond():
            return
        time.sleep(0.05)
    raise AssertionError("timed out waiting for " + what)


class Bridge(unittest.TestCase):
    def setUp(self):
        self.dir = Path(tempfile.mkdtemp())
        self.data = self.dir / "scan"
        self.data.mkdir()
        self.status({"zustand": "bereit", "scanner": True, "detail": "bereit"})
        self.proc, self.broker = None, None

    def tearDown(self):
        if self.proc and self.proc.poll() is None:
            self.proc.kill()
            self.proc.wait()
        if self.proc and self.proc.stdout:
            self.proc.stdout.close()
        if self.broker:
            self.broker.stop()

    def status(self, s):
        (self.data / "laib-status.json").write_text(json.dumps(s))

    def start(self, retained=None, pw=PW):
        self.broker = Broker(retained)
        env = {"PATH": os.environ["PATH"], "MQTT_HOST": "127.0.0.1", "MQTT_PORT": str(self.broker.port), "MQTT_USER": "scan",
               "MQTT_PASSWORD": pw, "SCAN_DATA": str(self.data)}
        self.proc = subprocess.Popen([sys.executable, str(SCRIPT)], env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
        return self.broker

    def config(self, kind, obj):
        v = self.broker.retained.get(CFG.format(kind, obj))
        return json.loads(v) if v else None

    def orders(self):
        d = self.data / "auftrag"
        return [json.loads(f.read_text()) for f in sorted(d.glob("*.json"))] if d.exists() else []

    def announced(self):
        wait(lambda: self.config("sensor", "zustand") and self.broker.retained.get("laib/scan/verfuegbar") == "online", "the device")

    def test_the_device_has_the_household_button_and_its_state_and_no_person_button(self):
        b = self.start()
        self.announced()
        haus = self.config("button", "haushalt")
        self.assertEqual((haus["name"], haus["unique_id"], haus["payload_press"]), ("Scannen – Haushalt", "laib-scan-haushalt", "haushalt"))
        z = self.config("sensor", "zustand")
        self.assertEqual((z["unique_id"], z["device_class"], z["options"]), ("laib-scan-zustand", "enum", ["bereit", "scannt", "wartet", "fertig", "fehler"]))
        for c in (haus, z):
            self.assertEqual(c["device"]["identifiers"], ["laib-scan"])
            self.assertEqual(c["device"]["name"], "Scanner")
            self.assertEqual(c["availability_topic"], "laib/scan/verfuegbar")
        # the owner on laib#1287: no button per person, any HA user could press another's
        self.assertEqual(sorted(t for t in b.retained if t.endswith("/config")),
                         [CFG.format("button", "haushalt"), CFG.format("sensor", "zustand")])
        self.assertEqual(json.loads(b.retained["laib/scan/zustand"])["zustand"], "bereit")
        # every config and the availability are retained: HA finds them after its own restart
        self.assertTrue(all(r for t, _, r in b.log if t.endswith("/config") or t == "laib/scan/verfuegbar"), b.log)
        # the will: a bridge that is gone reads „nicht verfügbar“
        self.assertEqual(b.wills, [("laib/scan/verfuegbar", "offline", True)])

    def test_a_press_is_the_order_laib_scan_start_writes_for_the_household(self):
        b = self.start()
        self.announced()
        b.press("haushalt")
        wait(lambda: len(self.orders()) == 1, "the household's order")
        haus = self.orders()[0]
        self.assertEqual({k: haus[k] for k in ("aktion", "stapel", "person", "fuer", "von")},
                         {"aktion": "start", "stapel": "", "person": "", "fuer": "", "von": "home-assistant"})
        self.assertEqual(sorted(haus), ["aktion", "at", "fuer", "person", "stapel", "von"], "the same keys as haushalt/scan.go's order")
        self.assertEqual([f.name for f in (self.data / "auftrag").iterdir() if f.name.startswith(".")], [], "a half-written order is left")

    def test_a_press_that_is_no_button_of_ours_starts_nothing(self):
        b = self.start()
        self.announced()
        b.press("person:mama")            # an earlier build's person button, still on a dashboard
        b.press("person:../../etc")
        b.press("haushalt", retain=True)  # a retained press would start a stack on every reconnect
        b.press("haushalt")
        b.press("haushalt")               # pressed twice: one stack
        wait(lambda: len(self.orders()) >= 1, "the one good press")
        time.sleep(0.5)
        self.assertEqual(len(self.orders()), 1, self.orders())
        self.assertEqual(self.orders()[0]["fuer"], "")

    def test_the_state_maps_the_status_files_states_and_says_nothing_of_whose_stack(self):
        b = self.start()
        self.announced()
        for roh, want in (("scannt", "scannt"), ("trennt", "scannt"), ("legt-ab", "scannt"), ("wartet-auf-rest", "wartet"),
                          ("fertig", "fertig"), ("fehler", "fehler"), ("etwas-neues", None)):
            self.status({"zustand": roh, "scanner": True, "detail": "Scan fertig: 2023-05-01 Arzt - Befund", "person": "mama", "seite": 3})
            wait(lambda: json.loads(b.retained["laib/scan/zustand"]).get("roh") == roh, roh)
            z = json.loads(b.retained["laib/scan/zustand"])
            self.assertEqual(z["zustand"], want, roh)
            self.assertEqual(z.get("seite"), 3)
            self.assertNotIn("mama", b.retained["laib/scan/zustand"])
            self.assertNotIn("Befund", b.retained["laib/scan/zustand"])

    def test_the_person_buttons_an_earlier_build_left_are_cleared_on_connect(self):
        old = {CFG.format("button", "person-" + p): json.dumps({"unique_id": "laib-scan-person-" + p}) for p in ("mama", "lena")}
        b = self.start(retained=old)
        self.announced()
        wait(lambda: not [t for t in b.retained if "/person-" in t], "the old person buttons cleared")
        for p in ("mama", "lena"):
            self.assertIn((CFG.format("button", "person-" + p), "", True), b.log)
        self.assertIsNotNone(self.config("button", "haushalt"))

    def test_no_scanner_removes_the_device(self):
        b = self.start()
        self.announced()
        self.status({"zustand": "bereit", "scanner": False})
        wait(lambda: not [t for t in b.retained if t.endswith("/config")], "every config cleared")
        self.assertEqual(b.retained.get("laib/scan/verfuegbar"), "offline")
        b.press("haushalt")
        time.sleep(0.5)
        self.assertEqual(self.orders(), [])

    def test_an_unreadable_status_clears_nothing(self):
        old = {CFG.format("button", "haushalt"): json.dumps({"unique_id": "laib-scan-haushalt"})}
        (self.data / "laib-status.json").write_text("{kaputt")
        b = self.start(retained=old)
        wait(lambda: "laib/scan/befehl" in b.subs, "the subscription")
        time.sleep(2.5)
        self.assertIn(CFG.format("button", "haushalt"), b.retained, "a device cleared on a status it could not read")
        self.assertEqual(b.log, [])

    def test_a_stop_says_offline(self):
        b = self.start()
        self.announced()
        self.proc.send_signal(signal.SIGTERM)
        self.proc.wait(timeout=5)
        self.assertEqual(b.retained.get("laib/scan/verfuegbar"), "offline")
        self.assertIsNotNone(self.config("button", "haushalt"), "a restart must not remove the device: its id in HA would change")

    def test_without_a_password_it_idles_and_says_why(self):
        self.start(pw="")
        time.sleep(1)
        self.assertIsNone(self.proc.poll(), "it exits, and systemd would restart it every 30 s")
        self.proc.kill()
        out = self.proc.communicate()[0]
        self.assertIn("SCAN_MQTT_PASSWORD fehlt", out)
        self.assertEqual(self.broker.wills, [])

    def test_a_refused_login_ends_the_process_for_systemd_to_restart(self):
        self.start(pw="falsch")
        self.assertEqual(self.proc.wait(timeout=10), 1)
        self.assertIn("nicht herein", self.proc.communicate()[0])


if __name__ == "__main__":
    unittest.main()
