#!/usr/bin/env python3
"""The Bote's switch test set (#520): short and long commands to Hermes' API server.

Each sentence goes in its own session to POST /v1/chat/completions, streamed, the
way Haus's Sprechen field reaches the Bote. A case passes when the device is
where the sentence puts it (on, off, the percentage, the blind open or closed),
read from Home Assistant's /api/states with HA_TOKEN from the same key file, the
answer is not the sentence said back, and it carries no steer frame
([OUT-OF-BAND USER MESSAGE …]). The device is the measure because the persona
plugin hands a sentence to HA's own intents first (#520, ADR 0015): then no
Hermes tool runs, and the table says "HA"; a switch through the model's ha tool
says "Modell". Without HA_TOKEN in the file, a Home Assistant tool must have run
(Hermes' hermes.tool.progress events). The cases come in pairs that switch back,
so the house ends as it was.

    python3 scripts/bote-schalttest.py --key-file $DATA_DIR/bote/hermes/.env --licht Bambi [--jalousie "Rollo Wohnzimmer"]

The key is read from the file (API_SERVER_KEY=…, or the bare key), never from
argv. Exit 0: every case switched; 1: at least one did not; 2: the API did not
answer, nothing was measured. --list prints the sentences only, for the same
test by hand over Signal (templates/bote/README.md, "Switch commands").
Stdlib only.
"""
import argparse, json, re, sys, time, urllib.error, urllib.request, uuid

LICHT = ["{l} an", "{l} aus", "Schalte bitte {l} an", "Dimm {l} auf 30 Prozent", "{l} 80 Prozent", "Mach {l} wieder aus"]
JALOUSIE = ["{j} zu", "{j} auf", "Fahr bitte {j} runter", "Mach {j} wieder auf"]
FRAME = "OUT-OF-BAND USER MESSAGE"


def sentences(licht, jalousie):
    out = [s.format(l=licht) for s in LICHT]
    if jalousie:
        out += [s.format(j=jalousie) for s in JALOUSIE]
    return out


def read_key(path):
    with open(path) as f:
        text = f.read()
    m = re.search(r"(?m)^API_SERVER_KEY=(.+)$", text)
    key = (m.group(1) if m else text).strip()
    if not key or "\n" in key:
        raise SystemExit("bote-schalttest: kein API_SERVER_KEY in " + path)
    return key


def read_ha_token(path):
    with open(path) as f:
        m = re.search(r"(?m)^HA_TOKEN=(.+)$", f.read())
    return m.group(1).strip() if m else ""


def ha_get(ha, token, path):
    req = urllib.request.Request(ha.rstrip("/") + path, headers={"Authorization": "Bearer " + token})
    with urllib.request.urlopen(req, timeout=10) as f:
        return json.load(f)


def entity(ha, token, name, domain):
    """the entity id Home Assistant shows under that name, in that domain"""
    for st in ha_get(ha, token, "/api/states"):
        eid = st.get("entity_id", "")
        if eid.startswith(domain + ".") and (st.get("attributes") or {}).get("friendly_name", "").casefold() == name.casefold():
            return eid
    raise SystemExit("bote-schalttest: Home Assistant kennt kein %s namens %s" % (domain, name))


def expected(sentence):
    """(state, brightness percent or None) the sentence puts the device in"""
    words = sentence.lower().split()
    if "prozent" in words:
        return "on", int(words[words.index("prozent") - 1])
    if words[-1] in ("zu", "runter"):
        return "closed", None
    if words[-1] == "auf":
        return "open", None
    return ("on" if words[-1] == "an" else "off"), None


def at(state, want):
    """'' when the device's state is what the sentence asked for, else what it is"""
    st, pct = want
    have = state.get("state")
    if have not in {"closed": ("closed", "closing"), "open": ("open", "opening")}.get(st, (st,)):   # a blind still moving counts
        return "%s ist %s, nicht %s" % ((state.get("attributes") or {}).get("friendly_name", "?"), have, st)
    b = (state.get("attributes") or {}).get("brightness")
    if pct is not None and (b is None or abs(round(b * 100 / 255) - pct) > 2):
        return "Helligkeit %s %%, nicht %d %%" % ("-" if b is None else round(b * 100 / 255), pct)
    return ""


