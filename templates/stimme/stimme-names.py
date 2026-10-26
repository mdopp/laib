"""The household's names as Whisper's initial prompt.

Whisper turns a name it has never heard into whatever German words sound
alike: „Schalte Bambi an“ came back as „Schaut er beim Jahn.“ An initial
prompt that lists the names biases the decoder toward them. This reads what
the household exposed to Assist in Home Assistant (entities, their aliases,
the areas and floors they are in) and writes it, comma-joined, to the file
Whisper reads at start (WYO_WHISPER_INITIAL_PROMPT_FILE in
stimme-whisper.container).

The tiers are wyoming-faster-whisper's (its vocabulary.py and hass_api.py,
--hass-token), but the names of lights, switches, media players and the like
with their aliases come first, then the areas and floors that hold something
exposed, then the other areas, then everything else: Whisper knows „Küche“,
not „Bambi“, and with upstream's areas first a box's 50 exposed things were
cut after the T of the entity ids, Bambi with them. The budget is 200 tokens
counted with Whisper's own tokenizer (the tokenizer.json in its model cache)
where there is one, else upstream's estimate of two characters a token,
which on German names fills only about two thirds of it. Upstream fetches them itself, with an administrator's token and the
aiohttp extra this image lacks; here a non-administrator's token reads
`should_expose` from the entity registry, which every user may list.
Entities Home Assistant keeps outside its registry (zone.home and the like)
are left out.

It runs inside the Whisper container (stimme-names.service, `podman exec -i …
python3 -`), the stdlib only, and prints "names changed" only when the file
changed: the unit restarts Whisper then, and only then. Home Assistant away
keeps the last names; a refused token fails the unit.
"""
import base64
import json
import os
import socket
import struct
import sys
import urllib.parse

BUDGET = 200   # vocabulary.DEFAULT_PROMPT_MAX_TOKENS: well under Whisper's hard cap of 223
SPOKEN = {"light", "switch", "fan", "media_player", "climate", "scene", "todo", "cover", "lock", "vacuum"}  # hass_api.PRIORITY_DOMAINS, and what Assist switches


class WS:
    """Home Assistant's WebSocket API, one command at a time (stdlib only)"""

    def __init__(self, base: str, token: str):
        u = urllib.parse.urlsplit(base)
        self.s = socket.create_connection((u.hostname, u.port or 80), timeout=10)
        self.s.sendall((f"GET /api/websocket HTTP/1.1\r\nHost: {u.netloc}\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"
                        f"Sec-WebSocket-Key: {base64.b64encode(os.urandom(16)).decode()}\r\nSec-WebSocket-Version: 13\r\n\r\n").encode())
        self.buf, self.n = b"", 0
        while b"\r\n\r\n" not in self.buf:
            self.buf += self.more()
        head, self.buf = self.buf.split(b"\r\n\r\n", 1)
        if b" 101 " not in head.split(b"\r\n")[0]:
            raise ConnectionError("no WebSocket: " + head.split(b"\r\n")[0].decode(errors="replace"))
        self.recv()
        self.send({"type": "auth", "access_token": token})
        if self.recv().get("type") != "auth_ok":
            raise PermissionError("the WebSocket API refused STIMME_HA_TOKEN")

    def more(self) -> bytes:
        b = self.s.recv(65536)
        if not b:
            raise ConnectionError("Home Assistant closed the WebSocket")
        return b

    def take(self, n: int) -> bytes:
        while len(self.buf) < n:
            self.buf += self.more()
        out, self.buf = self.buf[:n], self.buf[n:]
        return out

    def send(self, obj) -> None:
        data, mask = json.dumps(obj).encode(), os.urandom(4)
        n = len(data)
        head = bytes([0x81]) + (bytes([0x80 | n]) if n < 126 else bytes([0xFE]) + struct.pack(">H", n) if n < 65536 else bytes([0xFF]) + struct.pack(">Q", n))
        self.s.sendall(head + mask + bytes(c ^ mask[i % 4] for i, c in enumerate(data)))

    def recv(self) -> dict:
        msg = b""
        while True:
            b0, b1 = self.take(2)
            n = b1 & 0x7F
            n = struct.unpack(">H", self.take(2))[0] if n == 126 else struct.unpack(">Q", self.take(8))[0] if n == 127 else n
            data = self.take(n)
            if b0 & 0x0F == 8:
                raise ConnectionError("Home Assistant closed the WebSocket")
            if b0 & 0x0F in (9, 10):   # ping, pong: a short session answers none
                continue
            msg += data
            if b0 & 0x80:
                return json.loads(msg)

    def __call__(self, type: str, **kw):
        self.n += 1
        self.send(dict(kw, id=self.n, type=type))
        while True:
            m = self.recv()
            if m.get("id") == self.n and m.get("type") == "result":
                if not m.get("success"):
                    raise RuntimeError(f"{type}: {(m.get('error') or {}).get('message', 'refused')}")
                return m.get("result")

    def close(self) -> None:
        self.s.close()


