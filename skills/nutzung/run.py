"""nutzung: the week's usage for the model's suggestions (#270).

Moved out of the Operator's Go (operator/usage.go, #839). The runner reads
usage-metrics with its own token and hands the answers as they came
(data.usage); the script renders them for the model and vets each wish the
model makes: a suggestion becomes a ticket, and an intake repo may be
public, so it never quotes one of the family's unanswered sentences word for
word (owner, 2026-10-16). The model decides what is worth suggesting.
"""

import json

import laibskill
from laibskill import field


def render(usage):
    """The window as text, and the unanswered sentences a wish may not quote."""
    days, summary = field(usage, "days", 7), field(usage, "summary", {})
    out = [f"Nutzung der letzten {days} Tage (anonyme Zählungen, seit {field(summary, 'from')}): {field(summary, 'total', 0)} insgesamt."]
    used = set()
    for a in field(summary, "apps", []) or []:
        used.add(field(a, "app"))
        ev = ", ".join(f"{field(e, 'event')} {field(e, 'count', 0)}" for e in field(a, "events", []) or [])
        out.append(f"- {field(a, 'app')}: {field(a, 'total', 0)} ({ev})")
    idle = [k for k in field(summary, "knownApps", []) or [] if k not in used]
    if idle:
        out.append(f"Ohne Nutzung in diesen {days} Tagen: {', '.join(idle)}")
    misses = field(usage, "misses", None)
    if isinstance(misses, dict):
        misses = field(misses, "misses", [])
    if not isinstance(misses, list):
        out.append(f"\nUnbeantwortete Sätze: nicht abrufbar ({field(usage, 'missesError') or 'keine Liste'}).")
        return "\n".join(out) + "\n", []
    out.append("\nUnbeantwortete Sätze (ohne Namen, nur für dich — nicht wörtlich zitieren):")
    for m in misses:
        out.append(f"- {field(m, 'count', 0)}× {field(m, 'source')}: „{field(m, 'text')}“ (zuletzt {field(m, 'lastDay')})")
    if not misses:
        out.append("- keine")
    return "\n".join(out) + "\n", [field(m, "text") for m in misses]


def quotes(text, sentences):
    """Whether text carries one of the sentences word for word."""
    t = text.lower()
    for s in sentences:
        s = s.strip().rstrip("?!. ").lower()
        if len(s) >= 8 and s in t:
            return True
    return False


def run(s):
    text, s.sentences = render(s.data.get("usage") or {})
    return laibskill.done(context=text, checks=["laib_wish"])


def check(s, tool, args):
    if quotes(json.dumps(args, ensure_ascii=False), s.sentences):
        return "der Vorschlag zitiert einen Satz der Familie wörtlich — nenne Thema und Anzahl, nicht den Satz"
    return ""
