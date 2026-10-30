"""durchsicht: the log review (#550 point 6, #551), the deterministic part.

Reads every service's warnings and errors of a day, groups them by
pattern and keeps what the last review saw; the model sees only the real
patterns that are new, recurring or growing. The journal's err is stderr,
and most of it is a container's INFO, so a line's content decides, never
its priority. Moved out of the Operator's Go (operator/durchsicht.go, #839).
"""

import json
import re

import laibskill

REVIEW_LINES = 5000  # per unit and day; a unit that reaches it is "mindestens"

A = re.ASCII  # Go's regexp: \w, \d, \s and \b are ASCII
JOURNAL_LINE = re.compile(r"^\S+ \S+ ([^\s\[:]+)(?:\[\d+\])?: (.*)$", A)
# #915: rootless Podman's network layer and container monitor write into a
# service's unit; a flow the peer already closed is their line, not the
# service's error (#903)
RUNTIME = {"pasta", "passt", "conmon"}
# the level a line names: level=x, "level":"x", [x] ...
NAMED_LEVEL = re.compile(r'(?i)(?:\blevel=|"level":\s*"|\blvl=|\[)(trace|debug|dbg|info|inf|notice|warn|warning|wrn|error|err|eror|fatal|ftl|crit|critical|panic)\b', A)
# ... or as a word in capitals: ERROR (MainThread), INFO:, LOG [Api]
UPPER_LEVEL = re.compile(r"\b(TRACE|DEBUG|VERBOSE|INFO|NOTICE|LOG|WARN|WARNING|ERROR|FATAL|CRITICAL|PANIC)\b", A)
ERROR_WORDS = re.compile(r"(?i)\b(error|errors|fatal|panic|exception|failed|failure|timeout|timed out|refused|denied|unauthorized|not connected|aborted|aborting|segfault|cannot|unable)\b|\b[A-Z]\w*(Error|Exception)\b", A)
NORMALISE = [
    (re.compile(r"\[?\d{4}[-/]\d{2}[-/]\d{2}[T ]\d{2}:\d{2}:\d{2}(?:[.,]\d+)?(?:Z| ?[+-]\d{2}:?\d{2})?\]?", A), ""),
    (re.compile(r"\d{1,2}/\d{1,2}/\d{4},? \d{1,2}:\d{2}:\d{2}(?: [AP]M)?", A), ""),
    (re.compile(r"\[?\b\d{2}:\d{2}:\d{2}(?:[.,]\d+)?\]?", A), ""),
    (re.compile(r"(?:/[\w.@%+-]+){2,}/?", A), "<pfad>"),
    (re.compile(r"(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b", A), "<id>"),
    (re.compile(r"\b\d{1,3}(?:\.\d{1,3}){3}(?::\d+)?\b", A), "<ip>"),
    (re.compile(r"\b[0-9a-fA-F]{6,}\b", A), None),  # an id is hex with a digit and a letter
    (re.compile(r"\d+(?:\.\d+)?", A), "<n>"),
    (re.compile(r"\s+", A), " "),
]
NOISE_LEVELS = {"trace", "debug", "dbg", "verbose", "info", "inf", "notice", "log", "warn", "warning", "wrn"}


def _hex(m):
    s = m.group(0)
    if not any(c.isdigit() for c in s) or not any(c in "abcdef" for c in s.lower()):
        return s  # a number stays a number
    return "<id>"


def _json_line(msg):
    """A JSON line's level, logger, msg and error, matched without case as
    Go's encoding/json does; None when it is no such line."""
    try:
        j = json.loads(msg)
    except ValueError:
        return None
    if not isinstance(j, dict):
        return None
    got = {}
    for k, v in j.items():
        k = k.lower()
        if k in ("level", "logger", "msg", "error"):
            if v is not None and not isinstance(v, str):
                return None
            got[k] = v or ""
    return got if got.get("msg") else None


def log_pattern(msg):
    """A journal message's pattern and whether it is a real error: the level
    the line names decides; without one, its words do. A JSON line (Caddy)
    is its logger, message and error."""
    level = ""
    j = _json_line(msg) if msg.startswith("{") else None
    if j is not None:
        logger = j.get("logger", "")
        msg, level = (logger + ": " if logger else "") + j["msg"], j.get("level", "").lower()
        if j.get("error"):
            msg += " (" + j["error"] + ")"
    else:
        n, u = NAMED_LEVEL.search(msg), UPPER_LEVEL.search(msg)
        if n and (not u or n.start() <= u.start()):
            level = n.group(1).lower()
        elif u:
            level = u.group(1).lower()
    if level in NOISE_LEVELS or level == "" and not ERROR_WORDS.search(msg):
        return "", False
    for rx, repl in NORMALISE:
        msg = rx.sub(_hex if repl is None else repl, msg)
    msg = msg.strip()
    if len(msg) > 160:
        msg = msg[:160] + "…"
    return msg, True


