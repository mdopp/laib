#!/usr/bin/env python3
"""mosquitto's detail line for the Box page (#785): how many devices and
services are connected to the broker, from its own $SYS tree.

A minimal MQTT 3.1.1 session in the standard library: CONNECT as the MQTT
user (the same as Home Assistant's), SUBSCRIBE to
$SYS/broker/clients/connected, which the broker publishes retained (and every
sys_interval, 10 s by default), read that one PUBLISH, DISCONNECT. Only
reads. The count is the broker's, minus this check itself. A refused login,
a silent broker or no number within MQTT_TIMEOUT is "nicht gemessen" and why,
never a number.

Environment: MQTT_HOST, MQTT_PORT, MQTT_USER, MQTT_PASSWORD, MQTT_TIMEOUT, STATUS_DIR.
"""
import datetime, json, os, socket

HOST, PORT = os.environ.get("MQTT_HOST", "127.0.0.1"), int(os.environ.get("MQTT_PORT", "1883"))
USER, PW = os.environ.get("MQTT_USER", ""), os.environ.get("MQTT_PASSWORD", "")
TIMEOUT = float(os.environ.get("MQTT_TIMEOUT", "15"))
STATUS = os.path.join(os.environ.get("STATUS_DIR", "/data"), "laib-status.json")
TOPIC = "$SYS/broker/clients/connected"
REFUSED = {1: "Protokoll", 2: "Client-ID", 3: "Broker nicht bereit", 4: "Benutzer oder Passwort falsch", 5: "nicht berechtigt"}


def status(detail, findings=()):
    s = {"at": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"), "detail": detail, "findings": list(findings)}
    os.makedirs(os.path.dirname(STATUS), exist_ok=True)
    with open(STATUS + ".tmp", "w") as f:
        json.dump(s, f, ensure_ascii=False, indent=1)
    os.replace(STATUS + ".tmp", STATUS)


def unmeasured(why, fix):
    status("nicht gemessen: " + why, [{"severity": "info", "unmeasured": True, "detail": "Verbindungen zu Mosquitto nicht gemessen: " + why, "fix": fix}])


def s8(x):
    b = x.encode()
    return len(b).to_bytes(2, "big") + b


def packet(head, body):
    n, ln = len(body), b""
    while True:
        d, n = n % 128, n // 128
        ln += bytes([d | (128 if n else 0)])
        if not n:
            return bytes([head]) + ln + body


def recv(sock, n):
    b = b""
    while len(b) < n:
        c = sock.recv(n - len(b))
        if not c:
            raise ConnectionError("Verbindung geschlossen")
        b += c
    return b


def read(sock):
    head = recv(sock, 1)[0]
    n = 0
    mult = 1
    while True:
        d = recv(sock, 1)[0]
        n += (d & 127) * mult
        mult *= 128
        if not d & 128:
            break
    return head, recv(sock, n)


class Refused(Exception):
    pass


def connected():
    with socket.create_connection((HOST, PORT), timeout=TIMEOUT) as sock:
        flags = 0x02 | (0x80 if USER else 0) | (0x40 if PW else 0)   # clean session, user, password
        body = s8("MQTT") + bytes([4, flags]) + (30).to_bytes(2, "big") + s8("laib-check") + (s8(USER) if USER else b"") + (s8(PW) if PW else b"")
        sock.sendall(packet(0x10, body))
        head, b = read(sock)
        if head >> 4 != 2 or len(b) < 2:
            raise ConnectionError("keine CONNACK")
        if b[1]:
            raise Refused(REFUSED.get(b[1], f"Code {b[1]}"))
        sock.sendall(packet(0x82, (1).to_bytes(2, "big") + s8(TOPIC) + b"\x00"))
        while True:
            head, b = read(sock)
            if head >> 4 == 9 and len(b) >= 3 and b[2] == 0x80:
                raise Refused("$SYS nicht lesbar")
            if head >> 4 == 3:
                tl = int.from_bytes(b[:2], "big")
                topic = b[2:2 + tl].decode()
                payload = b[2 + tl + (2 if head & 0x06 else 0):]
                if topic == TOPIC:
                    try:
                        sock.sendall(packet(0xE0, b""))
                    except OSError:
                        pass
                    return int(payload.decode().strip())


def main():
    try:
        n = connected()
    except Refused as e:
        return unmeasured(f"Mosquitto lässt den Benutzer nicht herein ({e})", "laib logs mosquitto; das Passwort: laib apply mosquitto")
    except socket.timeout:
        return unmeasured(f"Mosquitto nennt keine Verbindungszahl (keine Antwort in {TIMEOUT:g} s)", "laib logs mosquitto")
    except (OSError, ValueError, IndexError) as e:
        return unmeasured(f"Mosquitto antwortet nicht ({e})", "laib inspect mosquitto")
    n = max(n - 1, 0)   # this check is one of them
    status("kein Gerät verbunden" if n == 0 else "1 Gerät oder Dienst verbunden" if n == 1 else f"{n:,} Geräte und Dienste verbunden".replace(",", "."))


if __name__ == "__main__":
    main()
