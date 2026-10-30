"""Home Assistant's integrations, as laib's status file (#55, #309).

Most of Home Assistant's integrations are set up by hand once (README.md);
with MA_TOKEN it also asks Music Assistant whether it is usable (#526). With the
long-lived token post-deploy mints into HA_TOKEN (#349) this reads HA's config entries and
names what is still missing, the checklist the Box page and `laib diag`
show ("Wyoming Whisper: fehlt"). It runs inside the Home Assistant
container (home-assistant-check.service, `podman exec -i … python3 -`):
laib starts it when someone looks and the file is older than five minutes,
the timer once an hour so the morning report reads a fresh one.

It writes /config/.laib-status.json:
{"at": RFC3339, "detail": "…", "findings": [{"severity", "detail", "fix"}]}
"""
import base64
import json
import os
import socket
import struct
import time
import urllib.parse
import urllib.request

# what ADR 0015 wires into HA: a domain and a word of the entry's title
# (Wyoming has three entries of one domain)
EXPECTED = [
    ("Wyoming Whisper", "wyoming", "whisper"),
    ("Wyoming Piper", "wyoming", "piper"),
    ("Wyoming openWakeWord", "wyoming", "wake"),
    ("CalDAV", "caldav", ""),
    ("Music Assistant", "music_assistant", ""),
    ("MQTT", "mqtt", ""),
    ("MCP Server", "mcp_server", ""),
    ("Frage als Gesprächsagent", "ollama", ":8710"),  # post-deploy adds it once Frage answers (#491)
]
FIX = "in Home Assistant unter Einstellungen → Integrationen einrichten (templates/home-assistant/README.md)"
STIMME_FIX = "laib add stimme, dann laib apply home-assistant: die Wyoming-Einträge kommen von selbst (#540)"
FIXES = {"Frage als Gesprächsagent": "laib add frage, dann laib apply home-assistant: die Ollama-Integration auf Frage (#491)",
         "Wyoming Whisper": STIMME_FIX, "Wyoming Piper": STIMME_FIX, "Wyoming openWakeWord": STIMME_FIX}
# an entry Home Assistant could not set up: it counts for no row. Of the voice's domains it is a
# finding of its own, the server behind it is gone (#540)
BROKEN = ("setup_error", "setup_retry", "migration_error", "failed_unload")
VOICE = ("wyoming", "ollama")


def entries(base: str, token: str) -> list:
    req = urllib.request.Request(base.rstrip("/") + "/api/config/config_entries/entry",
                                 headers={"Authorization": "Bearer " + token})
    with urllib.request.urlopen(req, timeout=8) as resp:
        return json.load(resp)


def report(token: str, base: str) -> dict:
    if not token:
        return {"detail": "", "findings": []}  # no token, no checklist (as before #309)
    try:
        got = entries(base, token)
    except Exception as err:  # an unreachable HA is a finding, never a failed unit
        return {"detail": "Integrationsliste nicht lesbar", "findings": [{
            "severity": "gelb", "detail": f"Home Assistant antwortet nicht auf die Integrationsliste: {err}",
            "fix": "laib apply home-assistant (ersetzt einen HA_TOKEN, den Home Assistant ablehnt, #349)"}]}
    missing = [name for name, domain, title in EXPECTED
               if not any(e.get("domain") == domain and e.get("state") not in BROKEN and (not title or title in (e.get("title") or "").lower())
                          for e in got)]
    dead = [e for e in got if e.get("domain") in VOICE and e.get("state") in BROKEN]
    findings = [{"severity": "gelb", "detail": f"{name}: fehlt", "fix": FIXES.get(name, FIX)} for name in missing]
    findings += [{"severity": "gelb", "detail": f"„{e.get('title')}“ ({e.get('domain')}) antwortet nicht",
                  "fix": "der Dienst dahinter läuft nicht: starten, oder den Eintrag in Home Assistant löschen (Einstellungen → "
                         "Geräte & Dienste)"} for e in dead]
    detail = ["fehlt: " + ", ".join(missing)] if missing else []
    detail += [f"{len(dead)} Sprach-Integration{'en' if len(dead) > 1 else ''} ohne Dienst"] if dead else []
    return {"detail": " · ".join(detail) or f"alle {len(EXPECTED)} Integrationen eingerichtet", "findings": findings}


