"""durchsicht's check before a wish (#975): what a person did by hand before
filing one, in code; the model judges only what is left.

1. A planned stop. An error line of a service from two minutes before to
   three minutes after a planned stop of it, or of a service it depends on,
   is expected: never a pattern, only counted. Planned is a unit's
   "Stopping" line (systemd writes one only when someone asked; a crash
   writes "Main process exited", its automatic restart no "Stopping") that
   Laib's journal accounts for: an apply, the nightly backup, an updates
   run, Laib's own restart (ready), converge, restart, stop, start, repair,
   rollback, restore, pause or resume within three minutes of its span. The
   window ends three minutes after the unit's next "Started".
2. Still happening? A pattern new today whose last line is older than six
   hours, or older than the last planned restart of its service or of a
   dependency while the service is green now, is over. A pattern the last
   review saw too is not: coming back is news.
3. Already resolved? Only what the lines already read can tell: a
   certificate Caddy obtained, after the failures, for every name it failed for.
"""

import re
from collections import namedtuple
from datetime import datetime, timedelta, timezone

import laibskill

BEFORE, AFTER, PAD, QUIET = timedelta(minutes=2), timedelta(minutes=3), timedelta(minutes=3), timedelta(hours=6)
RESTARTS = r"systemd\[\d+\]:\s(Stopping|Started)\s"  # laib logs --grep: no space, args are split on spaces
STOP = re.compile(r"^(\S+) \S+ systemd\[\d+\]: (Stopping|Started) (\S+)", re.A)
IDENT = re.compile(r'"identifier":"([^"]+)"')
OBTAINED = "certificate obtained successfully"
MARK = "Aussortiert, kein Wunsch:"  # the morning report finds the sentence by it
PLANNED = {"apply": "Anwenden", "backup": "Sicherung", "updates": "Update", "ready": "Laib-Neustart", "converge": "converge",
           "restart": "laib restart", "stop": "laib stop", "start": "laib start", "repair": "Reparatur", "rollback": "Rücknahme",
           "restore": "Wiederherstellung", "pause": "laib pause", "resume": "laib resume"}
# What a service's lines may follow from: the templates' after: (test_run.py
# holds this to templates/*/laib.yml), laib itself for needs: [mcp], auth for
# an OIDC login, and Home Assistant for its clients, which name no after:
# because a box without Home Assistant still loads them.
DEPENDS = {
    "audiobookshelf": "auth file-share", "beets": "file-share", "bote": "laib home-assistant", "file-share": "auth",
    "gesundheit": "home-assistant", "haus": "auth laib home-assistant", "home-assistant": "auth mosquitto",
    "immich": "auth file-share", "kalender": "radicale", "media": "auth file-share", "music-assistant": "home-assistant",
    "paperless": "auth file-share", "radicale": "auth", "scan": "paperless file-share", "stimme": "home-assistant",
    "vaultwarden": "auth", "werkstatt": "laib", "whoami": "auth",
}
Halt = namedtuple("Halt", "svc label stop start")


def when(s):
    """A journal time (RFC 3339, up to nanoseconds), or None."""
    try:
        d = datetime.fromisoformat(str(s).replace("Z", "+00:00"))
    except ValueError:
        return None
    return d if d.tzinfo else None


def hm(t, now):
    return t.astimezone(now.tzinfo).strftime("%H:%M")


def closure(svc):
    seen, todo = set(), [svc]
    while todo:
        d = todo.pop()
        if d not in seen:
            seen.add(d)
            todo += DEPENDS.get(d, "").split()
    return sorted(seen)


def spans(entries):
    """The journal's planned work: (from, to, label, service or "") per entry."""
    out = []
    for e in entries if isinstance(entries, list) else []:
        verb, t, ms = laibskill.field(e, "verb"), when(laibskill.field(e, "t")), laibskill.field(e, "ms", 0)
        if verb in PLANNED and t:
            ms = ms if isinstance(ms, (int, float)) else 0
            out.append((t - timedelta(milliseconds=ms) - PAD, t + PAD, PLANNED[verb], str(laibskill.field(e, "service") or "")))
    return out


def halts(svc, units, planned):
    """A service's stops the journal accounts for, each with the unit's next start."""
    out = []
    for text in (t for t in units.values() if isinstance(t, str)):
        lines = [(when(m[1]), m[2], m[3]) for m in map(STOP.match, text.split("\n")) if m]
        for i, (t, verb, unit) in enumerate(lines):
            # the entry nearest the stop names it: the backup, then the restart after it
            why = min((p for p in planned if t and p[0] <= t <= p[1] and p[3] in ("", svc)), key=lambda p: abs(p[1] - PAD - t), default=None) if verb == "Stopping" else None
            if why:
                out.append(Halt(svc, why[2], t, next((u for u, v, n in lines[i + 1:] if u and v == "Started" and n == unit), t)))
    return out


