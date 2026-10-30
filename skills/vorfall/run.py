"""vorfall: an incident's first aid, before the model (#792).

Moved out of the Operator's Go (operator/vorfall.go firstAid, #839). An
incident a look opened (trigger.opened) gets its first steps without a
model: each service's last log line, the restart diag offers where it is
granted, and whether it is still red; then one note in German says what it
saw and did, and what comes next. Nothing red any more: the session ends
green, no model. Still red: the model gets the incident, with what was done
(trigger.done); the trigger goes back to the runner marked aided, so a
replay from the queue does not restart again and an escalation says what was
seen. A red the kernel posted, or one aided already, goes to the model as it
came.
"""

import json
import re
from datetime import datetime, timedelta, timezone

import laibskill
from laibskill import field, first_line

WAIT = timedelta(minutes=30)  # the runner's incidentWait: still red then, it escalates


def last_line(text):
    """The last line of a text that says something, cut to 200 characters."""
    lines = (text or "").strip().split("\n")
    line = lines[-1].strip()
    return line[:200] + "…" if len(line) > 200 else line


def services(tr):
    """The incident's services: its own and the ones to restart, at most three."""
    out = []
    for svc in [field(tr, "service")] + list(field(tr, "restart", []) or []):
        if svc and svc not in out:
            out.append(svc)
    return out[:3]


def still_red(s, svcs):
    """Which of svcs diag shows red now; None when diag cannot be read."""
    r = s.call("laib_diag")
    try:
        fs = json.loads(r.text) if not r.error else None
    except ValueError:
        fs = None
    if not isinstance(fs, list):
        return None
    return sorted({field(f, "service") for f in fs if field(f, "severity") == "rot" and field(f, "service") in svcs})


def said(tr):
    """What the first aid saw in the log and did, as sentences."""
    out = ""
    if tr.get("saw"):
        out += " Im Log: " + tr["saw"] + "."
    if tr.get("done"):
        out += " Schon getan: " + tr["done"] + "."
    return out


def when(s):
    m = re.match(r"^(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d)(?:\.\d+)?(Z|[+-]\d\d:\d\d)$", s or "")
    return datetime.fromisoformat(m.group(1) + m.group(2).replace("Z", "+00:00")) if m else datetime.now(timezone.utc)


def run(s):
    tr = dict(s.trigger)
    if not tr.get("opened") or tr.get("aided"):
        return laibskill.done()
    granted = s.skill.get("granted") or []
    svcs, saw, done = services(tr), [], []
    for svc in svcs:
        if "logs" in granted:
            r = s.call("laib_logs", service=svc, since="1h")
            if not r.error and last_line(r.text):
                saw.append(f"{svc}: „{last_line(r.text)}“")
        if "restart" in granted and svc in (tr.get("restart") or []):
            r = s.call("laib_restart", service=svc)
            done.append(f"{svc} neu gestartet ({first_line(r.text or 'ohne Antwort')})")
    tr.update(aided=True, saw=" ".join(saw), done=", ".join(done) or "Logs gelesen")
    fixed = bool(done) and "diag" in granted and still_red(s, svcs) == []
    now = when(s.now)
    text = f"Vorfall {now:%H:%M}: {field(tr, 'detail')}.{said(tr)}"
    if fixed:
        text += " Danach ist nichts davon mehr rot."
    else:
        text += f" Weiter: das Modell sieht nach; ist es um {now + WAIT:%H:%M} noch rot, eskaliere ich."
    s.call("laib_note", kind="incident", text=text)
    if fixed:
        return laibskill.done(outcome="green", note=text, trigger=tr)
    return laibskill.done(trigger=tr)
