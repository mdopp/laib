"""laibskill.py, a skill script's one door (#839): the channel to the runner
and the guard. The Go side, with the real runner, is operator/script839_test.go."""

import json
import os
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))


def session(script, replies):
    """Runs script through laibskill.py as the runner does and answers its
    calls in order; returns what it asked for and its result."""
    with tempfile.TemporaryDirectory() as d:
        skills = os.path.join(d, "skills")
        os.makedirs(os.path.join(skills, "probe"))
        with open(os.path.join(HERE, "laibskill.py")) as f, open(os.path.join(skills, "laibskill.py"), "w") as g:
            g.write(f.read())
        with open(os.path.join(skills, "probe", "run.py"), "w") as f:
            f.write(script)
        p = subprocess.Popen([sys.executable, "-I", "-S", "-B", "-X", "utf8", os.path.join(skills, "laibskill.py"), os.path.join(skills, "probe", "run.py")],
                             stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, env={}, cwd=skills)
        p.stdin.write(json.dumps({"skill": {"name": "probe", "granted": ["note"]}, "trigger": {"kind": "tick"}, "state": {"n": 1}, "now": "2026-10-27T06:30:00Z"}) + "\n")
        p.stdin.flush()
        asked = []
        while True:
            msg = json.loads(p.stdout.readline())
            if "done" in msg:
                p.stdin.close()
                p.wait(10)
                return asked, msg["done"], p.stderr.read()
            asked.append(msg)
            p.stdin.write(json.dumps(replies.pop(0)) + "\n")
            p.stdin.flush()


class Channel(unittest.TestCase):
    def test_a_call_and_the_result(self):
        asked, done, err = session('''
import laibskill
def run(s):
    print("to stderr, not the channel")
    r = s.call("laib_note", kind="watch", text="x")
    return laibskill.done(outcome="green", why=f"{s.skill['name']} {s.trigger['kind']} {s.state['n']} {r.text} {r.refused}")
''', [{"text": "nicht ausgeführt", "error": True, "refused": True}])
        self.assertEqual(asked, [{"call": "laib_note", "args": {"kind": "watch", "text": "x"}}])
        self.assertEqual((done["outcome"], done["why"]), ("green", "probe tick 1 nicht ausgeführt True"))
        self.assertIn("to stderr", err)

    def test_the_guard(self):
        _, done, _ = session('''
import os, laibskill
def tries(f):
    try:
        f()
        return "open"
    except laibskill.Refused:
        return "refused"
def run(s):
    out = [tries(lambda: __import__("socket")), tries(lambda: __import__("ssl")), tries(lambda: __import__("ctypes")),
           tries(lambda: open("/etc/hostname").read()), tries(lambda: os.open("/tmp/x", os.O_WRONLY | os.O_CREAT)),
           tries(lambda: os.kill(os.getppid(), 0)), tries(lambda: os.remove(__file__)), tries(lambda: __import__("json").dumps({}))]
    return laibskill.done(note=" ".join(out))
''', [])
        self.assertEqual(done["note"], "refused refused refused refused refused refused refused open")


if __name__ == "__main__":
    unittest.main()
