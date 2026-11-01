#!/usr/bin/env sh
# One budget per program (ADR 0023, ADR 0005 amendment #309): the kernel
# (cmd/, internal/), the Operator (operator/) and each program that moved out
# of the kernel. Each: production under its cap; test lines are counted
# but not capped (owner, 2026-10-18). They meet over MCP, files or HTTP,
# never by import.
#
# Crossing a cap means raising it in the same PR, with its reason next to
# the number and the owner's approval; at 95 % this warns, and on main CI
# opens or updates the "Kern-Diät" issue (scripts/kern-diaet.sh). Lines are
# counted as gofmt writes them; CI fails on a file gofmt would change (#866).
#
# --diet prints that issue's body and exits 0 when the kernel is at 95 % or
# more, 3 when it is not.
set -e
cd "$(dirname "$0")/.."
bad=0
budget() { # budget <name> <cap> <dir…>
  name=$1 cap=$2; shift 2
  prod=$(find "$@" -name '*.go' ! -name '*_test.go' -exec cat {} + 2>/dev/null | wc -l)
  test=$(find "$@" -name '*_test.go' -exec cat {} + 2>/dev/null | wc -l)
  [ "$name" = kernel ] && kernel=$prod kernelcap=$cap
  [ -n "$diet" ] && return
  echo "$name production: $prod  tests: $test  (cap $cap)"
  [ "$prod" -lt "$cap" ] || { echo "$name over $cap lines (ADR 0005, 0023)"; bad=1; }
  [ "$((prod * 100))" -lt "$((cap * 95))" ] || [ "$prod" -ge "$cap" ] || echo "warning: $name at $prod of $cap lines, 95 % or more (#309)"
}
diet=""
[ "${1:-}" = --diet ] && diet=1
lines() { find "$@" -name '*.go' ! -name '*_test.go' -exec cat {} + 2>/dev/null | wc -l; }
# All caps +10 % (kernel 22,000, Operator 3,300, each program's, the skills' 55 / 220 / 275): set by
# the owner, mdopp, 2026-10-30, on train 2026-10-30b ("Erhöhe alle Grenzen um 10%"): the review
# issues of #851 landed in one train with the kernel at 19,433, the Operator at 2,941, frage at 399.
# 20,000: set by the owner, mdopp, 2026-10-23, on #609 (approve is a merge,
# a red run rolls back its whole batch) asking for 17,000: "Setze die Grenze
# auf 20k". The kernel stood at 16,944.
# 16,900: raised by the owner, mdopp, 2026-10-23, #574-#579: the tools an
# agent was missing on the box ("Setze das alles um"): built-in hands get
# their new verbs after an update, the state repo readable, the LAN's
# devices, an update on demand, a short context and inbox, an honest
# Inbox error; the kernel at 16,232 before, about 500 lines asked.
# 16,400: raised by the owner, mdopp, 2026-10-22, #561: box.yml zuhause,
# the household's address, its proposal verb (laib zuhause) and the
# ZUHAUSE_* builtins, about 230 lines, the kernel at 15,867 before; state,
# review and render work with no honest cut. The owner set 16,400 rather
# than the 16,200 asked, room for the next household setting of that kind.
# 16,000: raised by the owner, mdopp, 2026-10-19, #443: the growth since
# v0.7.0 is trust-kernel work — the relay's verdicts, repairs, the
# credentials handover; no honest cut left, see #443. (Before: 15,000,
# owner, 2026-10-17, #309.) 
budget kernel 22000 cmd internal
budget operator 3300 operator
# the skills' scripts (#839): the deterministic steps that left the Operator's Go, Python run by
# its runner, their own line so they are counted and not hidden in the Operator's; laibskill.py,
# their one door, with them. The owner, 2026-10-27, #839: no more than 50 skills, each of a
# sensible size — so the caps are per skill (a run.py of at most 200 lines; the largest at the
# move was 149) and on the count, not one sum that a few big skills could fill.
py=$(find skills -name '*.py' ! -name 'test_*.py' -exec cat {} + 2>/dev/null | wc -l)
pyt=$(find skills -name 'test_*.py' -exec cat {} + 2>/dev/null | wc -l)
n=$(find skills -mindepth 2 -maxdepth 2 -name skill.yml | wc -l)
[ -n "$diet" ] || echo "skill scripts production: $py  tests: $pyt  skills: $n (cap 55, each script file 220)"
[ "$n" -le 55 ] || { echo "more than 55 skills (#839)"; bad=1; }
# The rule for a skill's script (owner, 2026-11-01, #978: „was ist sinnvoll,
# verständlich und wartbar?“): one file, one theme, at most 220 lines — its
# run.py. A fact about the box (was a stop planned, what a service depends
# on, is a finding gone) is Laib's, read through a verb (laib planned), never
# decided again in a script; what two skills share lives in skills/lib/
# (logzeilen.py: how a journal line is read). A second file in a skill's
# folder is a warning sign that its theme is two: warned here, not failed.
# Every file, run.py and skills/lib/'s, is held to the 220.
for f in skills/*/*.py; do
  case "$f" in */test_*) continue ;; esac
  l=$(wc -l < "$f")
  [ "$l" -le 220 ] || { echo "$f has $l lines, a skill script is at most 220 (#839)"; bad=1; }
  case "$f" in skills/lib/*|*/run.py) ;; *) [ -n "$diet" ] || echo "warning: $f beside run.py: one file, one theme; shared parts in Laib or skills/lib/ (#978)" ;; esac
