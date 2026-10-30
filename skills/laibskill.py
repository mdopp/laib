"""laibskill: a skill script's one door to the box (#839, ADR 0023).

The Operator runs a built-in skill's run.py in its container as

    python3 -I -S -B -X utf8 laibskill.py <skill folder>/run.py

with an empty environment. This module is __main__: before the script's
first line it shuts the doors a script has no business with (the network,
other processes, files outside the skills and the standard library, writing
anywhere) and moves print() to stderr, because stdout is the channel to the
runner. Then it calls the script's run(session) and hands back its result.

The channel is one JSON object per line. The runner sends the start
({"skill", "trigger", "state", "now", "data"}); the script sends {"call":
<tool>, "args": {...}} and reads {"text", "error", "refused"}; at the end it
sends {"done": {...}}. When done names checks, the script stays while the
model works: for each model call of such a tool the runner sends {"check":
{"tool", "args"}} and the script's check(s, tool, args) answers {"refuse":
<why or "">}. The model keeps its judgement; the script keeps the rules. Every call goes through the runner's MCP client in the
skill's name: the runner refuses a tool the kernel did not grant the skill
or one over its limit, and records the refusal; the kernel checks it again.
The script gets no token, no address and no file of the Operator.

The guard is an audit hook (PEP 578). It is no sandbox on its own: the
scripts are this repo's, reviewed and shipped in the image, and the runner
gives them no credential. It catches a script that reaches past its door.
"""

import json
import os
import sys

STDLIB = os.path.dirname(os.__file__)
HERE = os.path.dirname(os.path.abspath(__file__))
_WRITE = os.O_WRONLY | os.O_RDWR | os.O_CREAT | os.O_APPEND | os.O_TRUNC
# what a skill script never does: other processes, the network, native code,
# changing a file
_NEVER = ("socket.", "subprocess.", "os.system", "os.exec", "os.posix_spawn", "os.spawn", "os.fork",
          "os.forkpty", "os.kill", "os.killpg", "signal.pthread_kill", "pty.", "ctypes.", "os.remove",
          "os.rename", "os.rmdir", "os.mkdir", "os.chmod", "os.chown", "os.symlink", "os.link",
          "os.truncate", "os.utime", "os.chdir", "os.chroot", "shutil.", "urllib.", "http.", "ftplib.",
          "smtplib.", "telnetlib.", "nntplib.", "poplib.", "imaplib.", "webbrowser.", "sqlite3.", "mmap.")
_NO_IMPORT = {"socket", "_socket", "ssl", "_ssl", "ctypes", "_ctypes", "subprocess", "_posixsubprocess",
              "multiprocessing", "_multiprocessing", "asyncio", "select", "selectors", "pty", "fcntl"}


class Refused(Exception):
    """Raised by the guard: a skill script reached past its door."""


def _readable(path):
    if not isinstance(path, (str, bytes)):
        return False
    p = os.path.realpath(os.fsdecode(path))
    return any(p == root or p.startswith(root + os.sep) for root in (STDLIB, HERE))


def _guard(event, args):
    if event.startswith(_NEVER):
        raise Refused(f"{event}: ein Skript einer Fähigkeit erreicht die Box nur über seine Werkzeuge")
    if event == "import" and args[0].split(".")[0] in _NO_IMPORT:
        raise Refused(f"import {args[0]}: nicht für ein Skript einer Fähigkeit")
    if event == "open":
        path, mode, flags = args
        if isinstance(path, int):
            return  # an open file descriptor: stdin, stdout, stderr
        writes = (isinstance(mode, str) and any(c in mode for c in "wax+")) or (flags or 0) & _WRITE
        if writes or not _readable(path):
            raise Refused(f"open {path}: ein Skript liest nur seinen Ordner und die Standardbibliothek")
    if event in ("os.listdir", "os.scandir", "glob.glob") and not _readable(args[0] if args else "."):
        raise Refused(f"{event} {args[0] if args else ''}: nur der eigene Ordner")


class Reply:
    """A tool's answer: its text, whether it errored, whether the runner refused it."""

    def __init__(self, text="", error=False, refused=False):
        self.text, self.error, self.refused = text, error, refused

    def json(self):
        """The text as JSON, or None when it is an error or not JSON."""
        if self.error:
            return None
        try:
            return json.loads(self.text)
        except ValueError:
            return None


class Session:
    """One run of a skill: what started it, and its door to the box."""

    def __init__(self, inp, out):
        self._in, self._out = inp, out
        start = json.loads(inp.readline())
        self.skill = start.get("skill") or {}
        self.trigger = start.get("trigger") or {}
        self.state = start.get("state")
        self.now = start.get("now", "")
        self.data = start.get("data") or {}

    def call(self, tool, **args):
        """Calls a laib tool in the skill's name; the runner decides whether it may."""
        self._send({"call": tool, "args": args})
        line = self._in.readline()
        if not line:
            raise EOFError("the runner closed the channel")
        r = json.loads(line)
        return Reply(r.get("text", ""), bool(r.get("error")), bool(r.get("refused")))

    def _send(self, msg):
        self._out.write(json.dumps(msg) + "\n")
        self._out.flush()


def done(outcome="", why="", note="", context="", human="", state=None, checks=(), fallback="", trigger=None):
    """What a script hands back. outcome set (unchanged, idle, green): the
    session ends without a model; else the model sees context. human is
    added to the session's note; state is kept for the next run when the
    session does not fail or wait (None keeps the last); checks are the
    tools whose model calls the script's check() vets; fallback is the note
    the runner writes when the model fails (#858); trigger, when set, is the
    session's trigger from here on (vorfall's first aid marks it aided)."""
    return {"outcome": outcome, "why": why, "note": note, "context": context, "human": human, "state": state, "checks": list(checks), "fallback": fallback,
            "trigger": trigger}


def first_line(s):
    return s.split("\n", 1)[0]


def one_line(s):
    return " ".join(s.split())


def field(d, key, default=""):
    """A JSON object's field matched without case, as Go's encoding/json
    reads laib's answers."""
    if not isinstance(d, dict):
        return default
    if key in d:
        return d[key] if d[key] is not None else default
    for k, v in d.items():
        if k.lower() == key.lower():
            return v if v is not None else default
    return default


def main(script):
    out = os.fdopen(os.dup(1), "w", encoding="utf-8")
    os.dup2(2, 1)  # print() goes to stderr: stdout is the runner's channel
    inp = os.fdopen(os.dup(0), "r", encoding="utf-8")
    sys.modules["laibskill"] = sys.modules[__name__]
    sys.path.insert(0, HERE)
    code = compile(open(script, encoding="utf-8").read(), script, "exec")
    sys.addaudithook(_guard)  # from here on, the script's rules
    ns = {"__name__": "skill", "__file__": script}
    exec(code, ns)
    s = Session(inp, out)
    result = ns["run"](s)
    s._send({"done": result})
    if not result.get("checks"):
        return
    for line in inp:  # the model's calls of its checks, until the session ends
        c = json.loads(line)["check"]
        s._send({"refuse": ns["check"](s, c["tool"], c.get("args") or {}) or ""})


if __name__ == "__main__":
    main(sys.argv[1])