def offers(base: str, token: str, service: str) -> list:
    """#528: Home Assistant's update entities that have something newer —
    HACS and the custom integrations, the devices' firmware. Each is an info
    finding ("Update: …" / "Firmware: …", laib-notify's weekly "Was ist
    veraltet" picks them up by these words); the installable extensions are
    also one finding whose fix is a single value proposal, HA_INSTALL, which
    the Operator's weekly aktualisierung files as it stands and post-deploy
    installs once a person approved it. Firmware is only reported: a failed
    flash can brick a device."""
    if not token:
        return []
    try:
        req = urllib.request.Request(base.rstrip("/") + "/api/states", headers={"Authorization": "Bearer " + token})
        with urllib.request.urlopen(req, timeout=8) as resp:
            states = json.load(resp)
    except Exception:  # the integrations finding above says HA does not answer
        return []
    out, install = [], []
    for s in states:
        a = s.get("attributes") or {}
        old, new = a.get("installed_version"), a.get("latest_version")
        if not str(s.get("entity_id", "")).startswith("update.") or s.get("state") != "on" or not old or not new:
            continue
        name = a.get("title") or a.get("friendly_name") or s["entity_id"]
        if a.get("device_class") == "firmware":
            out.append({"severity": "info", "detail": f"Firmware: {name} {old} → {new} (nur gemeldet)",
                        "fix": "einspielen nur auf ein ausdrückliches Ja: Home Assistant → Einstellungen → Updates, wenn das Gerät gerade entbehrlich ist"})
            continue
        notes = f" · {a['release_url']}" if a.get("release_url") else ""
        out.append({"severity": "info", "detail": f"Update: {name} {old} → {new}{notes}", "fix": "der Operator schlägt es samstags vor (HA_INSTALL)"})
        if int(a.get("supported_features") or 0) & 1 and not any(c in s["entity_id"] + new for c in " ,="):
            install.append((f"{s['entity_id']}={new}", f"{name} {new}{notes}"))
    if install:
        out.append({"severity": "info", "detail": "Erweiterungen installieren: " + "; ".join(w for _, w in install),
                    "fix": f"laib propose --set services/{service}/values.yml:HA_INSTALL=" + ",".join(v for v, _ in install)})
    return out


def unpacked(config: str) -> list:
    """#528: every custom integration where Home Assistant loads it, custom_components/<domain>/manifest.json.
    HACS 2.0.5 unpacked one a level too deep (custom_components/senec/senec/): HA said "Integration not
    found" and nothing else did. post-deploy straightens one whose manifest lies one level down."""
    cc = os.path.join(config, "custom_components")
    try:
        names = sorted(n for n in os.listdir(cc) if not n.startswith((".", "_")) and os.path.isdir(os.path.join(cc, n)))
    except OSError:
        return []
    out = []
    for n in names:
        if os.path.isfile(os.path.join(cc, n, "manifest.json")):
            continue
        deep = any(os.path.isfile(os.path.join(cc, n, *sub, "manifest.json")) for sub in ([n], ["custom_components", n]))
        out.append({"severity": "gelb",
                    "detail": f"Erweiterung {n}: Home Assistant lädt sie nicht, " + ("sie liegt eine Ebene zu tief" if deep else "ihr fehlt die manifest.json"),
                    "fix": "laib apply home-assistant: zieht sie gerade und startet Home Assistant neu" if deep
                           else "in HACS neu herunterladen, dann laib apply home-assistant"})
    return out


def zwave(url: str) -> dict:
    """#417: zwave-js-ui's own health of its driver: 200 once the stick's
    controller is ready. Without it every Z-Wave device is unavailable while
    Home Assistant itself answers 200."""
    try:
        with urllib.request.urlopen(url.rstrip("/") + "/health/zwave", timeout=8) as resp:
            if resp.status == 200:
                return {}
            why = f"HTTP {resp.status}"
    except Exception as err:
        why = str(err)
    return {"severity": "rot", "detail": f"Z-Wave: kein Controller, alle Z-Wave-Geräte sind nicht erreichbar ({why})",
            "fix": "journalctl --user -u home-assistant-zwave: der Stick (ZWAVE_DEVICE) und SELinux (SecurityLabelDisable, #417)"}


