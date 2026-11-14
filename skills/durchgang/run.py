"""durchgang: a red run, handled the way a helper did by hand (owner, 2026-11-14).

laib applies every approval waiting as one run (Durchgang). A red run of
several is taken back and, since #1209, each approval applied again alone:
the green ones stay, the red ones go back, and a hook's precondition makes
its card wait for a Box-Aufgabe. The cards still said only why laib took
them back. This script waits until laib is through, reads the run's journal
line and the failing service's error lines around that minute, and asks the
model one question: the cause in one plain sentence, and what only the owner
can do. Then, all of it code: the cause goes onto the red cards and their
follow-ups (laib note --karten); a red card that touches no failing service
(a run that went back as a whole) is proposed again on its own, as it was;
and what only the owner can do becomes one Box-Aufgabe, unless laib filed
one for the card already. It proposes; it never approves, rolls back or
skips a consent: a proposal waits for whatever its class asks.
"""

import re
import time
from datetime import datetime, timedelta, timezone

import laibskill
from laibskill import field, first_line, hm, when
from lib import logzeilen

LOOKS, POLL = 48, 15  # laib's rollback may take twelve minutes: looks, and seconds between them
BEFORE = timedelta(minutes=5)  # the error lines read: from this long before the red line to a minute after
RUN = re.compile(r"^Freigaben (#\d+(?:, #\d+)*) \(")
ID = re.compile(r'(?:"id":\s*|#)([0-9]+)')  # a proposal's or a Box-Aufgabe's number in laib's answer
SET = re.compile(r"^services/([a-z0-9-]+)/values\.yml:([A-Z][A-Z0-9_]*)$", re.ASCII)
sleep = time.sleep  # a test replaces it


def red_run(s, tr):
    """The newest red run in the journal (an apply named „Freigaben #…“), or None."""
    if field(tr, "kind") != "manual" and "apply: " not in str(field(tr, "detail")):
        return None  # another red: vorfall's; laib-operator -once durchgang reads the newest run
    es = s.call("laib_journal", verb="apply", since="24h").json()
    reds = [e for e in es or [] if field(e, "result") == "red" and RUN.match(field(e, "reason"))] if isinstance(es, list) else []
    return reds[-1] if reds else None


def wait_back(s, run, ids):
    """Waits until laib is through with run: its rollback as a whole, or
    (#1209) each approval applied again alone and the run's end line; and no
    step left on its cards. Returns (whether what went back runs again: True,
    False, None when the journal does not say, the cards by id)."""
    for look in range(LOOKS):
        es = s.call("laib_journal", since="24h", limit="400").json()
        es = [e for e in es if isinstance(e, dict)] if isinstance(es, list) else []
        whole = [e for e in es if field(e, "verb") == "apply" and field(e, "reason") == "rollback " + run]
        end = any(field(e, "verb") == "rollback" and str(field(e, "detail")).startswith(f"Lauf {run} war rot") for e in es)
        alone = [e for e in es if field(e, "verb") == "apply" and field(e, "reason") in {f"rollback #{i}" for i in ids}]
        ps = s.call("laib_inbox", all=True, limit="60").json()
        cards = {field(p, "id"): p for p in ps if isinstance(p, dict)} if isinstance(ps, list) else {}
        if (whole or end) and all(not field(cards.get(i), "step") for i in ids) or look == LOOKS - 1:
            back = field(whole[-1], "result") == "green" if whole else all(field(e, "result") == "green" for e in alone) if end else None
            return back, cards
        sleep(POLL)


def services(p):
    """The services a card touches: its files, its update."""
    out = {f.split("/")[1] for f in field(p, "files", []) or [] if f.startswith("services/") and f.count("/") >= 2}
    return out | {u for u in [field(field(p, "intent", {}), "update")] if u}


def failing(red):
    """The services the run's red line names, in its order: „<service>: …“ per line."""
    out = []
    for line in str(field(red, "detail")).splitlines():
        svc = line.split(":", 1)[0].strip()
        if re.fullmatch(r"[a-z0-9-]+", svc, re.ASCII) and svc not in out:
            out.append(svc)
    return out


