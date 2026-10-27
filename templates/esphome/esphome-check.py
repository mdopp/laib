#!/usr/bin/env python3
"""esphome's detail line for the Box page (#785): the devices ESPHome's
dashboard has, and which were last built with an older ESPHome than the
dashboard's own, so an update is ready for them; from the dashboard's GET
/devices (its configured and importable lists). Only reads: nothing is
compiled or uploaded (that is esphome-flash, after an approval). A device
never built here has no deployed version and counts as no update. A silent
dashboard is "nicht gemessen" and why.

Environment: ESPHOME_URL, STATUS_DIR.
"""
import datetime, json, os, urllib.request

URL = os.environ.get("ESPHOME_URL", "http://127.0.0.1:6052").rstrip("/")
STATUS = os.path.join(os.environ.get("STATUS_DIR", "/data"), "laib-status.json")


def status(detail, findings=()):
    s = {"at": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"), "detail": detail, "findings": list(findings)}
    os.makedirs(os.path.dirname(STATUS), exist_ok=True)
    with open(STATUS + ".tmp", "w") as f:
        json.dump(s, f, ensure_ascii=False, indent=1)
    os.replace(STATUS + ".tmp", STATUS)


def unmeasured(why, fix):
    status("nicht gemessen: " + why, [{"severity": "info", "unmeasured": True, "detail": "ESPHomes Geräte nicht gemessen: " + why, "fix": fix}])


def main():
    try:
        with urllib.request.urlopen(URL + "/devices", timeout=15) as r:
            d = json.load(r)
    except (OSError, ValueError) as e:
        return unmeasured(f"ESPHome antwortet nicht ({e})", "laib inspect esphome")
    configured = d.get("configured") if isinstance(d, dict) else None
    if not isinstance(configured, list):
        return unmeasured("ESPHome antwortet unerwartet (keine Geräteliste)", "laib logs esphome")
    found = len(d.get("importable") or [])
    old = [c for c in configured if isinstance(c, dict) and c.get("deployed_version") and c.get("current_version")
           and c["deployed_version"] != c["current_version"]]
    n = len(configured)
    if not n:
        detail = "noch kein Gerät eingerichtet"
    else:
        detail = f"{n} Gerät{'e' if n > 1 else ''} eingerichtet, "
        detail += f"{len(old)} mit Update bereit (ESPHome {old[0]['current_version']})" if old else "alle aktuell"
    if found:
        detail += f" · {found} neue{'s' if found == 1 else ''} im Netz gefunden"
    status(detail)


if __name__ == "__main__":
    main()