def music(url: str, token: str) -> tuple:
    """#526: Music Assistant is set up, has Jellyfin's titles and knows players. With the
    token music-assistant's post-deploy minted for Home Assistant (MA's JSON-RPC API);
    without one, no Music Assistant to look at. (detail, findings)"""
    if not token:
        return "", []

    def cmd(command: str, **args):
        req = urllib.request.Request(url.rstrip("/") + "/api", data=json.dumps({"message_id": "check", "command": command, "args": args}).encode(),
                                     headers={"Authorization": "Bearer " + token, "Content-Type": "application/json"}, method="POST")
        with urllib.request.urlopen(req, timeout=8) as resp:
            return json.load(resp)

    again = "laib apply music-assistant (#526)"
    try:
        with urllib.request.urlopen(url.rstrip("/") + "/info", timeout=8) as resp:
            info = json.load(resp)
        loaded = lambda domain: any(p.get("status") == "loaded" for p in cmd("config/providers", provider_domain=domain))
        jellyfin, hass, tracks, players = loaded("jellyfin"), loaded("hass"), cmd("music/tracks/count"), cmd("players/all")
    except Exception as err:  # an unreachable MA is a finding, never a failed unit
        return "Music Assistant antwortet nicht", [{"severity": "gelb", "detail": f"Music Assistant antwortet nicht: {err}", "fix": again}]
    out = []
    if not info.get("onboard_done"):
        out.append(("Music Assistant: nicht eingerichtet", again))
    if not jellyfin:
        out.append(("Music Assistant: keine Jellyfin-Quelle", again + ": das Jellyfin-Konto musicassistant"))
    elif not tracks:
        out.append(("Music Assistant: keine Titel aus Jellyfin",
                    "Musik in den Ordner music der Dateifreigabe legen; Music Assistant übernimmt Jellyfins Bibliothek beim nächsten Abgleich"))
    if not players:
        out.append(("Music Assistant: kennt keine Lautsprecher",
                    "Lautsprecher einschalten: Chromecast, AirPlay und DLNA im Heimnetz findet Music Assistant selbst"))
    if not hass:
        out.append(("Music Assistant: Anmeldung über Home Assistant fehlt", "laib apply home-assistant (#526)"))
    if out:
        return "Music Assistant unvollständig", [{"severity": "gelb", "detail": d, "fix": f} for d, f in out]
    return f"Music Assistant: {tracks} Titel, {len(players)} Lautsprecher", []


class WS:
    """Home Assistant's WebSocket API with the check's token, one command at a time (stdlib only)"""

    def __init__(self, base: str, token: str):
        u = urllib.parse.urlsplit(base)
        self.s = socket.create_connection((u.hostname, u.port or 80), timeout=8)
        self.s.sendall((f"GET /api/websocket HTTP/1.1\r\nHost: {u.netloc}\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"
                        f"Sec-WebSocket-Key: {base64.b64encode(os.urandom(16)).decode()}\r\nSec-WebSocket-Version: 13\r\n\r\n").encode())
        self.buf = b""
        while b"\r\n\r\n" not in self.buf:
            self.buf += self.more()
        head, self.buf = self.buf.split(b"\r\n\r\n", 1)
        if b" 101 " not in head.split(b"\r\n")[0]:
            raise ConnectionError("no WebSocket: " + head.split(b"\r\n")[0].decode(errors="replace"))
        self.recv()
        self.send({"type": "auth", "access_token": token})
        if self.recv().get("type") != "auth_ok":
            raise ConnectionError("the WebSocket API refused HA_TOKEN")
        self.n = 0

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

    def send(self, obj, opcode: int = 1) -> None:
        data, mask = (json.dumps(obj).encode() if opcode == 1 else obj), os.urandom(4)
        n = len(data)
        head = bytes([0x80 | opcode]) + (bytes([0x80 | n]) if n < 126 else bytes([0xFE]) + struct.pack(">H", n) if n < 65536
                                         else bytes([0xFF]) + struct.pack(">Q", n))
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
            if b0 & 0x0F == 9:
                self.send(data, opcode=10)  # pong
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


# a sentence HA's built-in intents know on every box, that needs no device and switches nothing
PROBE = {"de": "Wie spät ist es", "en": "What time is it"}
APPLY = "laib apply home-assistant (#634)"
# ADR 0015's five intents (#864) as text, recognized and never executed: (what, sentence, intent). The media
# ones are Laib's own sentences (pre-start, #933); None: no local intent may take it, so it reaches Frage.
# {name} is a light or switch exposed to Assist. What this does not measure: the microphone, Whisper, the music.
FIVE = [("Timer", "Stelle einen Timer auf 5 Minuten", "HassStartTimer"), ("Einslive", "Spiele Einslive", "LaibRadio"),
        ("Musik", "Spiele Musik von Queen", "LaibMusik"), ("Frage", "Was ist ein Igel", None), ("Schalter", "Schalte {name} ein", "HassTurnOn")]


