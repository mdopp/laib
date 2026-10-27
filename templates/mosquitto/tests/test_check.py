"""#785: mosquitto-check.py writes the broker's detail line for the Box page:
how many clients are connected, from the broker's own $SYS tree, asked as the
MQTT user over a minimal MQTT 3.1.1 session. A fake broker speaks enough
MQTT (CONNECT/CONNACK, SUBSCRIBE/SUBACK, a retained PUBLISH); a refused login
or a silent broker gives "nicht gemessen" and why, never a number."""
import json, os, socket, socketserver, subprocess, sys, tempfile, threading, unittest
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent.parent / "mosquitto-check.py"
PW = "-mqtt-pw-Zq"


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


class Broker:
    def __init__(self, connected=6, refuse=False, silent=False):
        self.connected, self.refuse, self.silent, self.subs, self.disconnected = connected, refuse, silent, [], False
        outer = self

        class H(socketserver.StreamRequestHandler):
            def handle(self):
                t, body = read_packet(self.rfile)
                assert t >> 4 == 1, t
                proto, i = utf8(body, 0)
                level, flags = body[i], body[i + 1]
                i += 4
                _, i = utf8(body, i)
                user, i = utf8(body, i) if flags & 0x80 else ("", i)
                pw, i = utf8(body, i) if flags & 0x40 else ("", i)
                ok = proto == "MQTT" and level == 4 and user == "homeassistant" and pw == PW and not outer.refuse
                self.wfile.write(bytes([0x20, 2, 0, 0 if ok else 5]))
                if not ok:
                    return
                while True:
                    t, body = read_packet(self.rfile)
                    if t is None:
                        return
                    if t >> 4 == 14:
                        outer.disconnected = True
                        return
                    if t >> 4 == 8:
                        pid = body[:2]
                        topic, _ = utf8(body, 2)
                        outer.subs.append(topic)
                        self.wfile.write(bytes([0x90, 3]) + pid + b"\x00")
                        if not outer.silent:
                            tb = topic.encode()
                            payload = str(outer.connected).encode()
                            rest = len(tb).to_bytes(2, "big") + tb + payload
                            self.wfile.write(bytes([0x31, len(rest)]) + rest)

        socketserver.ThreadingTCPServer.allow_reuse_address = True
        self.srv = socketserver.ThreadingTCPServer(("127.0.0.1", 0), H)
        self.srv.daemon_threads = True
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()
        self.port = self.srv.server_address[1]


def run(port, timeout="3"):
    d = tempfile.mkdtemp()
    env = {"PATH": os.environ["PATH"], "MQTT_HOST": "127.0.0.1", "MQTT_PORT": str(port), "MQTT_USER": "homeassistant",
           "MQTT_PASSWORD": PW, "STATUS_DIR": d, "MQTT_TIMEOUT": timeout}
    r = subprocess.run([sys.executable, "-"], input=SCRIPT.read_text(), text=True, capture_output=True, env=env, timeout=60)
    with open(os.path.join(d, "laib-status.json")) as f:
        return r, json.load(f)


class Check(unittest.TestCase):
    def test_connected_clients_without_the_check(self):
        b = Broker(connected=6)
        r, st = run(b.port)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(st["detail"], "5 Geräte und Dienste verbunden")
        self.assertEqual(b.subs, ["$SYS/broker/clients/connected"])
        self.assertEqual(st["findings"], [])
        self.assertRegex(st["at"], r"^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$")
        self.assertNotIn(PW, r.stdout + r.stderr + json.dumps(st))

    def test_only_the_check(self):
        _, st = run(Broker(connected=1).port)
        self.assertEqual(st["detail"], "kein Gerät verbunden")

    def test_one(self):
        _, st = run(Broker(connected=2).port)
        self.assertEqual(st["detail"], "1 Gerät oder Dienst verbunden")

    def test_refused_login_is_not_measured(self):
        _, st = run(Broker(refuse=True).port)
        self.assertTrue(st["detail"].startswith("nicht gemessen: Mosquitto lässt den Benutzer nicht herein"), st["detail"])
        self.assertTrue(st["findings"][0]["unmeasured"])

    def test_silent_sys_tree_is_not_measured(self):
        _, st = run(Broker(silent=True).port, timeout="1")
        self.assertTrue(st["detail"].startswith("nicht gemessen: Mosquitto nennt keine Verbindungszahl"), st["detail"])

    def test_broker_down_is_not_measured(self):
        s = socket.socket(); s.bind(("127.0.0.1", 0)); port = s.getsockname()[1]; s.close()
        _, st = run(port)
        self.assertTrue(st["detail"].startswith("nicht gemessen: Mosquitto antwortet nicht"), st["detail"])
        self.assertNotRegex(st["detail"], r"\d+ Geräte")


if __name__ == "__main__":
    unittest.main()