def clean(*groups) -> list:
    """vocabulary.clean_names: in order, whitespace collapsed, each name once (case-insensitive); „Sofa-Licht“
    and „Sofa Licht“, the spellings home-assistant adds for „Sofalicht“ (#760), are the same name to Whisper"""
    out, seen = [], set()
    for g in groups:
        for v in g or ():
            name = " ".join(v.split()) if isinstance(v, str) else ""
            key = name.lower().replace("-", "").replace(" ", "")
            if name and key not in seen:
                seen.add(key)
                out.append(name)
    return out


def fetch(ws) -> list:
    """the exposed names in upstream's four tiers, highest first"""
    try:
        reg = ws("config/entity_registry/list") or []
        friendly = {s.get("entity_id"): (s.get("attributes") or {}).get("friendly_name") for s in ws("get_states") or []}
        exposed = sorted((r for r in reg if not r.get("disabled_by") and ((r.get("options") or {}).get("conversation") or {}).get("should_expose") is True),
                         key=lambda r: r["entity_id"])
        ext = ws("config/entity_registry/get_entries", entity_ids=[r["entity_id"] for r in exposed]) if exposed else {}
        devices = {d.get("id"): d.get("area_id") for d in ws("config/device_registry/list") or []} if exposed else {}
        areas, floors = ws("config/area_registry/list") or [], ws("config/floor_registry/list") or []
    finally:
        ws.close()
    spoken, other, used = [], [], set()
    for r in exposed:
        e = r["entity_id"]
        area = r.get("area_id") or devices.get(r.get("device_id"))
        used.add(area)
        said = [friendly.get(e) or r.get("name") or r.get("original_name") or ""] + [a for a in ((ext or {}).get(e) or {}).get("aliases") or [] if isinstance(a, str)]
        (spoken if e.split(".")[0] in SPOKEN else other).extend(said)
    floors_used = {a.get("floor_id") for a in areas if a.get("area_id") in used and a.get("floor_id")}

    def places(records, key, ids, in_use):
        return [n for r in records if (r.get(key) in ids) == in_use for n in [r.get("name") or ""] + list(r.get("aliases") or [])]
    return clean(spoken), clean(places(areas, "area_id", used, True), places(floors, "floor_id", floors_used, True)), \
        clean(places(areas, "area_id", used, False), places(floors, "floor_id", floors_used, False)), clean(other)


def counter(cache: str = "/config"):
    """tokens of a text: Whisper's own tokenizer from its model cache, else two characters a token"""
    try:
        import glob, tokenizers   # in Whisper's image; not the stdlib, so only here
        tok = tokenizers.Tokenizer.from_file(sorted(glob.glob(os.path.join(cache, "**", "tokenizer.json"), recursive=True))[0])
        return lambda text: len(tok.encode(" " + text).ids)
    except (ImportError, IndexError, OSError, ValueError, RuntimeError):
        return lambda text: len(text) // 2


def prompt(tiers, count=lambda text: len(text) // 2) -> str:
    """vocabulary.RecognitionContext.whisper_prompt: fill in order, stop at the first name that does not fit"""
    chosen = []
    for name in clean(*tiers):
        if count(", ".join(chosen + [name]) + ".") > BUDGET:
            break
        chosen.append(name)
    return ", ".join(chosen) + "." if chosen else ""


def main(path: str, token: str, connect) -> int:
    if not token:
        print("no STIMME_HA_TOKEN (laib apply home-assistant mints it): Whisper keeps the names it has")
        return 0
    try:
        text = prompt(fetch(connect()), counter(os.path.dirname(path)))
    except PermissionError as err:
        print(f"stimme-names: {err}: laib apply home-assistant mints a new one", file=sys.stderr)
        return 1
    except (OSError, RuntimeError, ValueError) as err:   # Home Assistant away or restarting: its own finding
        print(f"Home Assistant does not answer ({type(err).__name__}: {err}): Whisper keeps the names it has")
        return 0
    try:
        with open(path, encoding="utf-8") as f:
            if f.read() == text:
                print(f"names unchanged ({text.count(', ') + 1 if text else 0})")
                return 0
    except FileNotFoundError:
        pass
    with open(path + ".tmp", "w", encoding="utf-8") as f:
        f.write(text)
    os.chmod(path + ".tmp", 0o644)   # Whisper runs as the image's user abc
    os.replace(path + ".tmp", path)
    print(f"names changed ({text.count(', ') + 1 if text else 0}): {text}")
    return 0


if __name__ == "__main__":
    base, tok = os.environ.get("HA_URL", "http://127.0.0.1:8123"), os.environ.get("HA_TOKEN", "")
    raise SystemExit(main(os.environ.get("NAMES_FILE", "/config/laib-names.txt"), tok, lambda: WS(base, tok)))
