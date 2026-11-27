#!/usr/bin/env python3
"""The scanner as a device in Home Assistant (homes ADR 0004 §6): MQTT
discovery on the box's Mosquitto, the standard library only.

It announces one device „Scanner“ with a button „Scannen – Haushalt“
(unique_id laib-scan-haushalt) and a sensor „Zustand“ (laib-scan-zustand:
bereit, scannt, wartet, fertig, fehler). A press is the order `laib scan
start` writes for the household (haushalt/scan.go): one JSON file in
/data/auftrag/, written whole and renamed, no `fuer`, no person. The scan
service takes it within seconds and decides; this bridge decides nothing.

No button per person (the owner on laib#1287, 2026-11-27): Home Assistant
has no permission per entity and a press carries no user, so any HA user
could file papers into another person's Paperless. „Für mich“ stays where
the person is known: Homes' Scannen and the tag at the scanner.

- Discovery configs and the state are retained; the availability topic is
  the connection's will, so a bridge that is gone reads „nicht verfügbar“.
- No scanner (the status says scanner: false): every config is cleared, and
  Home Assistant removes the device. No status file yet, or one that cannot
  be read: nothing is published and nothing cleared.
- On every connect the bridge clears any retained config under its node it
  does not want: the person buttons an earlier build published go too.
- The sensor carries the state and the page or sheet numbers, never the
  status file's sentence or who started the stack: Home Assistant shows its
  attributes to every one of its users, and a finished stack's sentence
  names documents.

A socket error ends the process; systemd starts it again (Restart=always).
Without a password (no Mosquitto on the box) it says so once and idles.

Environment: MQTT_HOST, MQTT_PORT, MQTT_USER, MQTT_PASSWORD, SCAN_DATA
(/data), DISCOVERY_PREFIX (homeassistant).
"""
import datetime, json, os, select, signal, socket, sys, time

PREFIX = os.environ.get("DISCOVERY_PREFIX", "homeassistant")
NODE = "laib-scan"
BEFEHL, ZUSTAND, DA = "laib/scan/befehl", "laib/scan/zustand", "laib/scan/verfuegbar"
ZUSTAENDE = ["bereit", "scannt", "wartet", "fertig", "fehler"]
# the status file's zustand (templates/scan/laib.yml status) → the sensor's five
ABBILD = {"bereit": "bereit", "scannt": "scannt", "trennt": "scannt", "legt-ab": "scannt",
          "wartet-auf-rest": "wartet", "fertig": "fertig", "fehler": "fehler"}
DEVICE = {"identifiers": [NODE], "name": "Scanner", "manufacturer": "Laib", "model": "Dokumente (scan)"}
ORIGIN = {"name": "Laib scan", "support_url": "https://github.com/mdopp/laib/tree/main/templates/scan"}
KEEPALIVE = 60


def log(msg):
    print(msg, flush=True)


def lesen(path):
    try:
        with open(path) as f:
            return json.load(f)
    except (OSError, ValueError):
        return None


def gewollt(status):
    """topic → config payload for what the bridge announces now; {} without a scanner"""
    if not isinstance(status, dict) or not status.get("scanner"):
        return {}
    gemein = {"availability_topic": DA, "device": DEVICE, "origin": ORIGIN}
    out = {f"{PREFIX}/button/{NODE}/haushalt/config": {"name": "Scannen – Haushalt", "unique_id": "laib-scan-haushalt",
                                                      "command_topic": BEFEHL, "payload_press": "haushalt", "icon": "mdi:scanner", **gemein}}
    out[f"{PREFIX}/sensor/{NODE}/zustand/config"] = {"name": "Zustand", "unique_id": "laib-scan-zustand", "state_topic": ZUSTAND,
                                                     "value_template": "{{ value_json.zustand }}", "json_attributes_topic": ZUSTAND,
                                                     "device_class": "enum", "options": ZUSTAENDE, "icon": "mdi:scanner", **gemein}
    return {t: json.dumps(c, ensure_ascii=False, sort_keys=True) for t, c in out.items()}


