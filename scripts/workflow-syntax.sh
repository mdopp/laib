#!/usr/bin/env bash
# Every `run:` block of every workflow parses as bash (GitHub's ${{ }}
# expressions replaced first). A quote that breaks a block otherwise shows
# up only when a 30-minute vm-install run reaches that step (main a0900aa:
# an apostrophe in a comment inside $SSH '…').
set -euo pipefail
cd "$(dirname "$0")/.."
python3 - <<'PY'
import glob, re, subprocess, sys, yaml
bad = 0
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
            # `$L <verb> | grep -q` under pipefail: grep exits at the first match, laib's
            # next write is SIGPIPE (141) and the probe fails though the line was there
            # (vm-install: RESTIC_PASSWORD, then IMMICH_API_KEY); grep … >/dev/null reads to the end
            for n, line in enumerate(st["run"].splitlines(), 1):
                if re.search(r"\$L [^|#]*\|\s*grep -[a-zA-Z]*q", line):
                    bad += 1
                    print(f"{f}: {jname}: {st.get('name', '')}: line {n}: `$L … | grep -q` is SIGPIPE under pipefail; use `grep … >/dev/null`")
print(f"workflow run blocks: {bad} with a syntax error or a laib | grep -q")
sys.exit(1 if bad else 0)
PY
