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
came. #861: a lesson the owner let in for the incident's finding is named
in the note with its fix, and handed back as used. #978: the log line is
the service's last real error (skills/lib/logzeilen.py, as the log review
tells one), read from laib logs' {unit: lines}, not its JSON's last line.
"""

import json
from datetime import datetime, timedelta, timezone

import laibskill
from laibskill import field, first_line
from lib import logzeilen

WAIT = timedelta(minutes=30)  # the runner's incidentWait: still red then, it escalates


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


def lessons_for(tr, lessons):
    """The owner's lessons for the incident's findings (#861): per finding
    its fix in one line — the condensed lesson's Lösung, or what helped."""
    out = {}
    for text in lessons or []:
        lines = (text or "").splitlines()
        finding = lines[0][len("# Lehre: "):] if lines and lines[0].startswith("# Lehre: ") else ""
        if finding in (tr.get("finding") or []):
            fix = [x.lstrip("- ").strip() for x in lines if x.lstrip("- ").startswith(("Lösung:", "Was half:"))]
            out[finding] = fix[0] if fix else ""
    return out


def run(s):
    tr = dict(s.trigger)
    if not tr.get("opened") or tr.get("aided"):
        return laibskill.done()
    granted = s.skill.get("granted") or []
    svcs, saw, done = services(tr), [], []
    for svc in svcs:
        if "logs" in granted:
            r = s.call("laib_logs", service=svc, since="1h")  # {unit: lines}: its last real error, else its last line
            line = "" if r.error else logzeilen.last_error(r.json()) if isinstance(r.json(), dict) else r.text.strip().split("\n")[-1][:200]
            if line:
                saw.append(f"{svc}: „{line}“")
        if "restart" in granted and svc in (tr.get("restart") or []):
            r = s.call("laib_restart", service=svc)
            green = field(r.json(), "green", None)  # {checks, green}; a refusal or an error is text
            done.append(f"{svc} neu gestartet ({'grün' if green is True else 'rot' if green is False else first_line(r.text or 'ohne Antwort')})")
    tr.update(aided=True, saw=" ".join(saw), done=", ".join(done) or "Logs gelesen")
    fixed = bool(done) and "diag" in granted and still_red(s, svcs) == []
    now = laibskill.when(s.now) or datetime.now(timezone.utc)
    text = f"Vorfall {now:%H:%M}: {field(tr, 'detail')}.{said(tr)}"
    taught = lessons_for(tr, s.data.get("lessons"))
    for finding, fix in taught.items():
        text += f" Lehre zu {finding}: {fix or 'liegt vor'}."
    if fixed:
        text += " Danach ist nichts davon mehr rot."
    else:
        text += f" Weiter: das Modell sieht nach; ist es um {now + WAIT:%H:%M} noch rot, eskaliere ich."
    s.call("laib_note", kind="incident", text=text)
    if fixed:
        return laibskill.done(outcome="green", note=text, trigger=tr, lessons=list(taught))
    return laibskill.done(trigger=tr, lessons=list(taught))