def five(ws, lang: str, reg: list, states: dict) -> tuple:
    """#864: the five intents through HA's conversation API, as text (no microphone): which one HA's
    local intents take, in how many ms. (detail, findings)"""
    if not lang.startswith("de"):
        return "", []
    exposed = [r["entity_id"] for r in reg if r.get("entity_id", "").split(".")[0] in ("light", "switch") and not r.get("disabled_by")
               and ((r.get("options") or {}).get("conversation") or {}).get("should_expose") is True]
    name = next((n for e in exposed if (n := ((states.get(e) or {}).get("attributes") or {}).get("friendly_name"))), "")
    probes = [(w, s.format(name=name), i) for w, s, i in FIVE if name or "{name}" not in s]
    t0 = time.monotonic()
    got = (ws("conversation/agent/homeassistant/debug", sentences=[s for _, s, _ in probes], language=lang) or {}).get("results") or []
    ms = round((time.monotonic() - t0) * 1000)
    ok, out = [], []
    for (what, sentence, want), hit in zip(probes, got + [None] * len(probes)):
        took = ((hit or {}).get("intent") or {}).get("name") if (hit or {}).get("match") else None
        if (want is None and not took) or (want is not None and took == want):
            ok.append(what)
        else:
            out.append({"severity": "gelb", "detail": f"Sprache: „{sentence}“ " + (f"nimmt {took}, nicht {want or 'Frage'}" if took else "wird nicht erkannt"),
                        "fix": "eigene Sätze in Home Assistant (ADR 0015, Prüfung 1); laib logs home-assistant"})
    missing = "" if name else " · Schalter nicht geprüft: kein Licht oder Schalter für Assist freigegeben"
    print(f"Absichten: {len(ok)} von {len(probes)} erkannt in {ms} ms: " + ", ".join(ok) + missing, flush=True)  # the unit's journal
    return f"Absichten: {len(ok)} von {len(probes)} erkannt" + missing, out


def voice(base: str, token: str, persona: str, wake_id: str, phrase: str) -> tuple:
    """#634: the house persona's Assist pipeline, measured, never switched: there and preferred,
    HA's local intents recognize PROBE in its language (conversation/agent/homeassistant/debug
    recognizes and executes nothing), its wake word is the persona's or one the engine offers,
    the satellites are on it. What it does not measure: the microphone, the end of speech, Whisper.
    (detail, findings)"""
    if not token or not persona:
        return "", []
    out, wait, on_device, said5, found5 = [], "", False, "", []
    try:
        req = urllib.request.Request(base.rstrip("/") + "/api/states", headers={"Authorization": "Bearer " + token})
        with urllib.request.urlopen(req, timeout=8) as resp:
            states = {s.get("entity_id"): s for s in json.load(resp)}
        ws = WS(base, token)
        try:
            listed = ws("assist_pipeline/pipeline/list") or {}
            ours = next((p for p in listed.get("pipelines") or [] if p.get("name") == persona), None)
            if ours is None:
                return f"Sprache: kein Ablauf „{persona}“", [{"severity": "gelb", "detail": f"Sprache: kein Assist-Ablauf „{persona}“", "fix": APPLY}]
            if listed.get("preferred_pipeline") != ours.get("id"):
                out.append(("gelb", f"Sprache: der Ablauf „{persona}“ ist nicht bevorzugt", APPLY))
            lang = ours.get("language") or "de"
            sentence = PROBE.get(lang.split("-")[0])
            said = f"Sprache: kein Prüfsatz für {lang}"
            if sentence:
                t0 = time.monotonic()
                got = (ws("conversation/agent/homeassistant/debug", sentences=[sentence], language=lang) or {}).get("results") or [None]
                ms = round((time.monotonic() - t0) * 1000)
                hit = got[0] or {}
                intent = (hit.get("intent") or {}).get("name") or ("eigener Satz" if hit.get("source") == "trigger" else "")
                if hit.get("match") and intent:
                    said = f"Sprache: lokale Befehle aktiv ({intent} in {ms} ms)"
                else:
                    said = "Sprache: lokale Befehle antworten nicht"
                    out.append(("gelb", f"Sprache: Home Assistants lokale Befehle erkennen „{sentence}“ ({lang}) nicht",
                                "Home Assistant → Einstellungen → Sprachassistenten: die Sprache des Ablaufs; laib logs home-assistant"))
            if ours.get("conversation_engine") != "conversation.home_assistant" and not ours.get("prefer_local_intents"):
                out.append(("gelb", f"Sprache: „{persona}“ fragt erst den Gesprächsagenten, nicht die lokalen Befehle", APPLY))
            have, engine = ours.get("wake_word_id"), ours.get("wake_word_entity")
            try:
                offered = [w.get("id") for w in (ws("wake_word/info", entity_id=engine) or {}).get("wake_words") or []] if engine else []
            except RuntimeError:
                offered = None   # the engine does not answer: nothing to say about its models
            if have == wake_id:
                said += f" · Weckwort {wake_id}"
            elif offered and wake_id in offered:
                out.append(("gelb", f"Weckwort: openWakeWord bietet {wake_id}, der Ablauf nutzt {have or 'keins'}", APPLY))
            elif have and offered is not None and have not in offered:
                out.append(("gelb", f"Weckwort: {have} bietet openWakeWord nicht an, der Ablauf wacht so nie auf", APPLY))
            else:   # not on the box's engine: a satellite may carry it itself (#759), read below
                wait = f" · Weckwort {have or 'nur am Gerät'}, bis {wake_id} da ist"
            reg = ws("config/entity_registry/list") or []
            said5, found5 = five(ws, lang, reg, states)
        finally:
            ws.close()
    except Exception as err:  # an unreachable or refusing HA is a finding, never a failed unit
        return "Sprache nicht messbar", [{"severity": "gelb", "detail": f"Sprache: nicht messbar ({type(err).__name__}: {err})", "fix": APPLY}]
    for r in reg:   # the satellites' selects, by HA's translation keys (a Voice PE, any Assist satellite)
        st = states.get(r.get("entity_id")) or {}
        if r.get("disabled_by") or not st:
            continue
        who, options = (st.get("attributes") or {}).get("friendly_name") or r.get("entity_id"), (st.get("attributes") or {}).get("options") or []
        if r.get("translation_key") == "pipeline" and st.get("state") != persona:
            out.append(("gelb", f"Satellit „{who}“: {st.get('state')}, nicht „{persona}“", APPLY))
        if r.get("translation_key") == "wake_word" and phrase and phrase in options:
            on_device = True   # Stimme's word on the device (#785): never say it is missing
            if st.get("state") != phrase:
                out.append(("gelb", f"Satellit „{who}“: weckt mit {st.get('state')}, kann „{phrase}“", APPLY))
    said += f" · Weckwort „{phrase}“ am Gerät" if wait and on_device else wait
    return said + (" · " + said5 if said5 else ""), [{"severity": s, "detail": d, "fix": f} for s, d, f in out] + found5


