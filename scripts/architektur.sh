#!/usr/bin/env bash
# README's architecture section (owner, 2026-10-27): the five layers as a
# blueprint (docs/architektur.svg, a dark twin for GitHub's dark theme; the
# owner's „Bauplan“, #899), then a table per layer with links to the code
# and the lines of each program against its cap. Rewrites only what stands
# between <!-- architektur:begin --> and <!-- architektur:end --> in
# README.md, and the two SVG files.
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
# --check exits 1 when README.md or an SVG is not what this script writes,
# and changes nothing. A daily workflow (.github/workflows/architektur.yml)
# runs it and opens the PR that brings them up to date.
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
esc() { local s=$1; s=${s//&/\&amp;}; s=${s//</\&lt;}; s=${s//>/\&gt;}; s=${s//\"/\&quot;}; echo "$s"; }

# --- the family's own programs: own repos, run as a template ---------------
# <name>|<repo>|<what it is, for the table>|<two short words for the picture>
own=(
  "Haus|mdopp/haus|the family's pages and app, the Box page; runs as the \`haus\` template|pages and app of the family"
  "Dokumente|mdopp/dokumente|scanner, splitting stacks, separator sheets, reading invoices and contracts|scan, split, read invoices"
  "Gesundheit|mdopp/gesundheit|medical records from Paperless as a timeline per person, the daily note (#893); its template comes from the \`apps\` registry|medical records as a timeline"
)

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
declare -A partlines
kernelsum=0
for p in "${parts[@]}"; do
  IFS='|' read -r title paths _ extra <<<"$p"
  if [ "$title" = "The verbs" ]; then
    # shellcheck disable=SC2086
    c=$(lines $extra)
  else
    # shellcheck disable=SC2086
    c=$(lines $(for x in $paths; do [ -e "$x" ] && echo "$x"; done))
  fi
  partlines[$title]=$c
  kernelsum=$((kernelsum + c))
done
[ "$kernelsum" = "$kernel" ] || warn "the kernel's parts add up to $kernelsum lines, scripts/loc.sh counts $kernel"

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
# lines in a skill's run.py, both +10 % on 2026-10-30 (scripts/loc.sh, which
# holds them). Shown against both; neither is a gate here.
skillcap=55
runcap=220
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

# --- the blueprint: docs/architektur.svg, light and dark (#899) ------------
# The five layers as one picture, drawn from the lists the tables use: the
# own programs, loc.sh's budgets, the kernel's parts with their lines, the
# templates' titles. An <img> gets no page CSS, so colours are fixed in the
# file and there is one file per theme; README's <picture> picks. Widths are
# estimated from character counts (about 0.55 em per character), nothing is
# measured, so every label is kept short.
svgfiles=(docs/architektur.svg docs/architektur-dark.svg)
svg() { # svg light|dark
  local theme=$1
  local bg panel fg muted line kern kernsoft prog progsoft eigen eigensoft dienst dienstsoft aussen aussensoft warn
  if [ "$theme" = dark ]; then
    bg="#0d1117" panel="#161b22" fg="#e6edf3" muted="#9aa8a0" line="#30363d"
    kern="#7fc4a2" kernsoft="#1f3329" prog="#93afe0" progsoft="#1f2838" eigen="#e0b07c" eigensoft="#33281c"
    dienst="#b4b4c4" dienstsoft="#26262e" aussen="#e09abd" aussensoft="#33212b" warn="#f0a067"
  else
    bg="#ffffff" panel="#ffffff" fg="#1d2420" muted="#5d6a63" line="#c9d1cb"
    kern="#2f6b52" kernsoft="#e3efe8" prog="#3d5a8a" progsoft="#e4eaf4" eigen="#8a5a2b" eigensoft="#f3e9dd"
    dienst="#5f5f6e" dienstsoft="#ececf1" aussen="#7a3b5c" aussensoft="#f2e4eb" warn="#a5561b"
  fi
  local font="font-family=\"-apple-system, 'Segoe UI', Helvetica, Arial, sans-serif\""
  rect() { echo "  <rect x=\"$1\" y=\"$2\" width=\"$3\" height=\"$4\" rx=\"$5\" fill=\"$6\" stroke=\"$7\"${8:+ stroke-width=\"$8\"}/>"; }
  text() { # text <x> <y> <size> <fill> <weight|-> <anchor|-> <text>
    echo "  <text x=\"$1\" y=\"$2\" font-size=\"$3\" fill=\"$4\"${5:+ font-weight=\"$5\"}${6:+ text-anchor=\"$6\"}>$(esc "$7")</text>"
  }
  arrow() { echo "  <path d=\"M$1 $2 L$1 $3\" stroke=\"$muted\" stroke-width=\"1.6\" fill=\"none\" marker-end=\"url(#ar)\"/>"; }
  local -i W=1000 L=20 R=980 y i r c cols bw x0 rows
  local name prod cap sub title a b o n

  # the rows: 1 family, 2 own programs and programs beside the kernel, 3 kernel, 4 services
  local -a pn ps on os
  while read -r name prod cap; do pn+=("$name"); ps+=("$(num "$prod") / $(num "$cap")"); done <<<"$programs"
  pn+=("stimme/trainer"); ps+=("$(num "$(find stimme/trainer -name '*.py' ! -path '*/tests/*' -exec cat {} + | wc -l | tr -d ' ')") Python")
  for o in "${own[@]}"; do IFS='|' read -r name _ _ sub <<<"$o"; on+=("$name"); os+=("$sub"); done
  local -i ownw progx progw h2 kcols=4 krows h3 h4 y1 y2 y3 y4 H xa xb oy
  ownw=$((16 + ${#on[@]} * 132 + 4)); progx=$((L + ownw + 20)); progw=$((R - L - ownw - 20))
  cols=4; rows=$(( (${#pn[@]} + cols - 1) / cols ))
  h2=$((38 + rows * 54 + 6)); [ "$h2" -ge 150 ] || h2=150
  krows=$(( (${#parts[@]} + kcols - 1) / kcols )); h3=$((40 + krows * 64 + 2))
  local -a sl; local cur=""
  for n in "${services[@]}"; do
    title=$(field title "templates/$n/laib.yml")
    if [ -n "$cur" ] && [ $(( ${#cur} + ${#title} + 3 )) -gt 112 ]; then sl+=("$cur"); cur=""; fi
    cur="${cur:+$cur · }$title"
  done
  [ -z "$cur" ] || sl+=("$cur")
  h4=$((40 + ${#sl[@]} * 24 + 2))
  y1=16; y2=$((y1 + 70 + 24)); y3=$((y2 + h2 + 32)); y4=$((y3 + h3 + 28)); H=$((y4 + h4 + 16))

  echo "<svg xmlns=\"http://www.w3.org/2000/svg\" viewBox=\"0 0 $W $H\" width=\"$W\" height=\"$H\" $font role=\"img\" aria-label=\"The five layers of a Laib box: family and devices, the own programs, the programs beside the kernel, the Laib kernel, the services from templates\">"
  echo "  <!-- generated by scripts/architektur.sh (#899); edit the script, not this file -->"
  echo "  <defs><marker id=\"ar\" viewBox=\"0 0 10 10\" refX=\"9\" refY=\"5\" markerWidth=\"7\" markerHeight=\"7\" orient=\"auto-start-reverse\"><path d=\"M0,0 L10,5 L0,10 z\" fill=\"$muted\"/></marker></defs>"
  rect 0 0 "$W" "$H" 0 "$bg" none
  # 1 family and devices
  rect "$L" "$y1" $((R - L)) 70 10 "$aussensoft" "$aussen"
  text $((L + 16)) $((y1 + 24)) 13 "$aussen" 600 "" "FAMILY AND DEVICES"
  text $((L + 16)) $((y1 + 50)) 14 "$fg" "" "" "phone and browser  ·  Signal  ·  speakers with their wake word  ·  scanner on USB  ·  lights, switches, sensors"
  # 2 own programs
  rect "$L" "$y2" "$ownw" "$h2" 10 "$eigensoft" "$eigen"
  text $((L + 16)) $((y2 + 24)) 13 "$eigen" 600 "" "OWN PROGRAMS, OWN REPOS"
  oy=$((y2 + 38 + (h2 - 38 - 16 - 96) / 2)) # the boxes centred in the band's height
  for ((i = 0; i < ${#on[@]}; i++)); do
    x0=$((L + 16 + i * 132))
    rect "$x0" "$oy" 120 96 8 "$panel" "$eigen"
    text $((x0 + 60)) $((oy + 34)) 15 "$fg" 600 middle "${on[i]}"
    # the short line in two, split at the space nearest its middle
    sub=${os[i]}; a=${sub:0:$(( ${#sub} / 2 + 1 ))}; a=${a% *}; b=${sub:${#a}}; b=${b# }
    text $((x0 + 60)) $((oy + 56)) 11 "$muted" "" middle "$a"
    text $((x0 + 60)) $((oy + 72)) 11 "$muted" "" middle "$b"
  done
  # 2 programs beside the kernel
  rect "$progx" "$y2" "$progw" "$h2" 10 "$progsoft" "$prog"
  text $((progx + 16)) $((y2 + 24)) 13 "$prog" 600 "" "PROGRAMS BESIDE THE KERNEL  ·  LAIB REPO, OWN CAPS"
  bw=$(( (progw - 32 - (cols - 1) * 10) / cols ))
  for ((i = 0; i < ${#pn[@]}; i++)); do
    r=$((i / cols)); c=$((i % cols)); x0=$((progx + 16 + c * (bw + 10))); y=$((y2 + 38 + r * 54))
    rect "$x0" "$y" "$bw" 44 7 "$panel" "$prog"
    text $((x0 + bw / 2)) $((y + 19)) 13 "$fg" 600 middle "${pn[i]}"
    read -r prod _ cap <<<"${ps[i]//,/}"
    if [ -n "$cap" ] && [ $((prod * 100)) -ge $((cap * 95)) ]; then # at 95 % or more: the number in the warning colour, as the table marks it
      text $((x0 + bw / 2)) $((y + 35)) 11 "$warn" 600 middle "${ps[i]}"
    else
      text $((x0 + bw / 2)) $((y + 35)) 11 "$muted" "" middle "${ps[i]}"
    fi
  done
  # 3 the kernel
  rect "$L" "$y3" $((R - L)) "$h3" 12 "$kernsoft" "$kern" 2
  text $((L + 16)) $((y3 + 26)) 13 "$kern" 600 "" "LAIB KERNEL  ·  the part the house trusts  ·  $(num "$kernel") of $(num "$kernelcap") lines"
  bw=$(( (R - L - 32 - (kcols - 1) * 12) / kcols ))
  for ((i = 0; i < ${#parts[@]}; i++)); do
    IFS='|' read -r title _ _ _ <<<"${parts[i]}"
    r=$((i / kcols)); c=$((i % kcols)); x0=$((L + 16 + c * (bw + 12))); y=$((y3 + 40 + r * 64))
    rect "$x0" "$y" "$bw" 54 7 "$panel" "$kern"
    text $((x0 + bw / 2)) $((y + 22)) 13 "$fg" 600 middle "$title"
    text $((x0 + bw / 2)) $((y + 40)) 11 "$muted" "" middle "$(num "${partlines[$title]}") lines"
  done
  # 4 the services
  rect "$L" "$y4" $((R - L)) "$h4" 10 "$dienstsoft" "$dienst"
  text $((L + 16)) $((y4 + 24)) 13 "$dienst" 600 "" "${#services[@]} SERVICES FROM TEMPLATES  ·  Laib sets them up, checks them, backs them up"
  for ((i = 0; i < ${#sl[@]}; i++)); do text $((L + 16)) $((y4 + 52 + i * 24)) 13 "$fg" "" "" "${sl[i]}"; done
  # the arrows, labelled beside them
  xa=$((L + ownw / 2)); xb=$((progx + progw / 2))
  arrow "$xa" $((y1 + 70)) $((y2 - 2));            text $((xa + 8)) $((y2 - 8)) 11 "$muted" "" "" "types, talks"
  arrow "$xb" $((y1 + 70)) $((y2 - 2));            text $((xb + 8)) $((y2 - 8)) 11 "$muted" "" "" "writes, calls"
  arrow "$xa" $((y2 + h2)) $((y3 - 2));            text $((xa + 8)) $((y3 - 10)) 11 "$muted" "" "" "asks, proposes over MCP"
  arrow "$xb" $((y2 + h2)) $((y3 - 2));            text $((xb + 8)) $((y3 - 10)) 11 "$muted" "" "" "reads, repairs through verbs"
  arrow $((L + 290)) $((y3 + h3)) $((y4 - 2));     text $((L + 298)) $((y4 - 8)) 11 "$muted" "" "" "sets up"
  arrow $((L + 662)) $((y3 + h3)) $((y4 - 2));     text $((L + 670)) $((y4 - 8)) 11 "$muted" "" "" "checks, backs up"
  echo "</svg>"
}

# --- the section -----------------------------------------------------------
section() {
  local name prod cap p title paths what n link d
  echo "$begin"
  echo "<!-- generated by scripts/architektur.sh from scripts/loc.sh, skills/ and templates/; edit the script, not this part -->"
  echo
  echo "Five layers. The family uses its programs and the services; the programs beside the kernel and the family's own programs reach the box only through Laib's verbs; the kernel sets up, checks and backs up every service. Lines are production lines as \`scripts/loc.sh\` counts them, against each program's cap. The picture is [\`docs/architektur.svg\`](docs/architektur.svg), drawn by the same script."
  echo
  echo "<picture>"
  echo "  <source media=\"(prefers-color-scheme: dark)\" srcset=\"docs/architektur-dark.svg\">"
  echo "  <img alt=\"The five layers of a Laib box: family and devices, the own programs, the programs beside the kernel, the Laib kernel, the services from templates\" src=\"docs/architektur.svg\" width=\"1000\">"
  echo "</picture>"
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
  local o repo
  for o in "${own[@]}"; do
    IFS='|' read -r name repo what _ <<<"$o"
    echo "| $name | [$repo](https://github.com/$repo) | $what |"
  done
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
  local links
  for p in "${parts[@]}"; do
    IFS='|' read -r title paths what _ <<<"$p"
    links=""
    if [ "$title" = "The verbs" ]; then
      links="[\`cmd/laib\`](cmd/laib/)"
    else
      for x in $paths; do
        [ -e "$x" ] || { warn "kernel part $title: $x does not exist"; continue; }
        if [ -d "$x" ]; then links="${links:+$links, }[\`${x#internal/}\`]($x/)"; else links="${links:+$links, }[\`${x#cmd/laib/}\`]($x)"; fi
      done
    fi
    echo "| $title | $what | $links | $(num "${partlines[$title]}") |"
  done
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

new=$(mktemp -d)
trap 'rm -rf "$new"' EXIT
section >"$new/section"
awk -v b="$begin" -v e="$end" -v f="$new/section" '
  $0 == b { while ((getline l < f) > 0) print l; skip = 1; next }
  $0 == e { skip = 0; next }
  !skip' README.md >"$new/README.md"
svg light >"$new/architektur.svg"
svg dark >"$new/architektur-dark.svg"
stale=""
for f in README.md "${svgfiles[@]}"; do cmp -s "$new/$(basename "$f")" "$f" 2>/dev/null || stale="$stale $f"; done
if [ -n "$check" ]; then
  [ -n "$stale" ] || { echo "architektur: README.md and ${svgfiles[*]} are current"; exit 0; }
  echo "architektur: stale:$stale; run scripts/architektur.sh" >&2
  diff -u README.md "$new/README.md" >&2 || true
  exit 1
fi
[ -n "$stale" ] || { echo "architektur: README.md and ${svgfiles[*]} already current"; exit 0; }
mkdir -p docs
for f in $stale; do cp "$new/$(basename "$f")" "$f"; done
echo "architektur: updated$stale"
