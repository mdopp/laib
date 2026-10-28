"""sicherung: the weekly backup sample, run by code (#287, #858).

The sample ran only when the model called backup check; a week the model
was away or chose otherwise had none. Now this script calls laib backup
check itself (restic checks a sample and restores one path into a scratch
dir and compares it), reads the week's backups from the journal and writes
the note: an incident when the check is red or no backup was green for more
than a day, else one sentence. No model (modell: nein): red, deviating and
stale are rules, not judgements; the morning report's model reads the note.
"""

import re
from datetime import datetime, timedelta, timezone

import laibskill
from laibskill import field, first_line

STALE = timedelta(days=1)


def when(s):
    """A Go time as JSON (RFC 3339, up to nanoseconds); None when unreadable."""
    m = re.match(r"^(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d)(?:\.(\d+))?(Z|[+-]\d\d:\d\d)$", s or "")
    if not m:
        return None
    frac = ("." + m.group(2)[:6]) if m.group(2) else ""
    return datetime.fromisoformat(m.group(1) + frac + m.group(3).replace("Z", "+00:00"))


def last_green(entries):
    """The newest green backup run (not a check) in the journal's entries."""
    ts = [when(field(e, "t")) for e in entries
          if field(e, "verb") == "backup" and field(e, "result") == "green" and not field(e, "reason").startswith("check")]
    ts = [t for t in ts if t is not None]
    return max(ts) if ts else None


def verdict(check, entries, now):
    """(kind, text) of the week's note: incident or watch."""
    at = now.strftime("%H:%M")
    red = check.error or not check.text.startswith("grün")
    last = last_green(entries)
    said = []
    if red:
        said.append(f"die Probe ist rot: {first_line(check.text).strip() or 'keine Antwort'}")
    if last is None:
        said.append("keine grüne Sicherung seit mindestens sieben Tagen")
    elif now - last > STALE:
        said.append(f"keine grüne Sicherung seit {last.astimezone(now.tzinfo):%d.%m. %H:%M}")
    if said:
        return "incident", f"Wochenprobe der Sicherung {at}: " + "; ".join(said) + ". Laib stellt nichts wieder her; ein Mensch sieht nach."
    detail = first_line(check.text).removeprefix("grün ·").strip()
    return "watch", f"Wochenprobe der Sicherung {at}: grün ({detail}); letzte Sicherung {last.astimezone(now.tzinfo):%d.%m. %H:%M}."


def run(s):
    check = s.call("laib_backup", action="check")
    entries = s.call("laib_journal", since="168h", verb="backup").json()
    now = when(s.now) or datetime.now(timezone.utc)
    kind, text = verdict(check, entries if isinstance(entries, list) else [], now)
    s.call("laib_note", kind=kind, text=text)
    return laibskill.done(outcome="green" if kind == "watch" else "red", why="" if kind == "watch" else "Sicherung rot oder zu alt", note=text)
