#!/usr/bin/env bash
# README's architecture section (owner, 2026-10-27): the five layers as a
# mermaid flowchart, then a table per layer with links to the code and the
# lines of each program against its cap. Rewrites only what stands between
# <!-- architektur:begin --> and <!-- architektur:end --> in README.md.
#
# Sources: scripts/loc.sh for lines and caps, skills/*/skill.yml for the
# Operator's skills (and each one's run.py for its lines), templates/*/laib.yml for the
# services (title, beschreibung) and templates/*/values.example.yml for the
# software each one runs (its *IMAGE keys), so a new template shows up by
# itself. What each program and kernel part is for is the hand-written map
# below; a program or kernel package the map does not know shows with a
# placeholder and a warning on stderr. Same tree, same bytes: no dates, C
# sort order.
#
# --check exits 1 when README.md is not what this script writes, and
# changes nothing. A daily workflow (.github/workflows/architektur.yml)
# runs it and opens the PR that brings README.md up to date.
set -euo pipefail
cd "$(dirname "$0")/.."
export LC_ALL=C

check=""
[ "${1:-}" = --check ] && check=1
begin='<!-- architektur:begin -->'
end='<!-- architektur:end -->'
grep -qxF "$begin" README.md && grep -qxF "$end" README.md || { echo "architektur: README.md has no $begin … $end" >&2; exit 2; }

