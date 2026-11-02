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

In front go the verbs (#961): „Schalte das Licht an, Schalte es aus“ („Schalte Bambi an“ came back as
„Schade van Bion.“ with Bambi in the prompt), then the house's stations („Spiele Einslive, …, Radio Bob,
Spiele Musik von“). The stations come from the list home-assistant's
pre-start renders for Assist's LaibRadio (#933), handed in as LAIB_SENDER, so Whisper writes a form Assist
matches. The verbs are written first but filled after the named things, so a full budget cuts them, never a
device. No station list (no Home Assistant, no stations on the share): the switching words and the names.

It runs inside the Whisper container (stimme-names.service, `podman exec -i …
python3 -`), the stdlib only, and prints "names changed" only when the file
changed: the unit restarts Whisper then, and only then. Home Assistant away
keeps the last names; a refused token fails the unit.

It also writes Stimme's status (wake): the wake word is „Hey Mycroft“, a model the Voice PE's
firmware and openWakeWord carry from the factory, so nothing is trained or flashed. It reads the
satellites' wake-word selects (HA's translation key wake_word): a device that offers the phrase
for the first time gets it selected, once; a later choice by hand stays (home-assistant's
post-deploy selects it on its next run too). A device that does not offer it is named, with
what to do. No satellite, or one Home Assistant does not reach, is no measurement: the status
says so and closes no Box-Aufgabe. And it carries the numbers: stimme-wakes counts the wakes
per day in laib-wakes.json, the detail line says „heute 2× geweckt, 2 ohne Befehl“,
einzelheiten the last days. Days are the box's (TZ).
"""
import base64
import json
import os
import pathlib
import re
import socket
import struct
import sys
import time
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


def stations(rendered: str) -> list:
    """#961: the house's stations, one spoken form each, from the list home-assistant's pre-start renders for
    Assist's LaibRadio (custom_sentences/de/laib_medien.yaml, #933), so Whisper writes what Assist matches.
    Its forms are the name, digits as words apart, and for a name without a space joined last („(1LIVE|eins
    LIVE|Einslive)“): a leading number is said with the word (Einslive), a trailing one apart (SWR3: SWR
    drei, not Swrdrei). A shouted name is said as a name (RADIO BOB: Radio Bob; NDR stays). No list, no station."""
    try:
        values = json.loads(rendered.split("\n", 1)[1] if rendered.startswith("#") else rendered)["lists"]["laib_sender"]["values"]
    except (ValueError, KeyError, TypeError, IndexError):
        return []
    out = []
    for v in values if isinstance(values, list) else []:
        forms = [f.strip() for f in str((v or {}).get("in") or "").strip("()").split("|") if f.strip()] if isinstance(v, dict) else []
        words = [f for f in forms if not re.search(r"\d", f)]
        said = (words[-1] if forms[0][:1].isdigit() else words[0]) if words else forms[0] if forms else ""
        alpha = [w for w in said.split() if w.isalpha()]
        shouted = alpha and all(w.isupper() for w in alpha) and any(len(w) > 3 for w in alpha)
        out.append(" ".join(w.capitalize() if shouted and w.isalpha() else w for w in said.split()))
    return clean(out)


SWITCH = ["Schalte das Licht an", "Schalte es aus"]   # #961: the verb Assist's most common sentence starts with


def frame(said: list) -> list:
    """#961: the verbs in front of the names. First the switching words: „Schalte Bambi an“ came back as „Schade
    van Bion.“ with Bambi in the prompt, but no „Schalte“; generic, not a device's name twice, 16 tokens on the box.
    Then the stations and the music sentence („Spiele Einslive, …, Radio Bob, Spiele Musik von“), the verb once,
    not before each station: on the box „Spiele“ is two tokens, nine stations with it did not leave room for the
    music sentence. Without a station no „Spiele“"""
    return SWITCH + ([f"Spiele {said[0]}"] + said[1:] + ["Spiele Musik von"] if said else [])


def prompt(tiers, count=lambda text: len(text) // 2, first=()) -> str:
    """vocabulary.RecognitionContext.whisper_prompt: fill in order, stop at the first name that does not fit.
    `first` (the stations' frame) is written in front but filled after the named things (#961): a full budget
    cuts stations, never a device's name"""
    if not any(clean(*tiers)):
        return ""   # nothing exposed: no frame around nothing
    head, chosen, lead = [], [], set(clean(first))
    for name in clean(*tiers[:1], first, *tiers[1:]):
        into = head if name in lead else chosen
        into.append(name)
        if count(", ".join(head + chosen) + ".") > BUDGET:
            into.pop()
            break
    return ", ".join(head + chosen) + "." if head or chosen else ""


def main(path: str, token: str, connect, sender: str = "") -> int:
    if not token:
        print("no STIMME_HA_TOKEN (laib apply home-assistant mints it): Whisper keeps the names it has")
        return 0
    try:
        text = prompt(fetch(connect()), counter(os.path.dirname(path)), frame(stations(sender)))
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


def counted(micro) -> tuple:
    """#825: (the detail's clause, einzelheiten) from stimme-wakes' counts; nothing without them"""
    try:
        c = json.loads((micro / "laib-wakes.json").read_text())
        tage = c.get("tage") or {}
    except (OSError, ValueError, AttributeError):
        return "", ""
    day = lambda t: f"{t.get('geweckt', 0)}× geweckt, {t.get('ohne', 0)} ohne Befehl" if t.get("geweckt") else "nicht geweckt"
    today = time.strftime("%Y-%m-%d")
    lines = [f"{d[8:10]}.{d[5:7]}.: {day(tage[d])}" for d in sorted(tage, reverse=True)[:7]]
    return "heute " + day(tage.get(today) or {}), "\n".join(lines)


def wake(connect, micro, phrase: str) -> None:
    """Stimme's status (micro/laib-status.json): which satellites listen for the phrase?"""
    try:
        before = json.loads((micro / "laib-status.json").read_text())
    except (OSError, ValueError):
        before = {}
    had = before.get("auf", []) if before.get("phrase") == phrase else []
    try:
        ws = connect()
        try:
            reg = ws("config/entity_registry/list") or []
            states = {s.get("entity_id"): s for s in ws("get_states") or []}
            on, off, away = [], [], []
            for r in reg:
                eid, st = r.get("entity_id", ""), states.get(r.get("entity_id")) or {}
                if r.get("translation_key") != "wake_word" or r.get("disabled_by") or not eid.startswith("select."):
                    continue
                opts = (st.get("attributes") or {}).get("options") or []
                name = ((st.get("attributes") or {}).get("friendly_name") or eid).removesuffix(" Aktivierungswort").removesuffix(" Wake word")
                hit = next((o for o in opts if o.casefold() == phrase.casefold()), None)
                if st.get("state") in (None, "unavailable", "unknown") or not opts:
                    away.append(name)
                elif not hit:
                    off.append(name)
                else:
                    on.append((eid, name))
                    if eid not in had and st.get("state") != hit:   # first offered: the house's word, once
                        ws("call_service", domain="select", service="select_option", service_data={"option": hit}, target={"entity_id": eid})
                        print(f"wake word: {eid} {st.get('state')} -> {hit}")
        finally:
            ws.close()
    except (OSError, RuntimeError, ValueError) as err:
        print(f"wake word: Home Assistant does not answer ({type(err).__name__}: {err}), the last look stays")
        return
    findings = []
    if off:
        findings.append({"severity": "gelb", "topic": "wakeword",
                         "detail": f"Der Lautsprecher „{', '.join(off)}“ kennt „{phrase}“ nicht",
                         "fix": f"In ESPHome bei diesem Gerät unter micro_wake_word das Modell hey_mycroft eintragen und neu aufspielen, "
                                f"oder einen Lautsprecher nehmen, der „{phrase}“ anbietet (wie der Voice PE)"})
    if away or not (on or off):
        findings.append({"severity": "info", "topic": "wakeword", "unmeasured": True, "fix": "Lautsprecher einschalten, in Home Assistant einbinden",
                         "detail": f"„{', '.join(away)}“ antwortet Home Assistant nicht" if away else "kein Gerät mit Wake-Word in Home Assistant"})
    detail = f"Wake-Word „{phrase}“ auf dem Gerät „{', '.join(n for _, n in on)}“" if on else f"Wake-Word „{phrase}“ noch auf keinem Gerät"
    clause, einzelheiten = counted(micro)
    detail += f"; {clause}" if clause and on else ""
    status = {"at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()), "detail": detail, "einzelheiten": einzelheiten if on else "",
              "findings": findings, "phrase": phrase, "geraet": bool(on) and not off and not away, "auf": [e for e, _ in on]}
    tmp = micro / "laib-status.json.tmp"
    tmp.write_text(json.dumps(status, ensure_ascii=False) + "\n")
    tmp.replace(micro / "laib-status.json")
    print(f"wake word: {detail}")


if __name__ == "__main__":
    base, tok = os.environ.get("HA_URL", "http://127.0.0.1:8123"), os.environ.get("HA_TOKEN", "")
    code = main(os.environ.get("NAMES_FILE", "/config/laib-names.txt"), tok, lambda: WS(base, tok), os.environ.get("LAIB_SENDER", ""))
    if tok and os.environ.get("WAKE_PHRASE") and os.path.isdir("/micro"):
        wake(lambda: WS(base, tok), pathlib.Path("/micro"), os.environ["WAKE_PHRASE"])
    raise SystemExit(code)