def write(path: str, r: dict) -> None:
    r["at"] = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
    tmp = path + ".tmp"
    with open(tmp, "w", encoding="utf-8") as f:
        json.dump(r, f, ensure_ascii=False)
        f.write("\n")
    os.replace(tmp, path)


if __name__ == "__main__":
    r = report(os.environ.get("HA_TOKEN", ""), os.environ.get("HA_URL", "http://127.0.0.1:8123"))
    r["findings"] += offers(os.environ.get("HA_URL", "http://127.0.0.1:8123"), os.environ.get("HA_TOKEN", ""), os.environ.get("LAIB_SERVICE", "home-assistant"))
    r["findings"] += unpacked(os.environ.get("HA_CONFIG", "/config"))
    detail, found = music(os.environ.get("MA_URL", "http://127.0.0.1:8095"), os.environ.get("MA_TOKEN", ""))
    if detail:
        r["findings"] += found
        r["detail"] = (r["detail"] + " · " if r["detail"] else "") + detail
    detail, found = voice(os.environ.get("HA_URL", "http://127.0.0.1:8123"), os.environ.get("HA_TOKEN", ""), os.environ.get("VOICE_PERSONA", ""),
                          os.environ.get("VOICE_WAKEWORD", ""), os.environ.get("VOICE_PHRASE", ""))
    if detail:
        r["findings"] += found
        r["detail"] = (r["detail"] + " · " if r["detail"] else "") + detail
    if open_keys := os.environ.get("ZWAVE_OPEN_KEYS", "").split():   # the unit looked on the host (#664)
        r["findings"].append({"severity": "gelb", "detail": "Z-Wave-Schlüssel für andere lesbar: zwave-js/" + ", zwave-js/".join(open_keys),
                              "fix": "laib apply home-assistant: pre-start macht sie 0600 (#664)"})
    z = zwave(os.environ["ZWAVE_URL"]) if os.environ.get("ZWAVE_URL") else {}
    if z:   # first, and the unit fails: laib status shows home-assistant red (#417)
        r["findings"].insert(0, z)
        r["detail"] = "Z-Wave ohne Controller" + (" · " + r["detail"] if r["detail"] else "")
    write(os.environ.get("LAIB_STATUS_FILE", "/config/.laib-status.json"), r)
    raise SystemExit(1 if z else 0)
