#!/usr/bin/env python3
"""radicale's detail line for the Box page (#785): how many calendars, lists
and address books the household has, and how many entries are in them.

Read from Radicale's own storage, mounted read-only: each collection is a
folder collections/collection-root/<home>/<collection>/ with .Radicale.props
(tag VCALENDAR or VADDRESSBOOK; a calendar that holds only VTODO is a list),
each entry one file in it, the dot files left out. No login, no network.
Home Assistant's home is kalender.py's copies of the family's calendars, so
it is not counted twice. A storage that is not there is "nicht gemessen".

Environment: RADICALE_DATA, STATUS_DIR.
"""
import datetime, glob, json, os

DATA = os.environ.get("RADICALE_DATA", "/radicale")
STATUS = os.path.join(os.environ.get("STATUS_DIR", "/data"), "laib-status.json")
COPIES = {"homeassistant"}   # kalender.py's copies for Home Assistant (#495)


def status(detail, findings=()):
    s = {"at": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"), "detail": detail, "findings": list(findings)}
    os.makedirs(os.path.dirname(STATUS), exist_ok=True)
    with open(STATUS + ".tmp", "w") as f:
        json.dump(s, f, ensure_ascii=False, indent=1)
    os.replace(STATUS + ".tmp", STATUS)


def n(x):
    return f"{x:,}".replace(",", ".")


def plural(k, one, many):
    return f"{n(k)} {one if k == 1 else many}"


def main():
    root = os.path.join(DATA, "collections", "collection-root")
    if not os.path.isdir(root):
        return status("nicht gemessen: Radicales Ablage fehlt (" + root + ")",
                      [{"severity": "info", "unmeasured": True, "detail": "Kalender und Adressbücher nicht gezählt: Radicales Ablage fehlt", "fix": "laib inspect radicale"}])
    cals = lists = books = entries = 0
    for props in glob.glob(os.path.join(root, "*", "*", ".Radicale.props")):
        folder = os.path.dirname(props)
        if os.path.basename(os.path.dirname(folder)) in COPIES:
            continue
        try:
            with open(props) as f:
                p = json.load(f)
        except (OSError, ValueError):
            continue
        tag = p.get("tag")
        if tag == "VADDRESSBOOK":
            books += 1
        elif tag == "VCALENDAR":
            if (p.get("C:supported-calendar-component-set") or "").replace(" ", "") == "VTODO":
                lists += 1
            else:
                cals += 1
        else:
            continue
        entries += sum(1 for e in os.scandir(folder) if e.is_file() and not e.name.startswith("."))
    if not cals + lists + books:
        return status("noch kein Kalender und kein Adressbuch")
    parts = [plural(cals, "Kalender", "Kalender")]
    if lists:
        parts.append(plural(lists, "Liste", "Listen"))
    parts.append(plural(books, "Adressbuch", "Adressbücher"))
    status(", ".join(parts) + " · " + plural(entries, "Eintrag", "Einträge"))


if __name__ == "__main__":
    main()
