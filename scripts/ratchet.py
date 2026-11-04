#!/usr/bin/env python3
"""The ratchet: dead code, staticcheck findings, duplicates and complexity
only go down (owner, 2026-11-03: "Code sollte immer möglichst sauber sein").

Each tool's findings of today are written down in scripts/ratchet/<tool>.txt,
one line per finding: a number, then the finding without its line number
(lines move with every edit, a finding does not). CI fails when

- a finding is new, or its number went up (a second copy of the same
  message in a file; a function's complexity score), and when
- a finding went away or its number went down: then the baseline is lower
  than written, and this PR lowers it — run `scripts/ratchet.py --update`
  and commit what it writes.

`--update` writes today's findings; it refuses to write a new or a higher
one unless `--accept` is given. Accepting is the exception, as raising a
line cap is: the reason goes into the PR, and the owner says yes.

The tools, pinned (`go run <tool>@<version>`, nothing added to go.mod):

- deadcode, the whole program: every main package is a root. A function
  unreachable even from the tests is "dead"; one only the tests reach is
  "test-only" (production code nobody runs), except in a fake package
  (a directory ending in "fake"), which exists for the tests.
- staticcheck, its default checks plus U1000 (unused), without ST1005:
  the error strings here are German sentences shown to people, and a German
  sentence starts with a capital letter.
- dupl, production files, clones of DUPL_TOKENS syntax tokens or more.
- gocognit, production functions with a cognitive complexity over
  COGNIT_OVER (golangci-lint's default for the same check).
Production means: not a _test.go file, not testdata/, not a fake package.
"""
import json
import os
import re
import subprocess
import sys
from collections import Counter
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
BASELINE = ROOT / "scripts" / "ratchet"

DEADCODE = "golang.org/x/tools/cmd/deadcode@v0.52.0"
STATICCHECK = "honnef.co/go/tools/cmd/staticcheck@v0.8.2"
DUPL = "github.com/mibk/dupl@v1.1.0"
GOCOGNIT = "github.com/uudashr/gocognit/cmd/gocognit@v1.3.0"
CHECKS = "inherit,U1000,-ST1005"
DUPL_TOKENS = 75
COGNIT_OVER = 30


def run(args, ok=(0,), stdin=None):
    p = subprocess.run(args, cwd=ROOT, input=stdin, capture_output=True, text=True)
    if p.returncode not in ok:
        sys.exit(f"{' '.join(args[:3])} failed ({p.returncode}):\n{p.stderr}{p.stdout}")
    return p.stdout


def lines_of(out, pattern, tool):
    """Every line matches the tool's format, or the tool changed and the
    ratchet would count nothing: that is an error, never zero findings."""
    found = []
    for line in out.splitlines():
        if not line.strip():
            continue
        m = re.match(pattern, line)
        if not m:
            sys.exit(f"{tool}: a line the ratchet cannot read: {line}")
        found.append(m.groups())
    return found


DEAD = r"^(\S+?):\d+:\d+: unreachable func: (\S+)$"


def fake(path):
    return any(part.endswith("fake") for part in Path(path).parent.parts)


def deadcode():
    dead = {f"{f}: {fn}" for f, fn in lines_of(run(["go", "run", DEADCODE, "-test", "./..."]), DEAD, "deadcode")}
    unrun = {f"{f}: {fn}" for f, fn in lines_of(run(["go", "run", DEADCODE, "./..."]), DEAD, "deadcode")}
    found = Counter(f"dead {k}" for k in dead)
    for k in unrun - dead:
        if not fake(k.split(":")[0]):
            found[f"test-only {k}"] += 1
    return found


def staticcheck():
    found = Counter()
    out = run(["go", "run", STATICCHECK, "-checks", CHECKS, "-f", "json", "./..."], ok=(0, 1))
    for line in out.splitlines():
        d = json.loads(line)
        if d["code"] == "compile":
            sys.exit(f"staticcheck: does not compile: {d['message']}")
        f = os.path.relpath(d["location"]["file"], ROOT)
        found[f"{f}: {d['message']} ({d['code']})"] += 1
    return found