def harmless(s):
    """The log shapes the owner or a ticket answered as harmless: ignore.yml's
    logs (laib ignore --log, #915), per service; and what to say about it."""
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


def run(s):
    st = s.call("laib_status")
    d = st.json()
    services = (d.get("services") or []) if isinstance(d, dict) else None
    if not isinstance(services, list):
        return laibskill.done(context="Protokoll-Durchsicht: laib status nicht lesbar (" + laibskill.first_line(st.text) + "). Schreib das in die Notiz.")
    counts, noise, full, unread = {}, {}, {}, []
    runtime, known_harmless = {}, {}
    marks, marks_said = harmless(s)
    for svc in services:
        name = svc.get("name", "") if isinstance(svc, dict) else ""
        units = s.call("laib_logs", args=f"{name} --since 24h --lines {REVIEW_LINES} --priority warning").json()
        if not isinstance(units, dict) or not all(t is None or isinstance(t, str) for t in units.values()):
            unread.append(name)
            continue
        for unit, text in units.items():
            lines = (text or "").strip().split("\n")
            full[unit] = len(lines) >= REVIEW_LINES
            for line in lines:
                m = JOURNAL_LINE.match(line)
                if not m:
                    continue
                if m.group(1) in RUNTIME:
                    runtime[unit] = runtime.get(unit, 0) + 1
                    continue
                why = next((why for rx, why in marks.get(name, []) if rx.search(m.group(2))), None)
                if why is not None:
                    h = known_harmless.setdefault(unit, [0, why])
                    h[0] += 1
                    continue
                p, real = log_pattern(m.group(2))
                if not real:
                    noise[unit] = noise.get(unit, 0) + 1
                    continue
                c = counts.setdefault(unit + "\t" + p, {"unit": unit, "pattern": p, "example": m.group(2), "count": 0})
                c["count"] += 1

    last = s.state if isinstance(s.state, dict) else {}
    news, known = [], 0
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
    news.sort(key=lambda n: (-n[0]["count"], n[0]["unit"] + n[0]["pattern"]))
    news = news[:8]
    shown = {c["unit"] + "\t" + c["pattern"] for c, _ in news}
    nxt = {key: {"count": c["count"], "told": bool((last.get(key) or {}).get("told")) or key in shown} for key, c in counts.items()}
    if not news and not unread:
        return laibskill.done(outcome="unchanged", why="keine neuen Fehlermuster", state=nxt)

    out = ["Fehlermuster der letzten 24 Stunden (nach Inhalt, nicht nach Journal-Priorität):"]
    for c, why in news:
        least = "mindestens " if full.get(c["unit"]) else ""
        ex = c["example"] if len(c["example"]) <= 200 else c["example"][:200] + "…"
        out.append(f"- {why}, {c['unit']}, {least}{c['count']}×: {c['pattern']}\n  Beispiel: {ex}")
    if known:
        out.append(f"Schon gemeldet und nicht gewachsen: {known} Muster (nicht wiederholen).")
    if unread:
        out.append("Logs nicht lesbar: " + ", ".join(unread) + ".")
    loud = sorted(noise, key=lambda u: (-noise[u], u))
    if loud:
        out.append("Rauschen (stderr ohne Fehler, nur gezählt): " + ", ".join(f"{u} {noise[u]}" for u in loud[:4]) + ".")
    if runtime:
        out.append("Container-Laufzeit (pasta, conmon; nicht der Dienst, nur gezählt): " + ", ".join(f"{u} {runtime[u]}" for u in sorted(runtime, key=lambda u: (-runtime[u], u))[:4]) + ".")
    if known_harmless:
        out.append("Als harmlos vermerkt (ignore.yml, nur gezählt): " + ", ".join(f"{u} {n} ({why})" for u, (n, why) in sorted(known_harmless.items())) + ".")
    if marks_said:
        out.append(marks_said)
    return laibskill.done(context="\n".join(out) + "\n", state=nxt)