def errors_around(s, svc, at, now):
    """The service's real error lines from BEFORE the red line to a minute after, newest last."""
    mins = int((now - at + BEFORE).total_seconds() // 60) + 2
    units = s.call("laib_logs", service=svc, since=f"{mins}m", lines="400", grep="error|fail|denied|refused|exception|warn|fatal|critical").json()
    seen, out = set(), []
    for text in (units or {}).values() if isinstance(units, dict) else []:
        for m in map(logzeilen.LINE.match, str(text).splitlines()):
            t = when(m[1]) if m else None
            pat, real = logzeilen.pattern(m[3]) if t else ("", False)
            if real and at - BEFORE <= t <= at + timedelta(minutes=1) and m[2] not in logzeilen.RUNTIME and pat not in seen:
                seen.add(pat)
                out.append((t, m[3].strip()[:240]))
    return [line for _, line in sorted(out)[-15:]]


def judge(s, run, cards, red, lines):
    """The model's two lines: the cause, and what only the owner can do ("" for nothing)."""
    asked = [f"Ein Durchgang der Box endete rot: {run}. Laib hat ihn zurückgenommen und jede Freigabe allein noch einmal angewendet.", "Die Freigaben darin:"]
    asked += [f"- #{i}: {first_line(field(p, 'reason'))[:160]} (Dienste: {', '.join(sorted(services(p))) or '–'}; "
              f"{'zurückgenommen' if field(p, 'outcome') == 'rolled-back' else 'allein angewendet'})" for i, p in cards]
    asked += ["Was Laib beim Einrichten meldete:"] + str(field(red, "detail")).splitlines()[:15]
    asked += ["Fehlerzeilen der Dienste um diese Minute:"] + (lines or ["(keine)"])
    asked += ["Antworte in genau zwei Zeilen, deutsch, für den Besitzer, ohne Befehle, Unit- oder Dateinamen und ohne englischen Fehlertext:",
              "Ursache: <ein Satz: welcher Dienst was abgelehnt hat und warum>",
              "Besitzer: <ein Satz, was nur der Besitzer tun kann, damit es beim nächsten Mal geht> oder nein"]
    r = s.ask("\n".join(asked))
    got = {}
    for line in ("" if r.error else r.text).splitlines():
        k, _, v = line.partition(":")
        if k.strip().strip("*") in ("Ursache", "Besitzer") and v.strip():
            got[k.strip().strip("*")] = laibskill.one_line(v)[:300]
    owner = got.get("Besitzer", "")
    return got.get("Ursache", ""), "" if owner.lower().strip(" .") in ("", "nein", "nichts", "-") else owner


def again(s, p, guilty, done):
    """Proposes card p again on its own, as it was: its values and notes. Says what came of it."""
    i, intent = field(p, "id"), field(p, "intent", {}) or {}
    sets = [(SET.match(k), v) for k, v in (field(intent, "set", {}) or {}).items()]
    if not sets or not all(m for m, _ in sets) or any(field(intent, k) for k in ("at", "grant", "update")):
        return f"#{i} lässt sich nicht von selbst neu vorschlagen; das macht, wer ihn vorgeschlagen hat"
    vals = sorted(f"{m[1]}/{m[2]}={v}" for m, v in sets)
    if vals in done:
        return f"#{i} ist schon neu vorgeschlagen"
    notes = [f"{k}={v}" for k, v in sorted((field(p, "notes", {}) or {}).items())]
    why = f"{first_line(field(p, 'reason'))} (neu vorgeschlagen: #{i} wurde mit dem roten Durchgang von {guilty} zurückgenommen)"
    r = s.call("laib_propose", reason=why, sets=vals, **({"notes": notes} if notes else {}))
    m = ID.search(r.text or "")
    return f"#{i} ließ sich nicht neu vorschlagen: {first_line(r.text)}" if r.error or not m else f"#{i} als #{m[1]}"


def proposed_since(cards, first):
    """The value sets of the cards filed after the run's first: one already there is not filed again."""
    out = []
    for p in cards.values():
        if isinstance(field(p, "id"), int) and field(p, "id") > first and field(p, "status") != "rejected":
            sets = field(field(p, "intent", {}), "set", {}) or {}
            ms = [(SET.match(k), v) for k, v in sets.items()]
            if ms and all(m for m, _ in ms):
                out.append(sorted(f"{m[1]}/{m[2]}={v}" for m, v in ms))
    return out


def task(s, owner, cause):
    """One Box-Aufgabe for the owner, unless one with that title is open. Its number, or what came of it."""
    open_ = s.call("laib_aufgabe", action="list").json()
    titles = [field(a, "titel") for a in field(open_, "aufgaben", []) or [] if isinstance(a, dict)]
    if owner in titles:
        return "die Box-Aufgabe dazu ist schon offen"
    r = s.call("laib_aufgabe", action="add", titel=owner, warum=cause)
    m = ID.search(r.text or "")
    return f"die Box-Aufgabe #{m[1]} ist für dich" if m and not r.error else f"die Box-Aufgabe ließ sich nicht anlegen: {first_line(r.text)}"


def run(s):
    st = s.state if isinstance(s.state, dict) else {}
    red = red_run(s, s.trigger)
    if red is None:
        return laibskill.done(outcome="idle", why="kein roter Durchgang")
    run_ = field(red, "reason")[len("Freigaben "):]
    if run_ in st.get("done", []):
        return laibskill.done(outcome="idle", why="dieser Durchgang ist schon gelesen")
    ids = [int(x) for x in re.findall(r"#(\d+)", RUN.match(field(red, "reason"))[1])]
    back, cards = wait_back(s, run_, ids)
    now = when(s.now) or datetime.now(timezone.utc)
    at = when(field(red, "t")) or now
    bad = failing(red)
    mine = [(i, cards[i]) for i in ids if i in cards]
    red_ = [(i, p) for i, p in mine if field(p, "outcome") == "rolled-back"]  # since #1209 the green ones stay applied
    kept = [i for i, p in mine if field(p, "outcome") != "rolled-back"]
    waits = [i for i, p in red_ if field(p, "refiled") or str(field(p, "because")).startswith("wartet auf Box-Aufgabe")]
    guilty = [i for i, p in red_ if services(p) & set(bad)]
    lines = [f"{svc}: {line}" for svc in bad[:2] for line in errors_around(s, svc, at, now)]
    cause, owner = judge(s, "#" + ", #".join(map(str, ids)), mine, red, lines)
    said = [f"Laib hat jede Freigabe allein angewendet; angewendet ist {', '.join(f'#{i}' for i in kept)}"] if kept else []
    if not cause:
        cause = f"{', '.join(bad) or 'ein Dienst'} lief nach dem Einrichten nicht richtig; die Ursache konnte der Operator ohne Modell nicht lesen"
    innocent = [(i, p) for i, p in red_ if i not in guilty and i not in waits and not str(field(p, "because")).startswith("weil es auf einer Änderung aufbaut")]
    if innocent and back is not True:
        said.append("Die Rücknahme ist nicht grün im Journal; deshalb schlage ich nichts neu vor")
    elif innocent and not guilty:
        said.append("Welche Freigabe den Fehler brachte, ließ sich keiner zuordnen; deshalb schlage ich nichts neu vor")
    elif innocent:
        done = proposed_since(cards, max(ids))
        said.append("Unbeteiligt und einzeln neu vorgeschlagen: " + "; ".join(again(s, p, ", ".join(f"#{g}" for g in guilty), done) for _, p in innocent))
    if owner and waits:
        said.append(f"Nur du kannst das: {owner} (Laib hat die Box-Aufgabe dazu schon angelegt, die Karte wartet darauf)")
    elif owner:
        said.append(f"Nur du kannst das: {owner} ({task(s, owner, cause)})")
    follow = sorted({field(p, k) for _, p in red_ for k in ("restore", "forward") if field(p, k)})
    text = f"Durchgang #{', #'.join(map(str, ids))} um {hm(at, now)} rot: {cause.rstrip('.')}." + "".join(f" {x}." for x in said)
    r = s.call("laib_note", kind="incident", text=text, karten=",".join(str(i) for i in [i for i, _ in red_] + follow))
    st["done"] = (st.get("done", []) + [run_])[-20:]
    return laibskill.done(outcome="green", note=text, human=first_line(r.text) if r.error else "", state=st)
