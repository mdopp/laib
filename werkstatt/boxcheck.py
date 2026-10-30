#!/usr/bin/env python3
"""werkstatt-boxcheck (#867): "done" is checked on the box after the merge.

Issues close at merge; the box gets the release at night. Once the box runs
a newer release than this script last saw (`laib version`), it reads every
release between the two, takes the issues those releases name that closed
since the older one, and keeps the ones with a box probe: an „Auf der Box:“
line in the body, or the label box-check. For each:

- a probe that is a read-only `laib` command in backticks on that line
  (`laib status beets`, `laib diag`, …) runs here; exit 0 is green, anything
  else red. Green comments the result, red comments it and reopens.
- anything else gets one comment „Auf der Box seit vX.Y, nicht automatisch
  prüfbar“.

It runs in the Werkstatt because it needs both hands: the laib MCP (read
verbs) and a GitHub login that may comment on and reopen issues; the box
has no GitHub token, and CI cannot reach the box. Each issue gets one
comment per release (a marker in the comment), so a rerun repeats nothing.
The first run only records the version: no backlog flood.

  werkstatt-boxcheck            one pass (the entrypoint runs it hourly)
"""
import json, os, re, shlex, subprocess, sys

REPO = os.environ.get("BOXCHECK_REPO", "mdopp/laib")
GH = os.environ.get("BOXCHECK_GH", "gh")
LAIB = os.environ.get("BOXCHECK_LAIB", "laib")
SEEN = os.environ.get("BOXCHECK_STATE") or os.path.expanduser("~/.werkstatt/boxcheck.json")
READ_VERBS = {"status", "diag", "look", "logs", "read", "state", "net", "version", "inspect", "journal", "context"}
MARK = "<!-- laib-boxcheck {} -->"


def run(argv, timeout=120):
    try:
        p = subprocess.run(argv, capture_output=True, text=True, timeout=timeout)
        return p.returncode, (p.stdout + p.stderr).strip()
    except (OSError, subprocess.TimeoutExpired) as e:
        return 124, str(e)


def gh_json(*args):
    code, out = run([GH, *args])
    if code:
        raise RuntimeError(f"gh {' '.join(args[:2])}: {out[:200]}")
    return json.loads(out or "null")


def semver(tag):
    m = re.match(r"v?(\d+)\.(\d+)\.(\d+)", tag or "")
    return tuple(map(int, m.groups())) if m else None


def probes(body):
    """the „Auf der Box:“ lines and the read-only laib commands on each"""
    out = []
    for line in (body or "").splitlines():
        i = line.lower().find("auf der box:")
        if i < 0:
            continue
        cmds = []
        for c in re.findall(r"`(laib [^`]+)`", line[i:]):
            try:
                argv = shlex.split(c)
            except ValueError:
                continue
            if len(argv) > 1 and argv[1] in READ_VERBS:
                cmds.append(argv)
        out.append((line.strip(), cmds))
    return out


def releases_between(old, new):
    rels = gh_json("release", "list", "-R", REPO, "--limit", "50", "--json", "tagName,publishedAt")
    lo, hi = semver(old), semver(new)
    since = next((r["publishedAt"] for r in rels if semver(r["tagName"]) == lo), "")
    tags = sorted((r["tagName"] for r in rels if (v := semver(r["tagName"])) and lo < v <= hi), key=semver)
    return tags, since


def check(n, version, since):
    issue = gh_json("api", f"repos/{REPO}/issues/{n}")
    if not issue or "pull_request" in issue or issue.get("state") != "closed" or (issue.get("closed_at") or "") < since:
        return None
    lines = probes(issue.get("body"))
    if not lines and "box-check" not in {l["name"] for l in issue.get("labels", [])}:
        return None
    mark = MARK.format(version)
    if any(mark in (c or "") for c in gh_json("api", f"repos/{REPO}/issues/{n}/comments", "--jq", "[.[].body]")):
        return None
    cmds = [c for _, cs in lines for c in cs]
    if not cmds:
        body = f"Auf der Box seit {version}, nicht automatisch prüfbar.\n{mark}"
        verdict = "unprüfbar"
    else:
        parts, red = [], False
        for argv in cmds:
            code, out = run([LAIB, *argv[1:]])
            red |= code != 0
            parts.append(f"`{shlex.join(argv)}` → {'grün' if code == 0 else f'rot (exit {code})'}\n```\n{out[-1500:]}\n```")
        verdict = "rot" if red else "grün"
        head = (f"Auf der Box seit {version}: die Prüfung ist **rot**, das Issue ist wieder offen."
                if red else f"Auf der Box seit {version}: die Prüfung ist grün.")
        body = head + "\n\n" + "\n\n".join(parts) + "\n\nWas grün hier nicht zeigt: nur was diese Befehle lesen, nicht was ein Mensch sieht.\n" + mark
    code, out = run([GH, "issue", "comment", str(n), "-R", REPO, "--body", body])
    if code:
        raise RuntimeError(f"comment on #{n}: {out[:200]}")
    if verdict == "rot":
        run([GH, "issue", "reopen", str(n), "-R", REPO])
    return verdict


def gh_login():
    """gh's own login in the kept home first; else the container's PAT (Issues: write needed)"""
    if os.environ.get("GH_TOKEN") or os.path.exists(os.path.expanduser("~/.config/gh/hosts.yml")):
        return
    try:
        m = re.search(r"x-access-token:([^@]+)@", open(os.environ.get("BOXCHECK_GIT_CREDENTIALS", "/run/laib/git-credentials")).read())
    except OSError:
        return
    if m:
        os.environ["GH_TOKEN"] = m[1]


def main():
    gh_login()
    code, out = run([LAIB, "version"])
    try:
        now = json.loads(out)["version"] if code == 0 else ""
    except (ValueError, KeyError, TypeError):
        now = ""
    if not semver(now):
        print(f"werkstatt-boxcheck: the box's version is unknown ({out[:120]}): nothing checked", file=sys.stderr)
        return 1
    try:
        seen = json.load(open(SEEN)).get("version", "")
    except (OSError, ValueError):
        seen = ""
    if seen and semver(seen) and semver(now) <= semver(seen):
        return 0
    if seen and semver(seen):
        try:
            tags, since = releases_between(seen, now)
            for tag in tags:
                body = gh_json("release", "view", tag, "-R", REPO, "--json", "body").get("body", "")
                for n in sorted({int(x) for x in re.findall(r"#(\d+)", body)}):
                    v = check(n, tag, since)
                    if v:
                        print(f"werkstatt-boxcheck: #{n} ({tag}): {v}")
        except RuntimeError as e:  # the next pass tries again; the markers keep what is done
            print(f"werkstatt-boxcheck: {e}", file=sys.stderr)
            return 1
    os.makedirs(os.path.dirname(SEEN), exist_ok=True)
    json.dump({"version": now}, open(SEEN, "w"))
    return 0


if __name__ == "__main__":
    sys.exit(main())