DUP = r"^(.+?):(\d+)-\d+: duplicate of (.+?):(\d+)-\d+$"


def dupl():
    files = [f for f in run(["git", "ls-files", "*.go"]).split()
             if not f.endswith("_test.go") and "testdata/" not in f and not fake(f)]
    out = run(["go", "run", DUPL, "-files", "-plumbing", "-t", str(DUPL_TOKENS)], stdin="\n".join(files))
    found = Counter()
    for a, la, b, lb in lines_of(out, DUP, "dupl"):
        if (a, int(la)) < (b, int(lb)):  # each pair is printed both ways
            found[f"{a} ~ {b}"] += 1
    return found


def gocognit():
    out = run(["go", "run", GOCOGNIT, "-over", str(COGNIT_OVER), "-ignore", r"_test\.go$|testdata/|fake/",
               "-f", "{{.Complexity}} {{.Pos.Filename}} {{.PkgName}}.{{.FuncName}}", "."], ok=(0, 1))
    return Counter({f"{f}: {fn}": int(n) for n, f, fn in lines_of(out, r"^(\d+) (\S+) (\S+)$", "gocognit")})


TOOLS = {"deadcode": deadcode, "staticcheck": staticcheck, "dupl": dupl, "gocognit": gocognit}
UNIT = {"gocognit": "functions over %d, the number is the score" % COGNIT_OVER,
        "dupl": "file pairs, the number is the cloned fragments of %d tokens or more" % DUPL_TOKENS}


def read(tool):
    base = Counter()
    p = BASELINE / f"{tool}.txt"
    if p.exists():
        for line in p.read_text().splitlines():
            if line and not line.startswith("#"):
                n, key = line.split(" ", 1)
                base[key] = int(n)
    return base


def write(tool, now):
    head = [f"# scripts/ratchet.py {tool}: {UNIT.get(tool, 'the number is how often')}",
            "# written by `scripts/ratchet.py --update`; lowered in the PR that fixes one, raised only with --accept",
            f"# total {len(now)}"]
    body = [f"{now[k]} {k}" for k in sorted(now)]
    (BASELINE / f"{tool}.txt").write_text("\n".join(head + body) + "\n")


def compare(base, now):
    """Return (worse, better): findings new or above their baseline, and
    findings gone or below it."""
    worse = sorted(k for k in now if now[k] > base.get(k, 0))
    better = sorted(k for k in base if now.get(k, 0) < base[k])
    return worse, better


def main(argv):
    update, accept = "--update" in argv, "--accept" in argv
    pick = [a for a in argv if not a.startswith("--")] or list(TOOLS)
    bad = 0
    for tool in pick:
        now, base = TOOLS[tool](), read(tool)
        worse, better = compare(base, now)
        print(f"{tool}: {len(now)} findings (baseline {len(base)})")
        if update:
            if worse and not accept:
                for k in worse:
                    print(f"  new or higher, not written: {now[k]} {k} (baseline {base.get(k, 0)})")
                print("  --update lowers only; --accept writes these too: the reason in the PR, the owner's yes")
                now = Counter({k: min(n, base[k]) for k, n in now.items() if k in base})
                bad = 1
            write(tool, now)
            continue
        for k in worse:
            print(f"::error title=ratchet {tool}::new or higher: {now[k]} {k} (baseline {base.get(k, 0)})")
        if worse:
            print(f"  fix it; if it must stay: scripts/ratchet.py --update --accept {tool}, the reason in the PR")
        if better:
            for k in better:
                print(f"  lower: {k} is {now.get(k, 0)}, the baseline says {base[k]}")
            print(f"::error title=ratchet {tool}::{len(better)} findings went down: lower the baseline in this PR: scripts/ratchet.py --update {tool}")
        bad |= bool(worse or better)
    return bad


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
