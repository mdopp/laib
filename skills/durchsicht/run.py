"""durchsicht: the log review (#550, #551, #839), the deterministic part.

Groups every service's warnings and errors of a day by pattern, by content,
never by the journal's priority. A pattern is checked as a person would
(#975): a line in the window of a maintenance marker of its service or of
one it depends on — announced before the restart (laib planned, #978) —
is only counted; a new pattern quiet for six hours or gone since a planned
restart of a green service is over; a certificate obtained after its
failures is resolved.
The model sees what is left: new, recurring or growing. docs/SKILLS.md.
"""

import re
from collections import namedtuple
from datetime import datetime, timedelta, timezone

import laibskill
from laibskill import field, hm, when
from lib import logzeilen

REVIEW_LINES, QUIET = 5000, timedelta(hours=6)  # lines per unit and day ("mindestens" when reached); a new pattern this quiet is over
IDENT, OBTAINED = re.compile(r'"identifier":"([^"]+)"'), "certificate obtained successfully"
MARK = "Aussortiert, kein Wunsch:"  # the morning report finds the sentence by it
Window = namedtuple("Window", "svc label stop start frm to")


def harmless(s):
    """ignore.yml's logs (laib ignore --log, #915): the shapes the owner or a
    ticket answered as harmless, per service; and what to say about it."""
    r = s.call("laib_state", args="ignore.yml")
    if r.error and "is not a file on main" in r.text:
        return {}, ""
    d = r.json()
    if not isinstance(d, dict) or not isinstance(d.get("data", {}), (dict, type(None))):
        return {}, "ignore.yml nicht lesbar (" + laibskill.first_line(r.text) + "); als harmlos Vermerktes ist mitgezählt."
    marks, bad = {}, 0
    for e in ((d.get("data") or {}).get("logs") or []):
        try:
            rx = re.compile(e["match"]) if isinstance(e, dict) and e.get("match") and str(e.get("reason", "")).strip() else None
        except (re.error, TypeError):
            rx = None
        if rx is None:
            bad += 1
            continue
        marks.setdefault(str(e.get("service", "")), []).append((rx, str(e["reason"])))
    return marks, (f"ignore.yml: {bad} Einträge unter logs ohne gültiges Muster oder Grund; sie gelten nicht." if bad else "")


class Plan:
    """The maintenance markers that held a service — its own and those of what
    it depends on — as Laib answers them (laib planned, #978), asked once per service."""

    def __init__(self, s):
        self.s, self.now, self.asked, self.unread, self.expected = s, when(s.now) or datetime.now(timezone.utc), {}, set(), {}

    def of(self, svc):
        if svc not in self.asked:
            ws = field(self.s.call("laib_planned", service=svc).json(), "windows", None)  # its maintenance markers (#978)
            if not isinstance(ws, list):
                self.unread.add(svc)
            ws = [Window(", ".join(field(w, "services", [])), field(w, "label"), *(when(field(w, k) or field(w, "until")) for k in ("since", "end", "from", "to")))
                  for w in ws or [] if isinstance(w, dict)]
            self.asked[svc] = [w for w in ws if None not in w]
        return self.asked[svc]

    def during(self, svc, unit, t):
        """Counts a line inside a planned stop's window as expected; True when it was."""
        w = max((w for w in self.of(svc) if t and w.frm <= t <= w.to), key=lambda w: (w.stop <= t, -abs((w.stop - t).total_seconds())), default=None)
        if w:
            e = self.expected.setdefault(unit, [0, set()])
            e[0] += 1
            e[1].add((w.stop, f"{w.label} von {w.svc} {hm(w.stop, self.now)}"))
        return bool(w)


