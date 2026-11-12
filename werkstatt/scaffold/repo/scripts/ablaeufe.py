#!/usr/bin/env python3
"""Abläufe: every behaviour scenario has a test that names it (owner,
2026-11-12: "jedes Programm bekommt nummerierte Abläufe").

docs/ABLAEUFE.md holds the program's scenarios in plain German, each a
heading that starts with its id (`### L-12 Titel`, one prefix per repo),
then a few Wenn/Dann lines. A test names the scenario it proves with a
marker in a comment or in its name, each id with the fingerprint of the
scenario's text the test was written against:

    // Ablauf L-12 @3f9a     # Ablauf L-12 @3f9a, L-13 @c0de     test("ablauf:L-12 @3f9a …")

The word, then a blank or a colon, then one id or several joined by commas,
each followed by `@` and the first four hex digits of the sha256 of the
scenario's title and lines, whitespace collapsed (fingerprint()). CI fails on

- a scenario no test names, unless its id is in scripts/ablaeufe-baseline.txt;
- a baselined id that a test names by now, or that is no scenario any more:
  take it out of the baseline in the same PR;
- a baseline that grew against the merge base with origin/main (ABLAEUFE_BASE
  names another ref): the baseline only shrinks, a new scenario comes with
  its test. Where the base has no baseline yet, this one is the first;
- a test that names an id docs/ABLAEUFE.md does not have, and an id twice;
- a marker without the fingerprint, or with one that is not the scenario's
  text any more: the scenario changed, so re-read that test against it.

`--stempeln L-12` prints the fingerprint of L-12 and every marker naming it.
It writes nothing, on purpose: whoever updates a marker re-reads that test
against the new text first.

The tests are the tracked and untracked (not ignored) files whose path
matches a pattern of DEFAULT_TESTS, or of scripts/ablaeufe-tests.txt (one
pattern per line, replacing the defaults) when the repo has it. A pattern
without a slash matches the file name, one with a slash the whole path
(fnmatch, where * crosses slashes).

One source (laib#1170): laib's werkstatt/scaffold/repo/scripts/ablaeufe.py,
copied unchanged into every repo of the household (scripts/copies.sh).
Usage: scripts/ablaeufe.py [--list | --stempeln ID…]
"""
import fnmatch
import hashlib
import os
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
DOC = "docs/ABLAEUFE.md"
BASELINE = "scripts/ablaeufe-baseline.txt"
TESTS = "scripts/ablaeufe-tests.txt"
DEFAULT_TESTS = [
    "*_test.go",
    "test_*.py",
    "*_test.py",
    "*.spec.ts",
    "*.spec.js",
    "*.spec.tsx",
    "*.test.ts",
    "*.test.js",
    "*.test.tsx",
    "*/e2e/*.ts",
    "*/e2e/*.js",
    "*/src/test/*.kt",
    "*/src/androidTest/*.kt",
]
ID = r"[A-Z][A-Z0-9]{0,4}-[1-9][0-9]{0,3}"
STAMP = r"(?:\s*@\w*)?"
HEADING = re.compile(r"^#{2,6}\s+(" + ID + r")(?![\w-])\s*(.*)$")
MARKER = re.compile(r"(?<![A-Za-z])[Aa]blauf[ :]\s*(" + ID + STAMP + r"(?:\s*,\s*" + ID + STAMP + r")*)(?![\w-])")
NAMED = re.compile("(" + ID + r")(?:\s*@(\w*))?")


def fingerprint(text):
    """The first four hex digits of the sha256 of the text, whitespace collapsed."""
    return hashlib.sha256(" ".join(text.split()).encode("utf-8")).hexdigest()[:4]


def scenarios(text):
    """The doc's scenarios, id -> fingerprint of its title and lines, in
    order (fenced code skipped), and the problems: an id twice, a heading
    without a title."""
    found, problems, fenced, current = {}, [], False, None
    for n, line in enumerate(text.splitlines(), 1):
        if line.lstrip().startswith("```"):
            fenced, current = not fenced, None
            continue
        if fenced:
            continue
        m = HEADING.match(line)
        if line.startswith("#"):
            current = None
        if not m:
            if current is not None:
                current.append(line)
            continue
        i, title = m.group(1), m.group(2).strip()
        if i in found:
            problems.append(f"{DOC}:{n}: {i} is there twice: give the second one the next free number")
            continue
        if not title:
            problems.append(f"{DOC}:{n}: {i} has no title: one sentence after the id")
        current = found[i] = [title]
    return {i: fingerprint("\n".join(lines)) for i, lines in found.items()}, problems


def markers(text):
    """Every id a file names with the marker: (id, fingerprint or None, line)."""
    found = []
    for m in MARKER.finditer(text):
        line = text.count("\n", 0, m.start()) + 1
        found += [(i, stamp or None, line) for i, stamp in NAMED.findall(m.group(1))]
    return found


def baseline_ids(text):
    """The baseline's ids, one per line, # starts a comment; and the ones twice."""
    ids, twice = [], []
    for line in text.splitlines():
        word = line.split("#", 1)[0].strip()
        if word:
            (twice if word in ids else ids).append(word)
    return ids, twice


def git(*args):
    return subprocess.run(["git", "-C", str(ROOT), *args], capture_output=True, text=True)


