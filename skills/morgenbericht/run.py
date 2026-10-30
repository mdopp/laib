"""morgenbericht: the lines the morning report ends with, written by code.

Moved out of the Operator's Go (operator/record.go Rate, operator/lessons.go
Stale, operator/aufgaben.go, #839). The model writes the report itself in its
loop with its tools (modell: frei); this script only adds what must always be
there: the month's rate of the Operator's proposals (#544), a lesson that
keeps failing, named for removal (#544), and the due Box-Aufgaben (#667).
The runner hands the inbox (data.history) and laib aufgabe list --faellig
(data.due). #858: a failing model consumed the slot with no report; the
script also reads status and journal and hands a report written from them,
which the runner writes when the model fails. #859: the Operator's sessions
of the month (data.sessions) give the day's tokens and model time; #861:
and, once a month, which lessons they were offered and which findings came
back.
"""

import json
import re
from datetime import datetime, timedelta, timezone

import laibskill
from laibskill import field

COUNTED = ["effective", "no-effect", "rolled-back", "rejected"]  # "later" and "applied" are neither
RESULT = {"green": "grün", "red": "rot"}
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


def model_line(sessions, now):
    """What the Operator's sessions cost the model in the last 24 hours (#859)."""
    day = [x for x in sessions if field(x, "modelMs", 0) and now - when(field(x, "started")) <= timedelta(days=1)]
    if not day:
        return "Modell in 24 Stunden: keine Sitzung des Operators."
    n_in, n_out, ms = (sum(field(x, k, 0) for x in day) for k in ("in", "out", "modelMs"))
    return f"Modell in 24 Stunden: {len(day)} {'Sitzung' if len(day) == 1 else 'Sitzungen'}, {n_in} Token gelesen, {n_out} geschrieben, {ms / 60000:.0f} Minuten."


def lessons_line(sessions, now):
    """Once a month (#861): which lessons the sessions' prompts carried, and
    which findings came back — an incident's first session per finding,
    "trotz Lehre" when a session was offered that finding's lesson before."""
    month = sorted((x for x in sessions if now - when(field(x, "started")) <= timedelta(days=30)), key=lambda x: when(field(x, "started")))
    used, seen, back, taught = {}, {}, {}, set()
    for x in month:
        tr = field(x, "trigger", {})
        if field(tr, "kind") == "event" and not field(tr, "queued", 0):
            for f in field(tr, "finding", []) or [field(tr, "service")]:
                seen[f] = seen.get(f, 0) + 1
                if seen[f] > 1:
                    back[f] = back.get(f, "") or (" (trotz Lehre)" if f in taught else "")
        for le in field(x, "lessons", []) or []:
            used[le] = used.get(le, 0) + 1
            taught.add(le)
    said = "Lehren im Monat: " + (", ".join(f"{k} {n}× angeboten" for k, n in sorted(used.items(), key=lambda kv: -kv[1])) or "keine angeboten") + ". "
    return said + ("Wiedergekommen: " + ", ".join(f"{f} {seen[f]}×{back[f]}" for f in sorted(back, key=lambda f: -seen[f])) + "." if back else "Kein Befund kam wieder.")


def hm(s, tz):
    """A journal time as the box's clock, or "?"."""
    d = when(s)
    return d.astimezone(tz).strftime("%H:%M") if d.year > 1 else "?"


def fallback(status, journal, now):
    """A report from laib status and the journal's last 24 hours, red first,
    for a morning whose model failed (#858)."""
    ready, services = field(status, "ready", {}), field(status, "services", []) or []
    red = [f"{field(x, 'name')} ({field(x, 'detail')})" if field(x, "detail") else field(x, "name") for x in services if field(x, "state") == "rot"]
    said = [f"Morgenbericht ohne Modell, aus Status und Journal: Laib {field(ready, 'state', 'unbekannt')}, "
            f"{field(ready, 'services', len(services))} Dienste, {field(ready, 'red', len(red))} rot, {field(ready, 'yellow', 0)} gelb."]
    if red:
        said.append("Rot: " + ", ".join(red) + ".")
    journal = journal if isinstance(journal, list) else []
    backups = [e for e in journal if field(e, "verb") == "backup"]
    if backups:
        b = backups[-1]
        said.append(f"Letzte Sicherung {hm(field(b, 't'), now.tzinfo)}: {RESULT.get(field(b, 'result'), field(b, 'result'))}.")
    reds = [e for e in journal if field(e, "result") == "red"]
    if reds:
        shown = [" ".join(x for x in (hm(field(e, "t"), now.tzinfo), field(e, "verb"), field(e, "service"), laibskill.one_line(field(e, "detail"))[:120]) if x) for e in reds[-5:]]
        said.append(f"Im Journal der Nacht {len(reds)} {'roter Eintrag' if len(reds) == 1 else 'rote Einträge'}: " + "; ".join(shown) + ".")
    else:
        said.append(f"Im Journal der Nacht {len(journal)} Einträge, keiner rot.")
    return " ".join(said)


def run(s):
    history = s.data.get("history") or []
    sessions, month = s.data.get("sessions") or [], s.now[:7]
    lines = [rate(history, when(s.now)), stale(history), due_line(s.data.get("due")), model_line(sessions, when(s.now))]
    if field(s.state, "month") != month:  # the month's first report
        lines.append(lessons_line(sessions, when(s.now)))
    human = " ".join(x for x in lines if x)
    status = s.call("laib_status").json() or {}
    journal = s.call("laib_journal", since="24h").json()
    return laibskill.done(human=human, fallback=(fallback(status, journal, when(s.now)) + " " + human).strip(), state={"month": month})