def judge(c, new, plan, green, obtained):
    """Why a pattern is no wish, or "" and what was checked."""
    last, ids, now = max(c["times"], default=None), c["ids"], plan.now
    w = max(plan.of(c["svc"]), key=lambda w: w.start, default=None)
    if ids and all(obtained.get(i) and obtained[i] > t for i, t in ids.items()):
        return f"behoben: Zertifikat für {', '.join(sorted(ids))} danach ausgestellt ({hm(max(obtained[i] for i in ids), now)})", ""
    if new and last and now - last > QUIET:
        return f"vorbei: zuletzt {hm(last, now)}, seit über {QUIET.seconds // 3600} Stunden nicht mehr", ""
    if new and last and w and w.start > last and green:
        return f"vorbei: zuletzt {hm(last, now)}, seit {w.label} von {w.svc} um {hm(w.start, now)} nicht mehr, Dienst grün", ""
    said = ["geplante Neustarts nicht geprüft (nicht lesbar)" if c["svc"] in plan.unread else "nicht während eines geplanten Neustarts",
            f"zuletzt {hm(last, now)} (vor {int((now - last).total_seconds() // 60)} min)" if last else "Zeitpunkt nicht lesbar"]
    if w and last:
        said.append(f"{'auch nach' if w.start <= last else 'vor'} {w.label} von {w.svc} um {hm(w.start, now)}" + ("" if green or w.start <= last else ", Dienst nicht grün"))
    else:
        said.append("kein geplanter Neustart")
    return "", ", ".join(said + ["noch aktuell" if new else "schon in der letzten Durchsicht"])


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
    if "nicht geprüft" in saw:  # laib planned unreadable or not granted yet (#978): a note, no wish
        return "geplante Neustarts nicht geprüft: kein Wunsch, nur die Notiz"
    return ""


def recheck(s, plan, kept, quiet, read):
    """#978: the Operator's open wishes about the services' logs, a day after
    they were filed: no error line of their services → erledigt; only lines
    today's checks drop → verworfen, with why. Two calls; the morning report says it."""
    ws, out = s.call("laib_wish", args="list").json(), []
    for w in ws if isinstance(ws, list) else []:
        svcs, since = field(w, "services", []) or [], when(field(w, "since"))
        if not field(w, "operator") or field(w, "skill") not in ("", "durchsicht") or not svcs or not set(svcs) <= read or kept & set(svcs) or not since or plan.now - since < timedelta(hours=20):
            continue
        why = next((quiet[x] for x in svcs if x in quiet), "")
        r = max((v for x in svcs for v in plan.of(x) if v.start > since), key=lambda v: v.start, default=None)
        done = f"seit {r.label} von {r.svc} um {hm(r.start, plan.now)} kein Fehler mehr" if r else f"kein Fehler von {', '.join(svcs)} in 24 Stunden"
        out.append(f"{field(w, 'id')} " + (f"verworfen würde heute nicht gemeldet: {why}" if why else f"erledigt {done}"))
    return len(out) if out and not s.call("laib_wish", args="status", stdin="\n".join(out)).error else 0


