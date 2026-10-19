#!/usr/bin/env bash
# werkstatt-skills (#394): the guide's skills, where pi and Claude Code look.
# The skills are /opt/werkstatt/guide/skills in the image, one source,
# rebuilt on every push to main and auto-updated on the box: no timer, no
# copy that ages. At every start this links them, so a newer image's set is
# what the agents see:
#   pi in a terminal  ~/.pi/agent/skills/laib -> the folder
#   pi in the browser ~/.pi/web/skills/laib   -> the folder
#   Claude Code       ~/.claude/skills/<name> -> each skill (it does not nest)
# A link into the folder whose skill is gone is removed; a person's own
# skills are never touched. Safe to repeat.
set -euo pipefail
SRC="${WERKSTATT_SKILLS:-/opt/werkstatt/guide/skills}"
for agent in "$HOME/.pi/agent" "$HOME/.pi/web"; do
  mkdir -p "$agent/skills"
  ln -sfn "$SRC" "$agent/skills/laib"
done
C="$HOME/.claude/skills"
mkdir -p "$C"
for l in "$C"/*; do
  [ -L "$l" ] || continue
  case "$(readlink "$l")" in "$SRC"/*) [ -e "$l/SKILL.md" ] || rm -f "$l";; esac
done
n=0
for d in "$SRC"/*/; do
  [ -f "$d/SKILL.md" ] || continue
  name=$(basename "$d")
  if [ -e "$C/$name" ] && [ ! -L "$C/$name" ]; then continue; fi   # the person's own skill of that name wins
  ln -sfn "$SRC/$name" "$C/$name"
  n=$((n + 1))
done
echo "werkstatt-skills: $n skills linked for pi and Claude Code"