def zustand(status):
    """the sensor's state topic: the five states, None (HA: unknown) for one it does not know"""
    roh = status.get("zustand") if isinstance(status, dict) else None
    roh = roh if isinstance(roh, str) else None
    z = {"zustand": ABBILD.get(roh), "roh": roh}
    for k in ("seite", "blatt"):
        if isinstance(status, dict) and isinstance(status.get(k), int):
            z[k] = status[k]
    return json.dumps(z, sort_keys=True)


def auftrag(data, befehl):
    """a press → the order `laib scan start` writes for the household; None for a payload that is no
    button of ours (a person's, which an earlier build had, included)"""
    if befehl != "haushalt":
        return None
    order = json.dumps({"aktion": "start", "stapel": "", "person": "", "fuer": "", "von": "home-assistant",
                        "at": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")})
    d = os.path.join(data, "auftrag")
    os.makedirs(d, exist_ok=True)
    name = str(time.time_ns())
    tmp = os.path.join(d, "." + name)   # written whole, then renamed: the service never reads half an order
    with open(tmp, "w") as f:
        f.write(order)
    os.replace(tmp, os.path.join(d, name + ".json"))
    return order


# a minimal MQTT 3.1.1 client: CONNECT with a will, SUBSCRIBE, PUBLISH at QoS 0, PINGREQ

def s8(x):
    b = x.encode() if isinstance(x, str) else x
    return len(b).to_bytes(2, "big") + b


def packet(head, body):
    n, ln = len(body), b""
    while True:
        d, n = n % 128, n // 128
        ln += bytes([d | (128 if n else 0)])
        if not n:
            return bytes([head]) + ln + body


class Getrennt(Exception):
    pass


class Mqtt:
    def __init__(self, host, port, user, pw, timeout=15):
        self.sock = socket.create_connection((host, port), timeout=timeout)
        self.buf = b""
        flags = 0x02 | 0x04 | 0x20 | 0x80 | 0x40   # clean session, a will, retained, user, password
        body = s8("MQTT") + bytes([4, flags]) + KEEPALIVE.to_bytes(2, "big") + s8("laib-scan") + s8(DA) + s8("offline") + s8(user) + s8(pw)
        self.sock.sendall(packet(0x10, body))
        head, b = self.read(timeout)
        if head is None or head >> 4 != 2 or len(b) < 2:
            raise Getrennt("keine Antwort auf CONNECT")
        if b[1]:
            raise Getrennt(f"Mosquitto lässt den Benutzer {user} nicht herein (Code {b[1]})")
        self.pid, self.sent = 0, time.monotonic()

    def send(self, data):
        self.sock.sendall(data)
        self.sent = time.monotonic()

    def publish(self, topic, payload, retain=True):
        self.send(packet(0x30 | (1 if retain else 0), s8(topic) + payload.encode()))

    def subscribe(self, topic):
        self.pid += 1
        self.send(packet(0x82, self.pid.to_bytes(2, "big") + s8(topic) + b"\x00"))

    def ping(self):
        if time.monotonic() - self.sent > KEEPALIVE / 2:
            self.send(b"\xc0\x00")

    def disconnect(self):
        try:
            self.send(b"\xe0\x00")
        finally:
            self.sock.close()

    def read(self, wait):
        """one packet (type byte, body) within wait seconds, or (None, b"")"""
        end = time.monotonic() + wait
        while True:
            p = self.parse()
            if p:
                return p
            left = end - time.monotonic()
            if left <= 0 or not select.select([self.sock], [], [], left)[0]:
                return None, b""
            c = self.sock.recv(65536)
            if not c:
                raise Getrennt("Mosquitto hat die Verbindung geschlossen")
            self.buf += c

    def parse(self):
        n, mult, i = 0, 1, 1
        while True:
            if len(self.buf) <= i:
                return None
            d = self.buf[i]
            n += (d & 127) * mult
            mult *= 128
            i += 1
            if not d & 128:
                break
        if len(self.buf) < i + n:
            return None
        head, body, self.buf = self.buf[0], self.buf[i:i + n], self.buf[i + n:]
        return head, body


def eingang(head, body):
    """a PUBLISH → (topic, payload, retained); None for any other packet"""
    if head >> 4 != 3:
        return None
    tl = int.from_bytes(body[:2], "big")
    off = 2 + tl + (2 if head & 0x06 else 0)
    return body[2:2 + tl].decode("utf-8", "replace"), body[off:].decode("utf-8", "replace"), bool(head & 1)


class Bruecke:
    def __init__(self, mqtt, data):
        self.m, self.data = mqtt, data
        self.veroeffentlicht, self.fremd = {}, set()
        self.zustand, self.da, self.zuletzt = None, None, 0.0

    def schritt(self):
        """the status file → configs, state and availability, only what changed.
        An unreadable status changes nothing: a device cleared and made again would get a new
        device id in HA, and every automation that names it would break."""
        status = lesen(os.path.join(self.data, "laib-status.json"))
        if not isinstance(status, dict):
            return
        soll = gewollt(status)
        for t in sorted((set(self.veroeffentlicht) | self.fremd) - set(soll)):
            self.m.publish(t, "")   # an empty retained config: HA removes the entity, and the device with its last
            log("entfernt: " + t)
        for t, c in soll.items():
            if self.veroeffentlicht.get(t) != c:
                self.m.publish(t, c)
        self.veroeffentlicht, self.fremd = soll, set()
        z = zustand(status)
        if soll and z != self.zustand:
            self.m.publish(ZUSTAND, z)
            self.zustand = z
        da = "online" if soll else "offline"
        if da != self.da:
            self.m.publish(DA, da)
            self.da = da

    def nachricht(self, topic, payload, retained):
        if topic == BEFEHL:
            self.druck(payload, retained)
        elif topic.endswith("/config") and payload and topic not in self.veroeffentlicht:
            self.fremd.add(topic)   # left by an earlier run (a person's button, the scanner gone): cleared on the next step
        elif topic.endswith("/config") and not payload:
            self.fremd.discard(topic)

    def druck(self, payload, retained):
        if retained:   # a press is never retained; one that is would start a stack on every reconnect
            log("befehl verworfen: als retained gesendet")
        elif not self.veroeffentlicht:
            log("befehl %r verworfen: kein Scanner eingerichtet" % payload[:40])
        elif time.monotonic() - self.zuletzt < 3:
            log("befehl %r verworfen: zweimal gedrückt" % payload[:40])
        elif auftrag(self.data, payload) is None:
            log("befehl %r verworfen: kein Knopf des Scanners" % payload[:40])
        else:
            self.zuletzt = time.monotonic()
            log("auftrag start von home-assistant: " + payload)


def main():
    host, port = os.environ.get("MQTT_HOST", "127.0.0.1"), int(os.environ.get("MQTT_PORT", "1883"))
    user, pw = os.environ.get("MQTT_USER", "scan"), os.environ.get("MQTT_PASSWORD", "")
    data = os.environ.get("SCAN_DATA", "/data")
    if not pw:
        log("kein Zugang zu Mosquitto (SCAN_MQTT_PASSWORD fehlt: ohne Mosquitto auf der Box kein Scanner in Home Assistant)")
        while True:
            time.sleep(3600)
    m = Mqtt(host, port, user, pw)
    b = Bruecke(m, data)

    def ende(*_):
        m.publish(DA, "offline")
        m.disconnect()
        sys.exit(0)

    signal.signal(signal.SIGTERM, ende)
    b.schritt()   # what is wanted first, so the retained configs that arrive next can be judged
    m.subscribe(BEFEHL)
    m.subscribe(f"{PREFIX}/+/{NODE}/+/config")
    log("verbunden mit %s:%d als %s" % (host, port, user))
    naechster = time.monotonic() + 2
    while True:
        head, body = m.read(max(0.0, naechster - time.monotonic()))
        e = eingang(head, body) if head is not None else None
        if e:
            b.nachricht(*e)
        if time.monotonic() >= naechster:
            b.schritt()
            m.ping()
            naechster = time.monotonic() + 2


if __name__ == "__main__":
    try:
        main()
    except (OSError, Getrennt) as e:
        log("getrennt: %s" % e)
        sys.exit(1)
