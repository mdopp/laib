#!/usr/bin/env python3
"""werkstatt-docprobe (#1169): a doc line the box can answer carries its
probe, and this pass writes the answer and its date next to it.

Two forms in README.md and docs/*.md (scripts/drift.sh fails on a line that
says „auf der Box zu prüfen“ or „unverified“ without one of them):

- the box answers it: an HTML comment after the claim, one read-only `laib`
  command and what its output must hold, at least one `has`:

      <!-- box: laib backups ~ "LastGreen": "(\d{4}-\d\d-\d\d) !~ "Offsite": "0001 -->

  `~ <re>` must match the output, `!~ <re>` must not (Python regexes, no
  " ~ " inside; in a table row no "|" either). The pass writes
  ` *(box 2026-11-11: yes, 2026-11-10)*` right after it: yes when the
  command exits 0 with output, every `~` matches and no `!~` does (a group
  in a `~` is shown); no when one fails; not measured when the command
  exits non-zero or says nothing. A `!~` alone could not go red on an empty
  read, hence at least one `~`.
- only a person answers it: `Box-Aufgabe #N`. A new one is written as
  `<!-- aufgabe: <Titel> | <warum> -->` (once; elsewhere the title alone);
  the pass adds it for the admins (`laib aufgabe add`, or the open task
  with that title) and puts `Box-Aufgabe #N` in its place.

A form right after a backtick is an example (docs/TESTING.md) and is left alone.

One pass, at most every seven days (--now: at once): a fresh clone of main,
the probes, one pull request from bot/boxfragen with the answers, the dates
and the new task numbers (rebuilt every pass, like scripts/architektur-pr.sh).
A no, a not measured, and a Box-Aufgabe the docs name that is done opens
one issue, or comments on the open one (a marker in its body). Output of a
probe never goes into an issue: it may name people.

  werkstatt-docprobe [--now]     one pass (the entrypoint calls it hourly)
  werkstatt-docprobe --check DIR every marker under DIR parses (CI)
"""
import datetime, hashlib, json, os, re, shlex, shutil, subprocess, sys, tempfile

REPO = os.environ.get("DOCPROBE_REPO", "mdopp/laib")
GH = os.environ.get("DOCPROBE_GH", "gh")
LAIB = os.environ.get("DOCPROBE_LAIB", "laib")
SEEN = os.environ.get("DOCPROBE_STATE") or os.path.expanduser("~/.werkstatt/docprobe.json")
READ_VERBS = {"status", "diag", "look", "logs", "read", "state", "net", "version", "inspect", "journal",
              "context", "backups", "befunde", "auslastung", "planned", "inbox"}
BOX = re.compile(r"(?<!`)<!-- box: (.+?) -->(?: \*\(box \d{4}-\d{2}-\d{2}: [^)]*\)\*)?")
NEW_TASK = re.compile(r"(?<!`)<!-- aufgabe: (.+?) -->")
TASK_REF = re.compile(r"Box-Aufgabe #(\d+)")
BRANCH = "bot/boxfragen"
MARK = "<!-- laib-docprobe {} -->"
EVERY = datetime.timedelta(days=7)


def run(argv, timeout=120, cwd=None):
    try:
        p = subprocess.run(argv, capture_output=True, text=True, timeout=timeout, cwd=cwd)
        return p.returncode, (p.stdout + p.stderr).strip()
    except (OSError, subprocess.TimeoutExpired) as e:
        return 124, str(e)


def parse(spec):
    """'laib <verb> … ~ <re> !~ <re>' → (argv, has, nots); ValueError when it is no read-only probe"""
    cmd, *parts = re.split(r" (!?~) ", spec)
    argv = shlex.split(cmd)
    if len(argv) < 2 or argv[0] != "laib" or not (argv[1] in READ_VERBS or argv[1:3] == ["template", "list"]):
        raise ValueError(f"not a read-only laib command: {cmd}")
    has, nots = [], []
    for op, rx in zip(parts[::2], parts[1::2]):
        (has if op == "~" else nots).append(re.compile(rx))
    if not has:
        raise ValueError(f"no '~ <re>': a probe of absences alone is green on an empty read: {spec}")
    return argv, has, nots


