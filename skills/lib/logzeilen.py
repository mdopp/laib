"""logzeilen: a service's journal lines as the skills read them (#551, #978).

What `laib logs` answers — {unit: lines}, each line "<time> <host>
<ident>[pid]: <message>" — and whether a message is a real error: the level
the line names decides, without one its words, never the journal's
priority (most of a container's stderr is INFO). durchsicht groups the
real errors by pattern; vorfall's first aid shows the last one.
"""

import json
import re

A = re.ASCII  # Go's regexp: \w, \d, \s and \b are ASCII
LINE = re.compile(r"^(\S+) \S+ ([^\s\[:]+)(?:\[\d+\])?: (.*)$", A)
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


def pattern(msg):
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


def last_error(units):
    """The newest message of laib logs' answer ({unit: lines}) that is a real
    error of the service's own, else its newest message, cut to 200
    characters; "" when there is none."""
    msgs = [m for text in (units or {}).values() if isinstance(text, str) for m in map(LINE.match, text.strip().split("\n")) if m and m[2] not in RUNTIME]
    msgs.sort(key=lambda m: m[1])
    real = [m[3] for m in msgs if pattern(m[3])[1]] or [m[3] for m in msgs]
    line = real[-1].strip() if real else ""
    return line[:200] + "…" if len(line) > 200 else line