def run(s):
    st = s.call("laib_status")
    services = field(st.json(), "services", None)
    if not isinstance(services, list):
        return laibskill.done(context="Protokoll-Durchsicht: laib status nicht lesbar (" + laibskill.first_line(st.text) + "). Schreib das in die Notiz.")
    counts, noise, full, unread, runtime, known_harmless, obtained, green, quiet = {}, {}, {}, [], {}, {}, {}, {}, {}
    (marks, marks_said), plan = harmless(s), Plan(s)
    for name in (field(x, "name") for x in services):
        green[name] = any(field(x, "name") == name and field(x, "state") == "grün" for x in services)
        units = s.call("laib_logs", args=f"{name} --since 24h --lines {REVIEW_LINES} --priority warning").json()
        if not isinstance(units, dict) or not all(t is None or isinstance(t, str) for t in units.values()):
            unread.append(name)
            continue
        for unit, text in units.items():
            lines = (text or "").strip().split("\n")
            full[unit] = len(lines) >= REVIEW_LINES
            for m in filter(None, map(logzeilen.LINE.match, lines)):
                if m[2] in logzeilen.RUNTIME:
                    runtime[unit] = runtime.get(unit, 0) + 1
                    quiet.setdefault(name, "Zeilen der Container-Laufzeit (pasta, conmon), nicht des Dienstes")
                    continue
                t, ident = when(m[1]), IDENT.search(m[3])
                if ident and t and OBTAINED in m[3]:
                    obtained[ident[1]] = t
                why = next((why for rx, why in marks.get(name, []) if rx.search(m[3])), None)
                if why is not None:
                    known_harmless.setdefault(unit, [0, why])[0] += 1
                    quiet.setdefault(name, f"als harmlos vermerkt ({why})")
                    continue
                p, real = logzeilen.pattern(m[3])
                if not real:
                    noise[unit] = noise.get(unit, 0) + 1
                    continue
                if plan.during(name, unit, t):
                    quiet.setdefault(name, "nur während geplanter Neustarts")
                    continue
                c = counts.setdefault(unit + "\t" + p, {"unit": unit, "svc": name, "pattern": p, "example": m[3], "count": 0, "times": [], "ids": {}})
                c["count"] += 1
                c["times"] += [t] if t else []
                if ident and t:
                    c["ids"][ident[1]] = t

    last = s.state if isinstance(s.state, dict) else {}
    news, known, dropped, checked = [], 0, [], []
    for key, c in counts.items():
        prev = last.get(key)
        if prev is None and c["count"] >= 3:
            news.append((c, "neu"))
        elif prev is not None and not prev.get("told"):
            news.append((c, "wiederkehrend"))
        elif prev is not None and c["count"] >= 2 * prev.get("count", 0) and c["count"] >= prev.get("count", 0) + 10:
            news.append((c, f"wächst (vorher {prev.get('count', 0)}×)"))
        elif prev is not None:
            known += 1
    for c, why in news:
        gone, said = judge(c, why == "neu", plan, green.get(c["svc"]), obtained)
        if gone:
            dropped.append((c, gone))
            quiet.setdefault(c["svc"], gone)
        else:
            checked.append((c, why, said))
    checked = sorted(checked, key=lambda n: (-n[0]["count"], n[0]["unit"] + n[0]["pattern"]))[:8]
    shown = {c["unit"] + "\t" + c["pattern"] for c, _, _ in checked}
    nxt = {key: {"count": c["count"], "told": bool((last.get(key) or {}).get("told")) or key in shown} for key, c in counts.items()}
    aside = told(dropped, plan)
    kept = {c["svc"] for c in counts.values() if not any(c is d for d, _ in dropped)}  # a real error today, not dropped
    closed = recheck(s, plan, kept, quiet, set(green) - set(unread))
    if not checked and not unread:
        if aside:
            s.call("laib_note", kind="watch", text="Protokoll-Durchsicht: keine neuen Fehlermuster. " + aside)
        return laibskill.done(outcome="unchanged", why="keine neuen Fehlermuster" + (f"; {len(dropped)} Muster aussortiert" if dropped else "") + (f"; {closed} Wünsche geschlossen" if closed else ""), state=nxt)

    out = ["Fehlermuster der letzten 24 Stunden (nach Inhalt, nicht nach Journal-Priorität), jedes geprüft wie von einem Menschen:"]
    for c, why, said in checked:
        ex = c["example"] if len(c["example"]) <= 200 else c["example"][:200] + "…"
        out.append(f"- {why}, {c['unit']}, {'mindestens ' if full.get(c['unit']) else ''}{c['count']}×: {c['pattern']}\n  Beispiel: {ex}\n  geprüft: {said}")
    out += [f"Schon gemeldet und nicht gewachsen: {known} Muster (nicht wiederholen)."] if known else []
    out += ["Geplante Neustarts, Vorbeis und Behobenes sind aussortiert; die Notiz bekommt die Liste angehängt, kein Wunsch dazu."] if aside else []
    out += ["Logs nicht lesbar: " + ", ".join(unread) + "."] if unread else []
    for title, d in (("Rauschen (stderr ohne Fehler, nur gezählt)", noise), ("Container-Laufzeit (pasta, conmon; nicht der Dienst, nur gezählt)", runtime)):
        if d:
            out.append(title + ": " + ", ".join(f"{u} {d[u]}" for u in sorted(d, key=lambda u: (-d[u], u))[:4]) + ".")
    if known_harmless:
        out.append("Als harmlos vermerkt (ignore.yml, nur gezählt): " + ", ".join(f"{u} {n} ({why})" for u, (n, why) in sorted(known_harmless.items())) + ".")
    out += [marks_said] if marks_said else []
    s.services = {c["svc"] for c, _, _ in checked}
    return laibskill.done(context="\n".join(out) + "\n", human=aside, state=nxt, checks=["laib_wish"])
