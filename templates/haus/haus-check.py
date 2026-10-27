#!/usr/bin/env python3
"""haus's detail line for the Box page (#785): how many devices are signed in
to the Haus app, for how many people, and when one was last active.

Read from Haus's own devices.json (a list of {id, name, uid, hash, created,
lastSeen}), mounted read-only; only the count, the people and the newest
lastSeen leave it, never a hash or a device's name. No network. No file yet
means nobody has paired a device; a file that does not parse is "nicht
gemessen".

Environment: HAUS_DATA, STATUS_DIR.
"""
import datetime, json, os, re

DATA = os.environ.get("HAUS_DATA", "/haus")
STATUS = os.path.join(os.environ.get("STATUS_DIR", "/data"), "laib-status.json")


def status(detail, findings=()):
    s = {"at": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"), "detail": detail, "findings": list(findings)}
    os.makedirs(os.path.dirname(STATUS), exist_ok=True)
    with open(STATUS + ".tmp", "w") as f:
        json.dump(s, f, ensure_ascii=False, indent=1)
    os.replace(STATUS + ".tmp", STATUS)


def when(s):
    """Go's RFC 3339 with nanoseconds, as Haus writes it; None when it is not one"""
    m = re.match(r"(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d)(?:\.\d+)?(Z|[+-]\d\d:\d\d)$", s or "")
    if not m:
        return None
    return datetime.datetime.fromisoformat(m.group(1) + ("+00:00" if m.group(2) == "Z" else m.group(2)))


def age(t):
    d = (datetime.datetime.now(datetime.timezone.utc) - t).total_seconds()
    if d < 60:
        return "gerade eben"
    if d < 3600:
        return f"vor {int(d // 60)} min"
    if d < 48 * 3600:
        return f"vor {int(d // 3600)} h"
    return f"vor {int(d // 86400)} Tagen"


def main():
    try:
        with open(os.path.join(DATA, "devices.json")) as f:
            devices = json.load(f)
        if not isinstance(devices, list):
            raise ValueError("keine Liste")
    except FileNotFoundError:
        return status("noch kein Gerät angemeldet")
    except (OSError, ValueError) as e:
        return status("nicht gemessen: Haus' Geräteliste ist nicht lesbar (" + type(e).__name__ + ")",
                      [{"severity": "info", "unmeasured": True, "detail": "Angemeldete Geräte nicht gezählt: devices.json ist nicht lesbar", "fix": "laib logs haus"}])
    devices = [d for d in devices if isinstance(d, dict)]
    if not devices:
        return status("noch kein Gerät angemeldet")
    people = len({d.get("uid") for d in devices if d.get("uid")})
    detail = ("1 Gerät" if len(devices) == 1 else f"{len(devices)} Geräte") + " angemeldet, von " + ("1 Person" if people == 1 else f"{people} Personen")
    seen = [t for t in (when(d.get("lastSeen")) for d in devices) if t]
    if seen:
        detail += " · zuletzt aktiv " + age(max(seen))
    status(detail)


if __name__ == "__main__":
    main()