def measure(spec):
    """(answer for the doc, reason for an issue); answer starts with yes, no or not measured"""
    argv, has, nots = parse(spec)
    code, out = run([LAIB, *argv[1:]])
    if code or not out.strip():
        return "not measured", f"`{shlex.join(argv)}` exit {code}" + ("" if out.strip() else ", no output")
    seen = []
    for rx in has:
        m = rx.search(out)
        if not m:
            return "no", f"`{shlex.join(argv)}`: nothing matches `{rx.pattern}`"
        seen += [g for g in m.groups() if g]
    for rx in nots:
        if rx.search(out):
            return "no", f"`{shlex.join(argv)}`: `{rx.pattern}` matches"
    shown = ", ".join(re.sub(r"[()*\n]", "", g)[:60] for g in seen)
    return ("yes, " + shown if shown else "yes"), ""


def docs(root):
    out = [os.path.join(root, "README.md")]
    d = os.path.join(root, "docs")
    out += sorted(os.path.join(d, f) for f in os.listdir(d) if f.endswith(".md")) if os.path.isdir(d) else []
    return [f for f in out if os.path.isfile(f)]


def check(root):
    bad = 0
    for f in docs(root):
        with open(f, encoding="utf-8") as fh:
            lines = fh.read().splitlines()
        for n, line in enumerate(lines, 1):
            for m in BOX.finditer(line):
                try:
                    parse(m[1])
                except (ValueError, re.error) as e:
                    print(f"{os.path.relpath(f, root)}:{n}: {e}")
                    bad = 1
            for m in NEW_TASK.finditer(line):
                if not m[1].partition(" | ")[0].strip():
                    print(f"{os.path.relpath(f, root)}:{n}: an aufgabe without a title")
                    bad = 1
    return bad


def tasks():
    code, out = run([LAIB, "state", "aufgaben.yml"])
    try:
        return {a["id"]: a for a in json.loads(out)["data"]["aufgaben"]} if code == 0 else None
    except (ValueError, KeyError, TypeError):
        return None


def new_tasks(texts, known):
    """title → number for every <!-- aufgabe: --> in texts; the ones with a warum are added first"""
    specs = sorted({m.strip() for t in texts for m in NEW_TASK.findall(t)}, key=lambda s: " | " not in s)
    ids = {}
    for spec in specs:
        titel, _, warum = (x.strip() for x in spec.partition(" | "))
        if titel in ids:
            continue
        ids[titel] = next((a["id"] for a in known.values() if a.get("titel") == titel and not a.get("erledigt")), None)
        if ids[titel] is None:
            code, out = run([LAIB, "aufgabe", "add", titel, *(["--warum", warum] if warum else []), "--fuer", "admins"])
            m = re.search(r"#(\d+)", out)
            if code or not m:
                raise RuntimeError(f"laib aufgabe add {titel!r}: exit {code}")
            ids[titel] = int(m[1])
            known[ids[titel]] = {"id": ids[titel], "titel": titel}
    return ids


def answer(root, today):
    """rewrites the docs; returns (rows for the PR, red items for issues)"""
    cache, rows, red, known = {}, [], [], tasks()
    ids = new_tasks([open(f, encoding="utf-8").read() for f in docs(root)], known) if known is not None else {}
    for f in docs(root):
        rel, text = os.path.relpath(f, root), open(f, encoding="utf-8").read()

        def box(m):
            spec = m[1]
            if spec not in cache:
                cache[spec] = measure(spec)
            got, why = cache[spec]
            rows.append(f"| `{rel}` | `{re.split(r' !?~ ', spec)[0]}` | {got} |")
            if not got.startswith("yes"):
                key = hashlib.sha1(f"{rel} {spec}".encode()).hexdigest()[:12]
                red.append((key, f"docs: the box answers „{got}“ to a line in {rel}",
                            f"`{rel}` carries the probe `<!-- box: {spec} -->`. On {today} the answer is **{got}**: {why}.\n\n"
                            "Either the box is not what the doc says (fix the box or the doc), or the probe is wrong (fix the probe). "
                            "The probe's output is not copied here; run the command in the Werkstatt."))
            return f"<!-- box: {spec} --> *(box {today}: {got})*"

        def task(m):
            titel = m[1].partition(" | ")[0].strip()
            if titel not in ids:
                return m[0]
            rows.append(f"| `{rel}` | new Box-Aufgabe #{ids[titel]} | {titel} |")
            return f"Box-Aufgabe #{ids[titel]}"

        new = NEW_TASK.sub(task, BOX.sub(box, text))
        for n in sorted({int(x) for x in TASK_REF.findall(new)}):
            done = (known or {}).get(n, {}).get("erledigt")
            if done:
                red.append((f"aufgabe-{n}-{rel}", f"docs: Box-Aufgabe #{n} is done, {rel} still waits for it",
                            f"Box-Aufgabe #{n} („{known[n].get('titel', '')}“) was ticked off on {done.get('am', '?')}. "
                            f"`{rel}` still names it: write its answer into the doc (a probe if the box can now answer it) and drop the number."))
        if new != text:
            open(f, "w", encoding="utf-8").write(new)
    return rows, red