def ask(url, key, sentence, timeout):
    """(answer text, tools that ran) for one sentence in a fresh session"""
    body = json.dumps({"model": "hermes-agent", "stream": True,
                       "messages": [{"role": "user", "content": sentence}]}).encode()
    req = urllib.request.Request(url.rstrip("/") + "/chat/completions", data=body, headers={
        "Content-Type": "application/json", "Authorization": "Bearer " + key,
        "X-Hermes-Session-Id": "schalttest-" + uuid.uuid4().hex[:12]})
    text, tools, event = [], [], ""
    with urllib.request.urlopen(req, timeout=timeout) as f:
        for raw in f:
            line = raw.decode("utf-8", "replace").rstrip("\r\n")
            if line.startswith("event:"):
                event = line[6:].strip()
                continue
            if not line.startswith("data:"):
                if not line:
                    event = ""
                continue
            data = line[5:].strip()
            if data == "[DONE]":
                break
            try:
                obj = json.loads(data)
            except ValueError:
                continue
            if event == "hermes.tool.progress":
                if obj.get("status") == "running":
                    tools.append(obj.get("tool", ""))
            elif not event:
                for c in obj.get("choices") or []:
                    text.append((c.get("delta") or {}).get("content") or "")
    return "".join(text).strip(), tools


def verdict(sentence, answer, tools, device=None):
    """'' when the case switched, else what is wrong; device: at()'s word on the measured state, None unmeasured"""
    if FRAME in answer:
        return "Steuer-Rahmen im Text"
    if device:
        return device
    if device is None and not any(t.startswith("mcp__ha__") for t in tools):   # Hermes' name for an MCP tool: mcp__<server>__<tool>
        return "kein ha-Werkzeug" + (" (nur %s)" % ", ".join(tools) if tools else "")
    if not answer:
        return "keine Antwort"
    if answer.strip(" .!").lower() == sentence.strip(" .!").lower():
        return "nur wiederholt"
    return ""


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--licht", required=True, help="a dimmable light's name as Home Assistant shows it")
    ap.add_argument("--jalousie", default="", help="a cover's name; without it the blind cases are skipped")
    ap.add_argument("--key-file", help="Hermes' .env (API_SERVER_KEY=…) or a file with the key alone")
    ap.add_argument("--url", default="http://127.0.0.1:8642/v1")
    ap.add_argument("--ha-url", default="http://127.0.0.1:8123", help="Home Assistant, from the box (the .env's HA_URL is the pod's)")
    ap.add_argument("--wait", type=float, default=5, help="seconds the device may take to get there")
    ap.add_argument("--timeout", type=float, default=120)
    ap.add_argument("--list", action="store_true", help="print the sentences only (for Signal, by hand)")
    a = ap.parse_args(argv)
    cases = sentences(a.licht, a.jalousie)
    if a.list:
        print("\n".join(cases))
        return 0
    if not a.key_file:
        ap.error("--key-file is needed (the key never goes on argv)")
    key, token = read_key(a.key_file), read_ha_token(a.key_file)
    ids = {}
    if token:
        try:
            ids = {s: entity(a.ha_url, token, a.licht, "light") for s in cases[:len(LICHT)]}
            ids.update({s: entity(a.ha_url, token, a.jalousie, "cover") for s in cases[len(LICHT):]})
        except (urllib.error.URLError, OSError) as e:
            print("NICHT GEMESSEN: Home Assistant antwortet nicht auf %s (%s)" % (a.ha_url, getattr(e, "reason", e)))
            return 2
    failed = 0
    for s in cases:
        t0 = time.monotonic()
        try:
            answer, tools = ask(a.url, key, s, a.timeout)
            device = None
            if token:
                end = time.monotonic() + a.wait
                while True:
                    device = at(ha_get(a.ha_url, token, "/api/states/" + ids[s]), expected(s))
                    if not device or time.monotonic() >= end:
                        break
                    time.sleep(0.2)
        except (urllib.error.URLError, OSError) as e:
            print("NICHT GEMESSEN: %s antwortet nicht (%s)" % (a.url, getattr(e, "reason", e)))
            return 2
        why = verdict(s, answer, tools, device)
        failed += bool(why)
        first = (answer.splitlines() or [""])[0][:80]
        path = "" if not token or why else ("Modell" if any(t.startswith("mcp__ha__") for t in tools) else "HA") + ": "
        print("%-6s %5.1f s  %-34s %s" % ("OK" if not why else "FEHLT", time.monotonic() - t0, s, why or path + first))
        if why:
            print("       Antwort: %s | Werkzeuge: %s" % (first, ", ".join(tools) or "keine"))
    print("%d von %d Befehlen geschaltet" % (len(cases) - failed, len(cases)))
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
