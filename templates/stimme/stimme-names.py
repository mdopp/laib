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

It also looks whether the satellites run the house persona's wake word (#759,
wake): once the trainer is done, it reads the satellites' wake-word selects
(HA's translation key wake_word) and writes Stimme's status: „trainiert, noch
nicht auf dem Gerät“ with the repair that flashes it (`laib flash esphome`, a
human approves it in the Inbox) until a device offers the phrase, then „auf dem
Gerät“. A device that offers it for the first time gets it selected, once; a
later choice by hand stays (home-assistant's post-deploy selects it on its next
run too). No satellite, or one Home Assistant does not reach, is no
measurement: the status says so and closes no Box-Aufgabe.

A device that offers the phrase runs the model as it was flashed (#825): esphome's flash
unit leaves <id>.geflasht.json, the manifest it compiled in, beside the trained one. When
the trained manifest's settings (WAKE_CUTOFF, the next training) differ from it, or a
device was flashed before Laib kept that copy, the status offers the same flash again.
And it carries the numbers: stimme-wakes counts the wakes per day in laib-wakes.json, the
detail line says „heute 2× geweckt, 2 ohne Befehl“, einzelheiten the last days and how many
false wakes wait as negatives for the next training. Days are the box's (TZ).

And the household's recordings (#828): Haus writes the family saying the phrase into
/recordings/<id>/haus as <person>-<YYYYMMDD-HHMMSS>.wav (through a .<name>.part dotfile), the
false wakes lie under negativ/. The detail counts them per person, and what is new since the
model's run began (the trainer's „begonnen“, else the model file's time); a clip the trainer
would not take (16 kHz mono 16-bit, 0.25–4 s) is no new one. With NEU new positives or new
negatives the status offers „neu trainieren“ first: `laib retrain stimme`, which the owner
approves in the Inbox; it moves the model aside, the trainer trains, and once the model is
back the flash above follows. Only counts are printed, never a name.
"""
import base64
import calendar
import json
import os
import pathlib
import re
import socket
import struct
import sys
import time
import urllib.parse
import wave

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


FLASH = ["flash", "esphome"]   # the builtin esphome template's flash unit; laib repair offers it only where there is one


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
    neg = int(c.get("gegenbeispiele") or 0)
    lines.append(f"{neg} Fehlalarme als Gegenbeispiele für das nächste Training gesammelt" if neg else "noch kein Fehlalarm als Gegenbeispiel gesammelt")
    return "heute " + day(tage.get(today) or {}), "\n".join(lines)


NEGATIVES = "negativ"   # recordings/<id>/negativ: stimme-wakes' false wakes (#825)
PERSON = re.compile(r"^(.+)-\d{8}-\d{6}$")   # Haus: <person>-<YYYYMMDD-HHMMSS>.wav (#828)
NEU = (5, 50)          # new positives, new negatives since the run began that offer a retrain (#828)
RETRAIN = ["retrain", "stimme"]


def usable(wav) -> bool:
    """what the trainer's reject_reason takes by its header: 16 kHz mono 16-bit, 0.25–4 s"""
    try:
        with wave.open(str(wav), "rb") as w:
            return (w.getnchannels(), w.getsampwidth(), w.getframerate()) == (1, 2, 16000) and 4000 <= w.getnframes() <= 64000
    except (OSError, EOFError, wave.Error):
        return False


def recordings(root, since: float) -> dict:
    """#828: the recordings under root (recordings/<id>): positives per person, the false wakes, the new ones"""
    c = {"person": {}, "weitere": 0, "negativ": 0, "neu": 0, "neu_negativ": 0, "unbrauchbar": 0}
    for wav in sorted(root.rglob("*.wav")) if root.is_dir() else []:
        try:
            new = wav.stat().st_mtime > since
        except OSError:
            continue
        if wav.name.startswith("."):   # Haus' temp file
            continue
        if NEGATIVES in wav.relative_to(root).parts:
            c["negativ"] += 1
            c["neu_negativ"] += new
            continue
        m = PERSON.match(wav.stem) if wav.parent.name == "haus" else None
        if m:
            c["person"][m.group(1)] = c["person"].get(m.group(1), 0) + 1
        else:
            c["weitere"] += 1
        if not usable(wav):
            c["unbrauchbar"] += 1
        elif new:
            c["neu"] += 1
    return c


def plural(n: int, one: str, many: str) -> str:
    return f"{n} {one if n == 1 else many}"


def since(status: dict, model) -> float:
    """when the model's run began: the trainer's „begonnen“, else the model file's time"""
    try:
        return calendar.timegm(time.strptime(status["begonnen"], "%Y-%m-%dT%H:%M:%SZ"))
    except (KeyError, TypeError, ValueError):
        try:
            return model.stat().st_mtime
        except OSError:
            return 0.0


def counted_recordings(c: dict, phrase: str) -> tuple:
    """(the detail's clause, einzelheiten's line, the retrain finding or None)"""
    pos = sum(c["person"].values()) + c["weitere"]
    if not pos and not c["negativ"]:
        return "", "", None
    who = [f"{p} {n}" for p, n in sorted(c["person"].items())] + ([f"{c['weitere']} weitere"] if c["weitere"] else [])
    clause = "Aufnahmen: " + ", ".join(who + [plural(c["negativ"], "Fehlalarm", "Fehlalarme")])
    new = [plural(c["neu"], "neue Aufnahme", "neue Aufnahmen")] + ([plural(c["neu_negativ"], "neuer Fehlalarm", "neue Fehlalarme")] if c["neu_negativ"] else [])
    clause += "; seit dem Training " + ", ".join(new)
    line = f"{pos} eigene Aufnahmen, {c['negativ']} Fehlalarme" + (f", {c['unbrauchbar']} unbrauchbar (nicht 16 kHz mono, 16 Bit, 0,25–4 s)" if c["unbrauchbar"] else "")
    if c["neu"] < NEU[0] and c["neu_negativ"] < NEU[1]:
        return clause, line, None
    return clause, line, {
        "severity": "gelb", "topic": "training", "repair": RETRAIN,
        "detail": f"{' und '.join(new)} von „{phrase}“ seit dem letzten Training",
        "fix": f"laib repair status stimme: nach deiner Freigabe in der Inbox trainiert Laib „{phrase}“ neu",
        "does": f"„{phrase}“ wird mit euren Aufnahmen neu trainiert",
        "effect": f"Laib legt das jetzige Modell beiseite und trainiert „{phrase}“ neu, mit {pos} eigenen Aufnahmen und {c['negativ']} "
                  f"Fehlalarmen; das dauert einige Stunden. Danach fragt Laib, ob es auf den Lautsprecher soll; bis dahin hört er mit dem alten.",
        "undo": "Das bisherige Modell bleibt als Kopie liegen (stimme/micro/vorher), der Lautsprecher behält es, bis du das neue aufspielen lässt."}


def wake(connect, micro, wid: str, phrase: str, recorded=pathlib.Path("/recordings")) -> None:
    """#759: Stimme's status (micro/laib-status.json) once the trainer is done: is the phrase on the satellites?"""
    def read(name):
        try:
            return json.loads((micro / name).read_text())
        except (OSError, ValueError):
            return {}
    done, before = read(f"{wid}.status.json").get("state") == "done" and (micro / f"{wid}.tflite").exists(), read("laib-status.json")
    if not done and read(f"{wid}.status.json").get("state") == "done":   # #828: moved aside, the trainer has not begun yet
        moved = {"at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()), "detail": f"Wake-Word „{phrase}“ wird neu trainiert, sobald der Trainer läuft",
                 "einzelheiten": "", "findings": [], "phrase": phrase}
        tmp = micro / "laib-status.json.tmp"
        tmp.write_text(json.dumps(moved, ensure_ascii=False) + "\n")
        tmp.replace(micro / "laib-status.json")
    if not done:
        print(f"wake word {wid}: not trained (yet), the trainer's status stays")
        return
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
                    if eid not in had and st.get("state") != hit:   # first offered: the persona's word, once
                        ws("call_service", domain="select", service="select_option", service_data={"option": hit}, target={"entity_id": eid})
                        print(f"wake word: {eid} {st.get('state')} -> {hit}")
        finally:
            ws.close()
    except (OSError, RuntimeError, ValueError) as err:
        print(f"wake word: Home Assistant does not answer ({type(err).__name__}: {err}), the last look stays")
        return
    trained = f"Wake-Word „{phrase}“ trainiert, noch nicht auf dem Gerät"
    c = recordings(recorded / wid, since(read(f"{wid}.status.json"), micro / f"{wid}.tflite"))
    rclause, rline, retrain = counted_recordings(c, phrase)
    findings = [retrain] if retrain else []   # first: laib repair status stimme files the first repair, and a retrain's flash follows
    print(f"recordings: {sum(c['person'].values()) + c['weitere']} positives, {c['negativ']} negatives, {c['neu']} and {c['neu_negativ']} new")
    flashed, want = read(f"{wid}.geflasht.json").get("micro"), read(f"{wid}.json").get("micro")
    stale = on and not off and want and flashed != want   # #825: on the device, but not as trained now
    if stale:
        was = f"Schwelle {flashed.get('probability_cutoff')}" if isinstance(flashed, dict) else "ein älterer Stand"
        findings.append({"severity": "gelb", "topic": "wakeword", "repair": FLASH,
                         "detail": f"Der Lautsprecher „{', '.join(n for _, n in on)}“ hört noch mit der alten Einstellung auf „{phrase}“ ({was}, "
                                   f"trainiert ist Schwelle {want.get('probability_cutoff')})",
                         "fix": "laib repair status stimme: nach deiner Freigabe in der Inbox spielt Laib es neu auf",
                         "does": f"„{phrase}“ wird mit der neuen Einstellung auf den Lautsprecher gespielt",
                         "effect": f"Laib baut die Firmware des Lautsprechers neu und spielt sie auf: er startet dabei neu und ist etwa 2 Minuten "
                                   f"still, danach wacht er seltener auf, wenn niemand „{phrase}“ gesagt hat.",
                         "undo": "Zurück nur mit einem neuen Aufspielen in ESPHome; seine YAML von vorher liegt daneben (….yaml.laib-vorher)."})
    if off:
        findings.append({"severity": "gelb", "detail": f"{trained} „{', '.join(off)}“", "topic": "wakeword", "repair": FLASH,
                         "fix": "laib repair status stimme: nach deiner Freigabe in der Inbox spielt Laib es auf",
                         "does": f"„{phrase}“ wird auf den Lautsprecher gespielt",
                         "effect": f"Laib baut die Firmware des Lautsprechers mit „{phrase}“ neu und spielt sie auf: er startet dabei neu "
                                   f"und ist etwa 2 Minuten still, danach hört er auf „{phrase}“.",
                         "undo": "Zurück nur mit einem neuen Aufspielen in ESPHome; seine YAML von vorher liegt daneben (….yaml.laib-vorher)."})
    if away or not (on or off):
        findings.append({"severity": "info", "topic": "wakeword", "unmeasured": True, "fix": "Lautsprecher einschalten, in Home Assistant einbinden",
                         "detail": f"„{', '.join(away)}“ antwortet Home Assistant nicht" if away else "kein Gerät mit Wake-Word in Home Assistant"})
    detail = f"Wake-Word „{phrase}“ auf dem Gerät „{', '.join(n for _, n in on)}“" if on and not off else trained
    clause, einzelheiten = counted(micro)
    detail += f"; {clause}" if clause and on else ""
    said = detail   # the journal's line: the recordings' clause names people, it stays in the status
    detail += f"; {rclause}" if rclause else ""
    einzelheiten = "\n".join(x for x in (einzelheiten if on else "", rline) if x)
    status = {"at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()), "detail": detail, "einzelheiten": einzelheiten, "findings": findings,
              "phrase": phrase, "geraet": bool(on) and not [f for f in findings if f is not retrain], "auf": [e for e, _ in on]}
    tmp = micro / "laib-status.json.tmp"
    tmp.write_text(json.dumps(status, ensure_ascii=False) + "\n")
    tmp.replace(micro / "laib-status.json")
    print(f"wake word: {said}")


if __name__ == "__main__":
    base, tok = os.environ.get("HA_URL", "http://127.0.0.1:8123"), os.environ.get("HA_TOKEN", "")
    code = main(os.environ.get("NAMES_FILE", "/config/laib-names.txt"), tok, lambda: WS(base, tok))
    if tok and os.environ.get("WAKE_ID") and os.path.isdir("/micro"):
        wake(lambda: WS(base, tok), pathlib.Path("/micro"), os.environ["WAKE_ID"], os.environ.get("WAKE_PHRASE", ""))
    raise SystemExit(code)