def test_files(patterns):
    """The tracked and untracked, not ignored, files the patterns match."""
    out = git("ls-files", "--cached", "--others", "--exclude-standard", "-z")
    if out.returncode != 0:
        raise SystemExit(f"ablaeufe: git ls-files failed: {out.stderr.strip()}")
    files = []
    for path in sorted(set(filter(None, out.stdout.split("\0")))):
        name = path.rsplit("/", 1)[-1]
        if any(fnmatch.fnmatchcase(path if "/" in p else name, p) for p in patterns):
            files.append(path)
    return files


def named(files):
    """id -> the markers that name it: (file, line, fingerprint or None)."""
    where = {}
    for path in files:
        try:
            text = (ROOT / path).read_text(encoding="utf-8", errors="replace")
        except OSError:
            continue
        for i, stamp, line in markers(text):
            where.setdefault(i, []).append((path, line, stamp))
    return where


def places(found):
    return ", ".join(sorted({path for path, _, _ in found}))


def stale(prints, where):
    """The markers whose fingerprint is missing or not the scenario's text."""
    problems = []
    for i, found in where.items():
        want = prints.get(i)
        for path, line, stamp in found if want else []:
            if stamp is None:
                problems.append(f"{path}:{line}: `Ablauf {i}` has no fingerprint: re-read the test against {i}, then write `Ablauf {i} @{want}`")
            elif stamp != want:
                problems.append(f"{path}:{line}: {i} changed since this test named it (@{stamp}, now @{want}): re-read the test against {i}, then write `@{want}`")
    return problems


def base_baseline():
    """The baseline at the merge base: its ids, None when the base has no
    baseline file, or an error string when the base cannot be read."""
    ref = os.environ.get("ABLAEUFE_BASE", "origin/main")
    base = git("merge-base", "HEAD", ref)
    if base.returncode != 0:
        return f"no merge base with {ref} (fetch it, or set ABLAEUFE_BASE): the baseline's shrink-only check cannot run"
    shown = git("show", f"{base.stdout.strip()}:{BASELINE}")
    if shown.returncode != 0:
        return None
    return baseline_ids(shown.stdout)[0]


def read(rel):
    p = ROOT / rel
    return p.read_text(encoding="utf-8") if p.is_file() else None


def check(doc, base_text, where, before):
    """The problems, given the doc, the baseline's text, the markers the
    tests hold (id -> (file, line, fingerprint)) and the baseline at the
    merge base (ids, None, or an error string)."""
    prints, problems = scenarios(doc)
    ids = list(prints)
    base, twice = baseline_ids(base_text or "")
    problems += [f"{BASELINE}: {i} is there twice" for i in twice]
    problems += [
        f"{i} has no test: write the test that proves it, with `Ablauf {i} @{prints[i]}` in a comment or its name"
        for i in ids
        if i not in where and i not in base
    ]
    problems += [
        f"{places(where[i])}: names {i}, which {DOC} does not have: fix the id or write the scenario"
        for i in sorted(set(where) - set(ids))
    ]
    problems += stale(prints, where)
    return ids, base, problems + shrinks(ids, base, where, before)


def shrinks(ids, base, where, before):
    """What keeps the baseline shrinking: no id in it that is covered or gone,
    none that the merge base did not have."""
    problems = []
    for i in base:
        if i not in ids:
            problems.append(f"{BASELINE}: {i} is no scenario in {DOC}: take it out of the baseline")
        elif i in where:
            problems.append(f"{BASELINE}: {i} has a test now ({places(where[i])}): take it out of the baseline")
    if isinstance(before, str):
        problems.append(before)
    elif before is not None:
        problems += [f"{BASELINE}: {i} is new in the baseline: it only shrinks; write the test instead" for i in base if i not in before]
    return problems


def stempeln(doc, where, wanted):
    """What --stempeln prints: each id's fingerprint and the markers naming
    it, whether they match. It writes nothing."""
    prints = scenarios(doc)[0]
    out = []
    for i in wanted:
        if i not in prints:
            out.append(f"{i}: no scenario in {DOC}")
            continue
        out.append(f"{i} @{prints[i]}")
        for path, line, stamp in where.get(i, []):
            state = "matches" if stamp == prints[i] else f"@{stamp}: re-read, then update" if stamp else "no fingerprint: re-read, then add it"
            out.append(f"  {path}:{line}  {state}")
    return out


def main(argv):
    doc = read(DOC)
    if doc is None:
        print(f"ablaeufe: {DOC} is missing: every program writes its scenarios there")
        return 1
    patterns = DEFAULT_TESTS
    own = read(TESTS)
    if own is not None:
        patterns = [p.strip() for p in own.splitlines() if p.strip() and not p.lstrip().startswith("#")]
    where = named(test_files(patterns))
    if argv[:1] == ["--stempeln"]:
        print("\n".join(stempeln(doc, where, argv[1:] or list(scenarios(doc)[0]))))
        return 0
    ids, base, problems = check(doc, read(BASELINE), where, base_baseline())
    if "--list" in argv:
        for i in ids:
            print(f"{i}  {places(where.get(i, [])) or ('baselined' if i in base else 'NO TEST')}")
    covered = sum(1 for i in ids if i in where)
    for p in problems:
        print(f"::error title=ablaeufe::{p}" if os.environ.get("GITHUB_ACTIONS") else f"  {p}")
    print(f"ablaeufe: {len(ids)} scenarios, {covered} named by a test, {len(base)} baselined, {len(problems)} problems")
    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
