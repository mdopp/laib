"""The names as Whisper writes them, as aliases in Home Assistant (#760).

Whisper writes a German compound hyphenated or split: „Schalte Sofa-Licht
aus“ for a light named „Sofalicht“. Home Assistant's own intents match a
name or an alias exactly (spaces and case aside), so the sentence missed
and went to Frage, which cannot switch. This gives every entity exposed to
Assist the other spellings of each compound in its name and its aliases:

  Sofalicht   -> „Sofa-Licht“, „Sofa Licht“
  Sofa-Licht  -> „Sofalicht“, „Sofa Licht“

A compound is a word that ends in one of SUFFIXES, the nouns a household
names its devices with, after at least three letters with a vowel among
them („Schlicht“ is none); a hyphenated one is split at the hyphen when its
second part is such a noun. Nothing else is guessed: two words („Rote
Lampe“) are never joined, a word ending in no listed noun is left alone.

Deterministic and idempotent. The owner's aliases are never removed or
changed; what Laib added is written down in STATE (the config folder's
.laib-aliases.json), so only those are updated or removed — when the name
changes, or the entity is no longer exposed. One the owner deleted is
remembered as declined and not added again.

It runs inside the Home Assistant container (home-assistant-names.service,
`podman exec -i … python3 -`, every 15 minutes) with HA_NAMES_TOKEN, the
user „Laib-Namen“'s (an administrator: config/entity_registry/update is
admin-only), and from post-deploy once per apply. The stdlib only.
"""
import base64
import json
import os
import socket
import struct
import sys
import urllib.parse

# the nouns a device name ends in, singular and plural; longest match wins
SUFFIXES = ("licht", "lichter", "lampe", "lampen", "leuchte", "leuchten", "strahler", "kette", "leiste", "dimmer",
            "steckdose", "steckdosen", "stecker", "schalter", "rollo", "rollos", "rollladen", "jalousie", "markise",
            "heizung", "thermostat", "fenster", "tür", "türen", "ventilator", "lüfter", "radio", "lautsprecher", "fernseher")
VOWELS = set("aeiouäöüy")


def split(word):
    """(prefix, noun) of a compound word, else None"""
    if word.count("-") == 1:
        a, b = word.split("-")
        return (a, b) if a and b.casefold() in SUFFIXES else None
    if not word.isalpha():
        return None
    low = word.casefold()
    for s in sorted(SUFFIXES, key=len, reverse=True):
        if low.endswith(s):   # the longest noun decides: „Ventilator“ is no „Ventila“ plus anything
            head = word[:len(word) - len(s)]
            return (head, word[len(head):]) if len(head) >= 3 and VOWELS & set(head.casefold()) else None
    return None


def forms(name):
    """the other spellings of a name's compounds: joined, hyphenated, split (#760)"""
    words = name.split()
    parts = [split(w) for w in words]
    if not any(parts):
        return []
    def noun(p, sep):   # joined lower case; apart capitalised as German writes it, unless the name is all lower case
        return p[1].casefold() if sep == "" or p[0][:1].islower() else p[1][:1].upper() + p[1][1:]
    out = [" ".join(w if p is None else p[0] + sep + noun(p, sep) for w, p in zip(words, parts)) for sep in ("", "-", " ")]
    return [f for f in out if f.casefold() != name.casefold()]


def plan(entities, state):
    """entities: {entity_id: (name or None when not exposed, aliases as HA has them)}; state: what Laib added
    before. Returns ({entity_id: new aliases}, new state). Owner aliases and HA's own None (the name) stay."""
    changes, new = {}, {}
    for eid in sorted(set(entities) | set(state)):
        name, aliases = entities.get(eid, (None, None))
        rec = state.get(eid) or {}
        added, declined = rec.get("added") or [], rec.get("declined") or []
        if aliases is None:   # the entity is gone from the registry: nothing to change
            continue
        have = [a for a in aliases if isinstance(a, str)]
        declined = sorted(set(declined) | {a for a in added if a not in have})   # the owner deleted it
        owner = [a for a in have if a not in added]
        want = []
        if name:
            taken = {x.casefold() for x in [name] + owner + declined}
            for src in [name] + owner:
                for f in forms(src):
                    if f.casefold() not in taken:
                        taken.add(f.casefold())
                        want.append(f)
        keep = [a for a in aliases if a is None or a in owner or a in want]
        result = keep + [w for w in want if w not in keep]
        if result != aliases:
            changes[eid] = result
        if want or declined:
            new[eid] = {"added": want, "declined": declined}
    return changes, new


