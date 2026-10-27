"""morgenbericht: the lines the morning report ends with, written by code.

Moved out of the Operator's Go (operator/record.go Rate, operator/lessons.go
Stale, operator/aufgaben.go, #839). The model writes the report itself in its
loop with its tools (modell: frei); this script only adds what must always be
there: the month's rate of the Operator's proposals (#544), a lesson that
keeps failing, named for removal (#544), and the due Box-Aufgaben (#667).
The runner hands the inbox (data.history) and laib aufgabe list --faellig
(data.due).
"""

import json
import re
from datetime import datetime, timedelta, timezone

import laibskill
from laibskill import field

COUNTED = ["effective", "no-effect", "rolled-back", "rejected"]  # "later" and "applied" are neither
WORD = {"effective": "wirksam", "no-effect": "ohne Wirkung", "rolled-back": "zurückgenommen", "rejected": "abgelehnt"}


def when(s):
    """A Go time as JSON (RFC 3339, up to nanoseconds); the zero time when unreadable."""
    m = re.match(r"^(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d)(?:\.(\d+))?(Z|[+-]\d\d:\d\d)$", s or "")
    if not m:
        return datetime(1, 1, 1, tzinfo=timezone.utc)
    frac = ("." + m.group(2)[:6]) if m.group(2) else ""
    return datetime.fromisoformat(m.group(1) + frac + m.group(3).replace("Z", "+00:00"))


def lesson(p):
    files = field(p, "files", []) or []
    return bool(files) and all(f.startswith("lessons/") for f in files)


def lesson_finding(p):
    r = field(p, "reason")
    return (r[len("Lehre: "):] if r.startswith("Lehre: ") else r).split(" — ", 1)[0]


def mine(history):
    """The Operator's own proposals, lessons excepted."""
    return [p for p in history if field(p, "actor").split(":", 1)[0] == "ops" and not lesson(p)]


def tally(ps):
    by = {}
    for p in ps:
        if field(p, "outcome") in COUNTED:
            by[field(p, "outcome")] = by.get(field(p, "outcome"), 0) + 1
    return sum(by.values()), by


def words(by):
    return ", ".join(f"{by[k]} {WORD[k]}" for k in COUNTED if by.get(k))


def rate(history, now):
    n, by = tally([p for p in mine(history) if now - when(field(p, "created")) <= timedelta(days=30)])
    if n == 0:
        return ""
    eff = by.get("effective", 0)
    return f"Vorschläge des Operators, 30 Tage: {eff} von {n} wirksam ({eff * 100 // n} %); {words(by)}."


def stale(history):
    """The lessons the owner let in whose finding was since proposed for at
    least twice without success: candidates to remove."""
    out = []
    for le in history:
        if not lesson(le) or field(le, "status") != "approved" or not field(le, "reason").startswith("Lehre: "):
            continue
        failed = sorted(f"#{field(p, 'id', 0)}" for p in mine(history)
                        if field(p, "finding") == lesson_finding(le) and when(field(p, "created")) > when(field(le, "decided")) and field(p, "outcome") in COUNTED[1:])
        if len(failed) >= 2:
            out.append(f"Lehre {', '.join(field(le, 'files', []))} hilft nicht: {len(failed)} spätere Vorschläge ohne Erfolg ({', '.join(failed)}), zum Entfernen.")
    return " ".join(out)


def due_line(text):
    """The due Box-Aufgaben in one sentence (#667); none, or no answer, says nothing."""
    try:
        tasks = field(json.loads(text or ""), "aufgaben", [])
    except ValueError:
        return ""
    if not isinstance(tasks, list) or not tasks:
        return ""
    return "Fällige Box-Aufgaben: " + ", ".join(f"#{field(a, 'id', 0)} {field(a, 'titel')}" for a in tasks) + "."


def run(s):
    history = s.data.get("history") or []
    lines = [rate(history, when(s.now)), stale(history), due_line(s.data.get("due"))]
    return laibskill.done(human=" ".join(x for x in lines if x))