class Plan:
    """The day's planned stops, read per service when first needed."""

    def __init__(self, s, journal):
        self.s, self.blind, self.spans, self.halts, self.unread = s, not isinstance(journal, list), spans(journal), {}, []
        self.now, self.expected = when(s.now) or datetime.now(timezone.utc), {}

    def of(self, svc):
        if svc not in self.halts:
            units = self.s.call("laib_logs", args=f"{svc} --since 25h --lines 200 --grep {RESTARTS}").json() if self.spans else {}
            if not isinstance(units, dict):
                units, self.unread = {}, self.unread + [svc]
            self.halts[svc] = halts(svc, units, self.spans)
        return self.halts[svc]

    def near(self, svc, t):
        """The planned stop of svc or a dependency whose window holds t, the one under way first, or None."""
        return max((h for d in closure(svc) for h in self.of(d) if t and h.stop - BEFORE <= t <= h.start + AFTER),
                   key=lambda h: (h.stop <= t, -abs((h.stop - t).total_seconds())), default=None)

    def last(self, svc):
        return max((h for d in closure(svc) for h in self.of(d)), key=lambda h: h.start, default=None)

    def during(self, svc, unit, t):
        """Counts a line inside a planned stop's window as expected; True when it was."""
        h = self.near(svc, t)
        if h:
            e = self.expected.setdefault(unit, [0, set()])
            e[0] += 1
            e[1].add((h.stop, f"{h.label} von {h.svc} {hm(h.stop, self.now)}"))
        return bool(h)


def judge(c, new, plan, green, obtained):
    """Why a pattern is no wish, or "" and what was checked."""
    last, ids, h, now = max(c["times"], default=None), c["ids"], plan.last(c["svc"]), plan.now
    if ids and all(obtained.get(i) and obtained[i] > t for i, t in ids.items()):
        return f"behoben: Zertifikat für {', '.join(sorted(ids))} danach ausgestellt ({hm(max(obtained[i] for i in ids), now)})", ""
    if new and last and now - last > QUIET:
        return f"vorbei: zuletzt {hm(last, now)}, seit über {QUIET.seconds // 3600} Stunden nicht mehr", ""
    if new and last and h and h.start > last and green:
        return f"vorbei: zuletzt {hm(last, now)}, seit {h.label} von {h.svc} um {hm(h.start, now)} nicht mehr, Dienst grün", ""
    blind = plan.blind or any(d in plan.unread for d in closure(c["svc"]))
    said = ["geplante Neustarts nicht geprüft (nicht lesbar)" if blind else "nicht während eines geplanten Neustarts",
            f"zuletzt {hm(last, now)} (vor {int((now - last).total_seconds() // 60)} min)" if last else "Zeitpunkt nicht lesbar"]
    if h and last:
        said.append(f"{'auch nach' if h.start <= last else 'vor'} {h.label} von {h.svc} um {hm(h.start, now)}" + ("" if green or h.start <= last else ", Dienst nicht grün"))
    else:
        said.append("kein geplanter Neustart")
    said.append("noch aktuell" if new else "schon in der letzten Durchsicht")
    return "", ", ".join(said)


def told(dropped, plan):
    """The note's last sentence: what became no wish, and why — once, so nothing disappears silently."""
    items = [f"{u} {n}× während eines geplanten Neustarts ({', '.join(x for _, x in sorted(hs)[:3])}{f' und {len(hs) - 3} weiteren' if len(hs) > 3 else ''})"
             for u, (n, hs) in sorted(plan.expected.items())]
    dropped = sorted(dropped, key=lambda d: -d[0]["count"])  # the note stays under laib note's 4,000 characters
    items += [f"{c['unit']} „{c['pattern'] if len(c['pattern']) <= 60 else c['pattern'][:60].rstrip() + '…'}“ {c['count']}× {why}" for c, why in dropped[:8]]
    items += [f"{len(dropped) - 8} weitere Muster ({sum(c['count'] for c, _ in dropped[8:])}×)"] if len(dropped) > 8 else []
    return f"{MARK} {'; '.join(items)}." if items else ""


def check(s, tool, args):
    """The model's wish names what was checked and one of the shown patterns' services."""
    services, saw, text = getattr(s, "services", set()), str(args.get("saw", "")), str(args.get("text", ""))
    if tool != "laib_wish":
        return ""
    if "geprüft" not in saw:
        return "saw nennt, was geprüft wurde: übernimm die Zeile „geprüft: …“ des Musters"
    if not any(svc.lower() in (text + " " + saw).lower() for svc in services):
        return "ein Wunsch nur zu einem gezeigten Muster: nenne seinen Dienst (" + ", ".join(sorted(services)) + ")"
    return ""