warn() { echo "architektur: warning: $*" >&2; }
num() { # 18290 -> 18,290
  local n=$1 out=""
  while [ ${#n} -gt 3 ]; do out=",${n: -3}$out"; n=${n:0:${#n}-3}; done
  echo "$n$out"
}
lines() { # production Go lines, counted as loc.sh counts them
  [ $# -gt 0 ] || { echo 0; return; }
  find "$@" -name '*.go' ! -name '*_test.go' -exec cat {} + 2>/dev/null | wc -l | tr -d ' '
}
budget() { # budget <prod> <cap>: "2,991 / 3,000", bold and marked at 95 % or more
  local s
  s="$(num "$1") / $(num "$2")"
  if [ "$1" -ge "$2" ]; then echo "**$s, over the cap**"
  elif [ "$(($1 * 100))" -ge "$(($2 * 95))" ]; then echo "**$s, at 95 % or more**"
  else echo "$s"; fi
}
id() { echo "$1" | tr -c 'A-Za-z0-9\n' '_'; }
grid() { # grid <cols> <id…>: invisible links that stack a subgraph's nodes in rows
  local cols=$1 i; shift
  local ids=("$@")
  for ((i = 0; i + cols < ${#ids[@]}; i++)); do echo "    ${ids[i]} ~~~ ${ids[i + cols]}"; done
}

# --- the programs beside the kernel: what scripts/loc.sh budgets ----------
declare -A role=(
  [operator]="The Operator, the admin agent: incidents, the morning report, the review (ADR 0013, 0027)"
  [lease]="laib-lease: the model role's policy proxy and the GPU lease (ADR 0011)"
  [haushalt]="laib-haushalt behind the kernel's verb door: scan, devices, tell, zuhause, person signal, wish (ADR 0026)"
  [persona]="The Bote's persona engine: how it speaks with each person (ADR 0020)"
  [disk]="laib-disk: the data RAID, SMART and temperatures for diag"
  [notify]="laib-notify: the outside watchdog ping and messages to the owner, the chat first, else mail"
  [automat]="Home Assistant automations as the Bote's MCP tool"
  [frage]="„was ist …“: web search with a summary, OpenAI-compatible and MCP"
  [stick]="laib-stick on the laptop: builds the install stick for a new box"
  [mcpserver]="The smallest MCP server, shared by the Bote's tools (frage, automat, persona)"
)
loc=$(scripts/loc.sh 2>&1 || true)
budgets=$(echo "$loc" | sed -nE 's/^([a-z0-9_-]+) production: ([0-9]+)  tests: [0-9]+  \(cap ([0-9]+)\)$/\1 \2 \3/p')
kernel=$(echo "$budgets" | awk '$1 == "kernel" {print $2}')
kernelcap=$(echo "$budgets" | awk '$1 == "kernel" {print $3}')
[ -n "$kernel" ] && [ -n "$kernelcap" ] || { echo "architektur: no kernel line in scripts/loc.sh's output:" >&2; echo "$loc" >&2; exit 2; }
programs=$(echo "$budgets" | awk '$1 != "kernel"' | sort)

# --- the kernel's parts: package dirs and the verbs' files in cmd/laib ----
# <title>|<paths>|<what it does>; cmd/laib's files no part claims are the
# last row, so the parts add up to the kernel.
parts=(
  "State|internal/state internal/secrets internal/boxfs cmd/laib/staterepo.go cmd/laib/secret.go|box.yml, every service's values and the age secrets, a git repo with history"
  "Render, apply, converge|internal/render internal/plan internal/apply internal/sysd cmd/laib/plan.go|template and values to Quadlet, Caddy, Authelia, AdGuard and nftables files; snapshot, write, restart, verify; keep the box on its state"
  "Inbox, proposal, review|internal/inbox internal/proposal internal/review internal/aufgaben|proposals, the judge every proposal passes, approval by content; the owner's to-do list"
  "Policy and hands|internal/policy internal/token internal/envelope internal/skill cmd/laib/token.go|which hand may call which verb up to which tier; the hands' verbs on one service; the Operator's skills"
  "Journal|internal/journal cmd/laib/journal.go|append-only JSONL, everything that happened"
  "Diag and repair|internal/diag|what is red, and the card that fixes it"
  "Backup|internal/backup|the nightly restic backup, restore, the recovery sheet"
  "Self-update|cmd/laib/self.go cmd/laib/updates.go internal/registry|Laib itself, the services' images and templates, with the previous one back on red"
  "Admin pages|internal/pages|\`laib.<domain>\`: Status, Inbox, Keys, Journal"
  "MCP and verb door|internal/mcp internal/cli cmd/laib/door.go|how everyone talks to Laib: MCP, the verb table, the door to the household verbs"
)
claimed=" "
for p in "${parts[@]}"; do IFS='|' read -r _ paths _ <<<"$p"; claimed="$claimed$paths "; done
unmapped=""
for d in internal/*/; do
  d=${d%/}
  case "$claimed" in *" $d "*) ;; *) unmapped="$unmapped $d"; warn "kernel package $d has no part in scripts/architektur.sh";; esac
done
[ -z "$unmapped" ] || parts+=("Not yet placed|${unmapped# }|_(no part yet: add it to scripts/architektur.sh)_")
rest=""
for f in cmd/laib/*.go; do
  case $f in *_test.go) continue;; esac
  case "$claimed" in *" $f "*) ;; *) rest="$rest $f";; esac
done
parts+=("The verbs|cmd/laib|the one binary, CLI and daemon (\`laib serve\`): the verb table and the verbs no part above holds|${rest# }")

# --- the services: one per template ---------------------------------------
field() { sed -nE "s/^$1:[[:space:]]*//p" "$2" | head -1 | sed -E 's/^"(.*)"$/\1/; s/^'"'"'(.*)'"'"'$/\1/; s/[[:space:]]+$//'; }
display() { # display <image ref>: its readable name, the org's for a bare "server"
  local repo=${1%%@*} name
  [[ ${repo##*/} == *:* ]] && repo=${repo%:*}
  name=${repo##*/}
  if [ "$name" = server ]; then repo=${repo%/*}; name=${repo##*/}; fi
  echo "$name"
}
upstream() { # upstream <image ref>: "[name](link)", or the bare name
  local ref=$1 repo name org rest dir wf
  repo=${ref%%@*}
  [[ ${repo##*/} == *:* ]] && repo=${repo%:*}
  name=$(display "$ref")
  case $repo in
    ghcr.io/mdopp/laib-*)
      wf=$(grep -lF "ghcr.io/mdopp/$name:" .github/workflows/*-image.yml 2>/dev/null | head -1 || true)
      dir=""
      [ -z "$wf" ] || dir=$(sed -nE 's/^[[:space:]]+file:[[:space:]]*([^[:space:]]+)\/Containerfile$/\1/p' "$wf" | head -1)
      [ -n "$dir" ] || dir=${name#laib-}
      if [ -d "$dir" ]; then echo "[$name]($dir/)"; else echo "$name"; fi ;;
    ghcr.io/immich-app/*) echo "[$name](https://github.com/immich-app/immich)" ;; # one repo, several images
    ghcr.io/*/*)
      rest=${repo#ghcr.io/}; org=${rest%%/*}; rest=${rest#*/}
      echo "[$name](https://github.com/$org/${rest%%/*})" ;;
    docker.io/library/*) echo "[$name](https://hub.docker.com/_/$name)" ;;
    docker.io/*/*) echo "[$name](https://hub.docker.com/r/${repo#docker.io/})" ;;
    lscr.io/linuxserver/*) echo "[$name](https://hub.docker.com/r/linuxserver/$name)" ;;
    *) echo "$name" ;;
  esac
}
declare -A runs first
services=()
for t in templates/*/; do
  t=${t%/}; n=${t#templates/}
  [ -f "$t/laib.yml" ] || continue
  services+=("$n")
  out="" helper="" seen=" "
  if [ -f "$t/values.example.yml" ]; then
    while read -r key ref; do
      case $key in CHECK_IMAGE|PFLEGE_IMAGE) continue;; esac
      [[ $ref =~ (^|/)python:[^/]*-alpine$ ]] && { helper=1; continue; }
      u=$(upstream "$ref")
      case "$seen" in *" $u "*) continue;; esac
      seen="$seen$u "
      out="${out:+$out, }$u"
      [ -n "${first[$n]:-}" ] || first[$n]=$(display "$ref")
    done < <(sed -nE 's/^([A-Z0-9_]*IMAGE):[[:space:]]*([^[:space:]#]+).*/\1 \2/p' "$t/values.example.yml")
  fi
  if [ -z "$out" ] && [ -n "$helper" ]; then out="own script (Python)"; first[$n]="own script"; fi
  runs[$n]=${out:-—}
done

# --- the Operator's skills: one per skills/<name>/skill.yml ---------------
# The owner's measure for them (2026-10-27): at most 50 skills, at most 200
# lines in a skill's run.py. Shown against both; neither is a gate.
skillcap=50
runcap=200
flat() { # flat <key> <skill.yml>: a one-line value without its comment
  sed -nE "s/^$1:[[:space:]]*//p" "$2" | head -1 | sed -E 's/[[:space:]]+#.*$//; s/[[:space:]]+$//'
}
when() { # when <trigger flow map>: the trigger in plain words
  local t=$1 k v
  t=${t#\{}; t=${t%\}}
  k=${t%%:*}; v=${t#*:}; v=${v# }; v=${v#\"}; v=${v%\"}
  case $k:$v in
    'every:${TICK}') echo "every tick (\`OPERATOR_TICK\`, 10 min by default)" ;;
    every:*m) echo "every ${v%m} min" ;;
    every:*h) echo "every ${v%h} h" ;;
    'daily:${REPORT_AT}') echo "daily at the report hour (\`OPERATOR_REPORT_AT\`, 07:00 by default)" ;;
    daily:*) echo "daily at $v" ;;
    weekly:*)
      local d=${v%% *} day
      case $d in Mo) day=Monday;; Di) day=Tuesday;; Mi) day=Wednesday;; Do) day=Thursday;; Fr) day=Friday;; Sa) day=Saturday;; So) day=Sunday;; *) day=$d;; esac
      echo "weekly, $day ${v#* }" ;;
    event:red) echo "when something turns red" ;;
    *) warn "skill trigger $t has no plain words in scripts/architektur.sh"; echo "\`$t\`" ;;
  esac
}
list() { # list "[a, b]": "\`a\`, \`b\`"
  local l=$1 out="" x
  l=${l#[}; l=${l%]}
  IFS=',' read -ra xs <<<"$l"
  for x in "${xs[@]}"; do x=${x// /}; [ -n "$x" ] && out="${out:+$out, }\`$x\`"; done
  echo "${out:-—}"
}
skills=()
for d in skills/*/; do
  [ -f "${d}skill.yml" ] && skills+=("$(basename "$d")")
done

# --- the section -----------------------------------------------------------
section() {
  local name prod cap p title paths what extra pid ids n tt link d
  echo "$begin"
  echo "<!-- generated by scripts/architektur.sh from scripts/loc.sh, skills/ and templates/; edit the script, not this part -->"
  echo
  echo "Five layers. The family uses its programs and the services; the programs beside the kernel and the family's own programs reach the box only through Laib's verbs; the kernel sets up, checks and backs up every service. Lines are production lines as \`scripts/loc.sh\` counts them, against each program's cap."
  echo
  echo '```mermaid'
  echo "flowchart TB"
  echo "  family[\"Family and devices<br/>phone and browser · Signal · speakers · scanner · lights, switches, sensors\"]"
  echo "  subgraph own[\"Own programs, own repos\"]"
  echo "    own_haus[\"Haus<br/>pages and app of the family\"]"
  echo "    own_dokumente[\"Dokumente<br/>scan, split, read invoices\"]"
  echo "  end"
  echo "  subgraph progs[\"Programs beside the kernel · laib repo, own caps\"]"
  ids=()
  while read -r name prod cap; do
    pid=p_$(id "$name"); ids+=("$pid")
    echo "    $pid[\"$name<br/>$(num "$prod") / $(num "$cap")\"]"
  done <<<"$programs"
  echo "    p_trainer[\"stimme/trainer<br/>wake-word training\"]"; ids+=(p_trainer)
  grid 4 "${ids[@]}"
  echo "  end"
  echo "  subgraph kern[\"Laib kernel · $(num "$kernel") / $(num "$kernelcap") lines\"]"
  ids=()
  for p in "${parts[@]}"; do
    IFS='|' read -r title _ _ <<<"$p"
    pid=k_$(id "$title"); ids+=("$pid")
    echo "    $pid[\"$title\"]"
  done
  grid 4 "${ids[@]}"
  echo "  end"
  echo "  subgraph svcs[\"${#services[@]} services from templates\"]"
  ids=()
  for n in "${services[@]}"; do
    tt=$(field title "templates/$n/laib.yml"); tt=${tt//\"/#quot;}
    pid=s_$(id "$n"); ids+=("$pid")
    echo "    $pid[\"$tt<br/>${first[$n]:-—}\"]"
  done
  grid 5 "${ids[@]}"
  echo "  end"
  echo "  family -->|types, talks| own"
  echo "  family -->|writes, calls| svcs"
  echo "  own -->|asks, proposes over MCP| kern"
  echo "  progs -->|reads, repairs through verbs| kern"
  echo "  kern -->|sets up, checks, backs up| svcs"
  echo '```'
  echo
  echo "**The one rule that holds it together:** a hand calls a verb; what lies above the hand's ceiling becomes a proposal in the Inbox; the human class (new services, keys, programs, removal) is approved only by a human, the low-risk rest by the box itself; then Laib applies, verifies and writes the journal."
  echo
  echo '```'
  echo "hand → verb → proposal → human approval (human class) → apply → journal"
  echo '```'
  echo
  echo "[\`internal/policy\`](internal/policy/) decides who may, [\`internal/inbox\`](internal/inbox/) judges and approves, [\`internal/apply\`](internal/apply/) applies and verifies, [\`internal/journal\`](internal/journal/) remembers."
  echo
  echo "### Family and devices"
  echo
  echo "Phone and browser, Signal, the speakers with their wake word, the scanner on USB, lights, switches and sensors. They reach the box through the family's own programs and the services below, never through the kernel directly."
  echo
  echo "### Own programs, own repos"
  echo
  echo "| Program | Repo | What it is |"
  echo "|---|---|---|"
  echo "| Haus | [mdopp/haus](https://github.com/mdopp/haus) | the family's pages and app, the Box page; runs as the \`haus\` template |"
  echo "| Dokumente | [mdopp/dokumente](https://github.com/mdopp/dokumente) | scanner, splitting stacks, separator sheets, reading invoices and contracts |"
  echo
  echo "### Programs beside the kernel"
  echo
  echo "In this repo, each with its own cap in \`scripts/loc.sh\`. They talk to Laib only through its verbs, never by import."
  echo
  echo "| Program | What it is | Lines / cap |"
  echo "|---|---|---:|"
  while read -r name prod cap; do
    what=${role[$name]:-}
    [ -n "$what" ] || { what="_(no role yet: add it to scripts/architektur.sh)_"; warn "program $name has no role in scripts/architektur.sh"; }
    if [ -d "$name" ]; then link="[\`$name/\`]($name/)"; else link="\`$name\`"; fi
    echo "| $link | $what | $(budget "$prod" "$cap") |"
  done <<<"$programs"
  echo "| [\`stimme/trainer/\`](stimme/trainer/) | trains a wake word for the speakers, the \`stimme\` template's trainer image | $(num "$(find stimme/trainer -name '*.py' ! -path '*/tests/*' -exec cat {} + | wc -l | tr -d ' ')") Python, no cap |"
  echo
  echo "### The Operator's skills"
  echo
  echo "${#skills[@]} / $skillcap skills in [\`skills/\`](skills/), each a folder with its \`skill.yml\` and, where it helps, a \`run.py\` for the steps that must always happen; the Operator runs them, [\`internal/skill\`](internal/skill/) checks them. Description, trigger, tools and \`modell\` are each \`skill.yml\`'s; the script's lines stand against $runcap."
  echo
  echo "\`modell\`: **frei**, the model works in a loop with the granted tools; **urteil**, the script prepares and the model judges once; **nein**, the script alone, no model."
  echo
  echo "| Skill | Description | Runs | Modell | Tools | run.py |"
  echo "|---|---|---|---|---|---:|"
  local s y tools more m py
  for s in "${skills[@]}"; do
    y=skills/$s/skill.yml
    tools=$(list "$(flat tools "$y")")
    more=$(flat more "$y")
    [ -z "$more" ] || tools="$tools; with a capable model also $(list "$more")"
    m=$(flat modell "$y"); m=${m:-frei}
    if [ -f "skills/$s/run.py" ]; then py=$(budget "$(wc -l <"skills/$s/run.py" | tr -d ' ')" "$runcap"); else py="—"; fi
    d=$(field description "$y"); d=${d//|/\\|}
    echo "| [\`$s\`](skills/$s/) | $d | $(when "$(flat trigger "$y")") | $m | $tools | $py |"
  done
  echo
  echo "### The Laib kernel"
  echo
  echo "The part the house trusts: it must work when everything else is broken. $(budget "$kernel" "$kernelcap") lines (\`cmd/\`, \`internal/\`)."
  echo
  echo "| Part | What it does | Code | Lines |"
  echo "|---|---|---|---:|"
  local sum=0 links c
  for p in "${parts[@]}"; do
    IFS='|' read -r title paths what extra <<<"$p"
    links=""
    if [ "$title" = "The verbs" ]; then
      links="[\`cmd/laib\`](cmd/laib/)"
      # shellcheck disable=SC2086
      c=$(lines $extra)
    else
      for x in $paths; do
        [ -e "$x" ] || { warn "kernel part $title: $x does not exist"; continue; }
        if [ -d "$x" ]; then links="${links:+$links, }[\`${x#internal/}\`]($x/)"; else links="${links:+$links, }[\`${x#cmd/laib/}\`]($x)"; fi
      done
      # shellcheck disable=SC2086
      c=$(lines $(for x in $paths; do [ -e "$x" ] && echo "$x"; done))
    fi
    sum=$((sum + c))
    echo "| $title | $what | $links | $(num "$c") |"
  done
  [ "$sum" = "$kernel" ] || warn "the kernel's parts add up to $sum lines, scripts/loc.sh counts $kernel"
  echo
  echo "### Services from templates"
  echo
  echo "${#services[@]} templates in [\`templates/\`](templates/); Laib sets each up, checks it and backs it up. Name and description are the household's, from each \`laib.yml\`; the software is each \`values.example.yml\`'s images, without the small Python helpers."
  echo
  echo "| Service | Beschreibung | Runs |"
  echo "|---|---|---|"
  for n in "${services[@]}"; do
    echo "| [$(field title "templates/$n/laib.yml")](templates/$n/) \`$n\` | $(field beschreibung "templates/$n/laib.yml") | ${runs[$n]} |"
  done
  echo "$end"
}

new=$(mktemp)
trap 'rm -f "$new" "$new.section"' EXIT
section >"$new.section"
awk -v b="$begin" -v e="$end" -v f="$new.section" '
  $0 == b { while ((getline l < f) > 0) print l; skip = 1; next }
  $0 == e { skip = 0; next }
  !skip' README.md >"$new"
rm -f "$new.section"
if [ -n "$check" ]; then
  cmp -s "$new" README.md && { echo "architektur: README.md is current"; exit 0; }
  echo "architektur: README.md's architecture section is stale; run scripts/architektur.sh" >&2
  diff -u README.md "$new" >&2 || true
  exit 1
fi
cmp -s "$new" README.md && { echo "architektur: README.md already current"; exit 0; }
cat "$new" >README.md
echo "architektur: README.md updated"