class WS:
    """Home Assistant's WebSocket API, one command at a time (stdlib only)"""

    def __init__(self, base, token):
        u = urllib.parse.urlsplit(base)
        self.s = socket.create_connection((u.hostname, u.port or 80), timeout=15)
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
            raise PermissionError("the WebSocket API refused HA_NAMES_TOKEN")

    def more(self):
        b = self.s.recv(65536)
        if not b:
            raise ConnectionError("Home Assistant closed the WebSocket")
        return b

    def take(self, n):
        while len(self.buf) < n:
            self.buf += self.more()
        out, self.buf = self.buf[:n], self.buf[n:]
        return out

    def send(self, obj):
        data, mask = json.dumps(obj).encode(), os.urandom(4)
        n = len(data)
        head = bytes([0x81]) + (bytes([0x80 | n]) if n < 126 else bytes([0xFE]) + struct.pack(">H", n) if n < 65536 else bytes([0xFF]) + struct.pack(">Q", n))
        self.s.sendall(head + mask + bytes(c ^ mask[i % 4] for i, c in enumerate(data)))

    def recv(self):
        msg = b""
        while True:
            b0, b1 = self.take(2)
            n = b1 & 0x7F
            n = struct.unpack(">H", self.take(2))[0] if n == 126 else struct.unpack(">Q", self.take(8))[0] if n == 127 else n
            data = self.take(n)
            if b0 & 0x0F == 8:
                raise ConnectionError("Home Assistant closed the WebSocket")
            if b0 & 0x0F in (9, 10):
                continue
            msg += data
            if b0 & 0x80:
                return json.loads(msg)

    def __call__(self, type, **kw):
        self.n += 1
        self.send(dict(kw, id=self.n, type=type))
        while True:
            m = self.recv()
            if m.get("id") == self.n and m.get("type") == "result":
                if not m.get("success"):
                    raise RuntimeError(f"{type}: {(m.get('error') or {}).get('message', 'refused')}")
                return m.get("result")

    def close(self):
        self.s.close()


def run(ws, path):
    """read, plan, write: the aliases first, then STATE, so a run that stops half way repeats"""
    try:
        with open(path, encoding="utf-8") as f:
            state = json.load(f).get("entities") or {}
    except FileNotFoundError:
        state = {}
    reg = ws("config/entity_registry/list") or []
    friendly = {s.get("entity_id"): (s.get("attributes") or {}).get("friendly_name") for s in ws("get_states") or []}
    exposed = {r["entity_id"] for r in reg if not r.get("disabled_by")
               and ((r.get("options") or {}).get("conversation") or {}).get("should_expose") is True}
    ids = sorted(exposed | ({r["entity_id"] for r in reg} & set(state)))
    ext = ws("config/entity_registry/get_entries", entity_ids=ids) if ids else {}
    entities = {e: ((friendly.get(e) or (ext.get(e) or {}).get("name") or (ext.get(e) or {}).get("original_name")) if e in exposed else None,
                    (ext.get(e) or {}).get("aliases") or []) for e in ids if ext.get(e)}
    changes, new = plan(entities, state)
    for eid, aliases in changes.items():
        ws("config/entity_registry/update", entity_id=eid, aliases=aliases)
        print(f"names: {eid}: " + ", ".join(f"„{a}“" for a in aliases if isinstance(a, str)))
    if new != state:
        with open(path + ".tmp", "w", encoding="utf-8") as f:
            json.dump({"version": 1, "entities": new}, f, ensure_ascii=False, indent=1)
        os.replace(path + ".tmp", path)
    n = sum(len(v["added"]) for v in new.values())
    print(f"names: {len(changes)} entities changed, {n} spellings Laib added in all" if changes else f"names: as set ({n} spellings Laib added)")


def main(path, token, connect):
    if not token:
        print("names: no HA_NAMES_TOKEN yet (laib apply home-assistant mints it): no spellings added")
        return 0
    try:
        ws = connect()
    except PermissionError as err:
        print(f"names: {err}: laib apply home-assistant mints a new one", file=sys.stderr)
        return 1
    except OSError as err:   # Home Assistant away or restarting: the next run adds them
        print(f"names: Home Assistant does not answer ({type(err).__name__}): no change")
        return 0
    try:
        run(ws, path)
    except (OSError, RuntimeError, ValueError) as err:
        print(f"names: NOT set: {type(err).__name__}: {err}", file=sys.stderr)
        return 1
    finally:
        ws.close()
    return 0


if __name__ == "__main__":
    base, tok = os.environ.get("HA_URL", "http://127.0.0.1:8123"), os.environ.get("HA_TOKEN", "")
    raise SystemExit(main(os.environ.get("NAMES_STATE", "/config/.laib-aliases.json"), tok, lambda: WS(base, tok)))