done
door=$(wc -l < skills/laibskill.py)
[ "$door" -le 275 ] || { echo "skills/laibskill.py has $door lines, the scripts' door is at most 275 (#839)"; bad=1; }
# the programs that moved out of the kernel (#309): their size then, plus a quarter
budget stick 1980 stick         # the laptop's install-stick builder, 1,468
budget frage 440 frage          # "was ist …", 261
budget automat 660 automat      # the Bote's HA automations, 421
budget persona 990 persona      # the Bote's persona engine, 701
budget mcpserver 220 mcpserver  # the Bote's tools' MCP server, 111
budget disk 770 disk            # laib-disk: the data RAID, SMART, thermal (#443), 564
# notify 600: the owner, mdopp, 2026-10-25 (#659): the chat retried before mail, mail sent right,
# failures noted once an hour; 501 lines, no honest cut
budget notify 660 notify        # laib-notify: the watchdog ping and the owner's mail (#443), ~270
# behind the kernel's verb door (ADR 0026, #598): the household verbs, each
# program with its own cap; the kernel's shrank by what moved, 16,889 to 16,695
# haushalt 700: set by the owner, mdopp, 2026-10-25, on #676 ("1 a"): laib tell (one message to
# one person through the Bote, haus#123), the first household verb ADR 0026 puts here instead of
# the kernel; 610 lines, room for the next verb of that size
# haushalt 920: set by the owner, mdopp, 2026-10-26, on #677 ("a+b"): laib zuhause and laib person
# signal move behind the door out of the kernel (ADR 0026 §4); 736 lines, plus about a quarter
budget haushalt 1012 haushalt    # laib-haushalt: scan and net geraete, 480; tell (#676); zuhause, person signal (#677); wish (#797)
# lease 1,300: set by the owner, mdopp, 2026-10-26, on #677 ("a+b"): the lease leaves the kernel
# as its own program (the llama template's policy proxy and GPU lease, ADR 0011, 0027), reversing
# 2026-10-17's "lease stays" (ADR 0005); 1,076 lines in the kernel, 1,154 with its own MCP client
# and atomic write
budget lease 1430 lease         # laib-lease: the policy proxy and the GPU lease (#677)

# the boundary: the Operator's code is not in the kernel's tree, and
# neither program imports the other
inside=$(grep -rlE '^package operator$' --include='*.go' cmd internal || true)
[ ! -d cmd/laib-operator ] || inside="$inside cmd/laib-operator"
[ -z "$inside" ] || { echo "Operator code inside the kernel's count: $inside"; bad=1; }
up=$(grep -rlE '"github.com/mdopp/laib/(internal|cmd)/' --include='*.go' operator 2>/dev/null || true)
[ -z "$up" ] || { echo "the Operator imports the kernel: $up"; bad=1; }
down=$(grep -rlE '"github.com/mdopp/laib/operator' --include='*.go' cmd internal || true)
[ -z "$down" ] || { echo "the kernel imports the Operator: $down"; bad=1; }
# a program that moved out of the kernel imports none of it, and the kernel
# imports none of them
for prog in stick frage automat persona mcpserver disk notify haushalt lease; do
  up=$(grep -rlE '"github.com/mdopp/laib/(internal|cmd)/' --include='*.go' "$prog" 2>/dev/null || true)
  [ -z "$up" ] || { echo "$prog imports the kernel: $up"; bad=1; }
  down=$(grep -rlE "\"github.com/mdopp/laib/$prog[/\"]" --include='*.go' cmd internal || true)
  [ -z "$down" ] || { echo "the kernel imports $prog: $down"; bad=1; }
done
if [ -n "$diet" ]; then
  [ "$((kernel * 100))" -ge "$((kernelcap * 95))" ] || exit 3
fi

# inside the kernel: the box chores (ADR 0005 amendment #309) report and
# maintain; the trust kernel never depends on them. Only cmd/laib and other
# chores import a chore.
chores="diag registry pages"
for c in $chores; do
  users=$(grep -rlE "\"github.com/mdopp/laib/internal/$c\"" --include='*.go' internal | grep -vE "^internal/($(echo $chores | tr ' ' '|'))/" || true)
  [ -z "$users" ] || { echo "the trust kernel imports the chore $c: $users"; bad=1; }
done

if [ -n "$diet" ]; then
  echo "The kernel is at **$kernel of $kernelcap** production lines (\`scripts/loc.sh\`), 95 % or more."
  echo
  echo "What sits in the kernel but is not the trust kernel (ADR 0019 §2, ADR 0023 §2): candidates to move out, as the Operator (#268) and the programs (#309) did. Measured by the owner's maxim (#246): easier to maintain, nothing worse for the family. Squeezing lines is not a fix."
  echo
  echo "| Package | Lines | What it is |"
  echo "|---|---:|---|"
  for c in $chores; do echo "| \`internal/$c\` | $(lines internal/$c) | box chore |"; done
  echo "| \`cmd/laib\` | $(lines cmd/laib) | the small verbs; \`internal/hands\` (secret, token, operator) is the next cut |"
  echo
  echo "Crossing the cap means raising it in the same PR, with its reason in \`scripts/loc.sh\` and the owner's approval (#309)."
  echo
  echo "This issue is opened and updated by CI on main (\`scripts/kern-diaet.sh\`)."
  exit 0
fi
exit $bad