def issues(red):
    code, out = run([GH, "issue", "list", "-R", REPO, "--state", "open", "--limit", "300", "--json", "number,body"])
    if code:
        raise RuntimeError(f"gh issue list: {out[:200]}")
    open_ = json.loads(out or "[]")
    for key, title, body in red:
        mark = MARK.format(key)
        n = next((i["number"] for i in open_ if mark in (i.get("body") or "")), None)
        argv = ([GH, "issue", "comment", str(n), "-R", REPO, "--body", body] if n else
                [GH, "issue", "create", "-R", REPO, "--title", title, "--body", body + "\n\n" + mark])
        code, out = run(argv)
        if code:
            raise RuntimeError(f"gh issue {'comment' if n else 'create'}: {out[:200]}")


def pull_request(root, rows, today):
    git = lambda *a: run(["git", "-c", "user.name=werkstatt-docprobe", "-c", "user.email=werkstatt@laib.invalid",
                          "-c", "credential.helper=", "-c", f"credential.helper=!{GH} auth git-credential", *a], cwd=root)
    if git("diff", "--quiet")[0] == 0:
        return
    for args in (("switch", "-q", "-C", BRANCH), ("commit", "-qam", f"docs: the box's answers of {today}"),
                 ("push", "-q", "--force", "origin", BRANCH)):
        code, out = git(*args)
        if code:
            raise RuntimeError(f"git {args[0]}: {out[:200]}")
    code, out = run([GH, "pr", "list", "-R", REPO, "--head", BRANCH, "--state", "open", "--json", "number", "--jq", ".[0].number // empty"])
    if code == 0 and out.strip():
        print(f"werkstatt-docprobe: updated #{out.strip()}")
        return
    body = (f"The Werkstatt's `werkstatt-docprobe` (#1169) ran the docs' box probes on {today}: read-only `laib` commands, "
            "the answer and its date next to each claim, and the number of each new Box-Aufgabe. A no or a not measured "
            "has its own issue. This branch is main plus that one commit; the next pass rebuilds it.\n\n"
            "| Doc | Probe | Answer |\n|---|---|---|\n" + "\n".join(rows))
    code, out = run([GH, "pr", "create", "-R", REPO, "--base", "main", "--head", BRANCH,
                     "--title", f"docs: the box's answers of {today}", "--body", body])
    if code:
        raise RuntimeError(f"gh pr create: {out[:200]}")


def gh_login():
    """gh's own login in the kept home first; else the container's PAT (as werkstatt-boxcheck)"""
    if os.environ.get("GH_TOKEN") or os.path.exists(os.path.expanduser("~/.config/gh/hosts.yml")):
        return
    try:
        m = re.search(r"x-access-token:([^@]+)@", open(os.environ.get("DOCPROBE_GIT_CREDENTIALS", "/run/laib/git-credentials")).read())
    except OSError:
        return
    if m:
        os.environ["GH_TOKEN"] = m[1]


def main(argv):
    if argv[:1] == ["--check"]:
        return check(argv[1] if len(argv) > 1 else ".")
    now = datetime.datetime.now(datetime.timezone.utc)
    try:
        last = datetime.datetime.fromisoformat(json.load(open(SEEN))["at"])
    except (OSError, ValueError, KeyError, TypeError):
        last = None
    if "--now" not in argv and last and now - last < EVERY:
        return 0
    gh_login()
    root, tmp = os.environ.get("DOCPROBE_DIR"), None
    if not root:
        tmp = tempfile.mkdtemp(prefix="docprobe-")
        root = os.path.join(tmp, "laib")
        code, out = run([GH, "repo", "clone", REPO, root, "--", "-q", "--depth", "1"], timeout=300)
        if code:
            print(f"werkstatt-docprobe: clone: {out[:200]}", file=sys.stderr)
            return 1
    try:
        today = now.date().isoformat()
        rows, red = answer(root, today)
        pull_request(root, rows, today)
        issues(red)
    except RuntimeError as e:  # the next hour tries again
        print(f"werkstatt-docprobe: {e}", file=sys.stderr)
        return 1
    finally:
        if tmp:
            shutil.rmtree(tmp, ignore_errors=True)
    os.makedirs(os.path.dirname(SEEN), exist_ok=True)
    json.dump({"at": now.isoformat()}, open(SEEN, "w"))
    print(f"werkstatt-docprobe: {len(rows)} answers, {len(red)} open questions")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
