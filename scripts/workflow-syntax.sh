#!/usr/bin/env bash
# Every `run:` block of every workflow parses as bash (GitHub's ${{ }}
# expressions replaced first). A quote that breaks a block otherwise shows
# up only when a 30-minute vm-install run reaches that step (main a0900aa:
# an apostrophe in a comment inside $SSH '…').
#
# No check pipes into a grep that stops at the first match (-q, or its
# output thrown away): grep exits, the writer still writing dies of SIGPIPE,
# and pipefail — every $SSH block sets it — turns a match red, or a match
# that must not be there green (#307, #304: `laib secret list | grep -qx`
# and `nft list table | grep -q` went red with the line right there). The
# writer runs to its end first: grep -q X <<<"$(writer)". The same holds
# for scripts/*.sh (#329).
set -euo pipefail
cd "$(dirname "$0")/.."
python3 - <<'PY'
import glob, re, subprocess, sys, yaml
bad = 0
early = re.compile(r"(?<!\|)\|(?!\|)\s*(?:sudo\s+)?grep\b[^|;&\n]*?(?:\s-[A-Za-z]*q|\s--quiet|\s--silent|>\s*/dev/null)")
for f in sorted(glob.glob(".github/workflows/*.yml")):
    wf = yaml.safe_load(open(f))
    for jname, job in (wf.get("jobs") or {}).items():
        for st in job.get("steps") or []:
            if "run" not in st or st.get("shell", "bash") not in ("bash", None):
                continue
            body = re.sub(r"\$\{\{[^}]*\}\}", "X", st["run"])
            r = subprocess.run(["bash", "-n"], input=body, text=True, capture_output=True)
            if r.returncode:
                bad += 1
                print(f"{f}: {jname}: {st.get('name', st['run'][:40])}: {r.stderr.strip()}")
            for line in body.splitlines():
                if early.search(line):
                    bad += 1
                    print(f"{f}: {jname}: a pipe into an early grep (SIGPIPE under pipefail, #307): {line.strip()[:160]}")
# the scripts CI and the boxes run carry the same pipefail (#329)
for f in sorted(glob.glob("scripts/**/*.sh", recursive=True)):
    for n, line in enumerate(open(f).read().replace("\\\n", " ").splitlines(), 1):
        if not line.lstrip().startswith("#") and early.search(line):
            bad += 1
            print(f"{f}:{n}: a pipe into an early grep (SIGPIPE under pipefail, #307): {line.strip()[:160]}")
print(f"workflow run blocks and scripts: {bad} with a syntax error or an early grep")
sys.exit(1 if bad else 0)
PY
