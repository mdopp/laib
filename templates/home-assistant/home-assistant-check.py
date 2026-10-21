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
import json
import os
import time
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
# finding of its own, the server behind it is gone (Solaris' after Phase 5, #540)
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
                         "Geräte & Dienste); was von Solaris übrig ist, listet scripts/solaris-off.py (#540)"} for e in dead]
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
    detail, found = music(os.environ.get("MA_URL", "http://127.0.0.1:8095"), os.environ.get("MA_TOKEN", ""))
    if detail:
        r["findings"] += found
        r["detail"] = (r["detail"] + " · " if r["detail"] else "") + detail
    z = zwave(os.environ["ZWAVE_URL"]) if os.environ.get("ZWAVE_URL") else {}
    if z:   # first, and the unit fails: laib status shows home-assistant red (#417)
        r["findings"].insert(0, z)
        r["detail"] = "Z-Wave ohne Controller" + (" · " + r["detail"] if r["detail"] else "")
    write(os.environ.get("LAIB_STATUS_FILE", "/config/.laib-status.json"), r)
    raise SystemExit(1 if z else 0)
