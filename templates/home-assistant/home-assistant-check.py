"""Home Assistant's integrations, as laib's status file (#55, #309).

Home Assistant's integrations are set up by hand once (README.md). With the
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
FIXES = {"Frage als Gesprächsagent": "laib add frage, dann laib apply home-assistant: die Ollama-Integration auf Frage (#491)"}


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
               if not any(e.get("domain") == domain and (not title or title in (e.get("title") or "").lower()) for e in got)]
    if not missing:
        return {"detail": f"alle {len(EXPECTED)} Integrationen eingerichtet", "findings": []}
    return {"detail": "fehlt: " + ", ".join(missing),
            "findings": [{"severity": "gelb", "detail": f"{name}: fehlt", "fix": FIXES.get(name, FIX)} for name in missing]}


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


def write(path: str, r: dict) -> None:
    r["at"] = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
    tmp = path + ".tmp"
    with open(tmp, "w", encoding="utf-8") as f:
        json.dump(r, f, ensure_ascii=False)
        f.write("\n")
    os.replace(tmp, path)


if __name__ == "__main__":
    r = report(os.environ.get("HA_TOKEN", ""), os.environ.get("HA_URL", "http://127.0.0.1:8123"))
    z = zwave(os.environ["ZWAVE_URL"]) if os.environ.get("ZWAVE_URL") else {}
    if z:   # first, and the unit fails: laib status shows home-assistant red (#417)
        r["findings"].insert(0, z)
        r["detail"] = "Z-Wave ohne Controller" + (" · " + r["detail"] if r["detail"] else "")
    write(os.environ.get("LAIB_STATUS_FILE", "/config/.laib-status.json"), r)
    raise SystemExit(1 if z else 0)
