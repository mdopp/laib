#!/usr/bin/env bash
# werkstatt-sessions: one Claude Code Remote Control session per checkout
# (#383), as ServiceBay's claude-dev started them (start-claude,
# reconcile_repos). Every checkout in /workspace with a CLAUDE.md gets a
# window in the tmux session `claude`, named after the checkout, running
# `claude --continue --remote-control <name>`: the session shows up under
# that name in the Claude app and on claude.ai, and picks its last
# conversation up again. The entrypoint runs a pass on start and then every
# five minutes, so a new checkout gets its session without a restart.
#
# Before a checkout's first start its onboarding and folder trust are
# answered in ~/.claude.json (#498, settle below): nobody is at the window.
#
# The sessions run in Claude Code's default permission mode: a tool call
# that needs a permission is asked for in the app. claude-dev also passed
# --allow-dangerously-skip-permissions; a session reachable from the phone
# that holds an apply-capable Laib hand does not get that (#383).
#
#   werkstatt-sessions             one pass (idempotent)
#   werkstatt-sessions off <name>  switch a checkout's session off: a marker
#                                  in ~/.werkstatt/no-autostart (the kept
#                                  home), so a restart keeps it off
#   werkstatt-sessions on <name>   switch it on again, and start it
#
# A window whose session ended with an error stays visible
# (remain-on-exit failed, tmux.conf) and is started again on the next pass;
# a session that was ended cleanly is started again too. Off is the marker.
set -euo pipefail
WS="${WERKSTATT_WORKSPACE:-/workspace}"
OFF="${WERKSTATT_SESSIONS_OFF:-$HOME/.werkstatt/no-autostart}"
S="${WERKSTATT_TMUX_SESSION:-claude}"
CLAUDE="${WERKSTATT_CLAUDE:-claude}"
CONF="${WERKSTATT_TMUX_CONF:-/etc/tmux.conf}"
CLAUDE_JSON="${WERKSTATT_CLAUDE_JSON:-$HOME/.claude.json}"
EMPTY=".werkstatt"   # the empty session's window: a leading dot, which no checkout's name has (valid)
t() { tmux -f "$CONF" "$@"; }

# a checkout's name crosses into a shell command and a tmux target: no
# dot first (hidden, or . and ..), no dash first, no slash, no colon
valid() { case "$1" in '' | .* | -* | */* | *:*) return 1 ;; esac; }

window() { # window <name>: the window's index, or nothing
  { t list-windows -t "=$S" -F '#{window_index} #{window_name}' 2>/dev/null || true; } |
    awk -v n="$1" '{ i = $1; sub(/^[0-9]+ /, "") } $0 == n { print i }'
}

start() { # start <name>
  local name="$1" path="$WS/$1" q cmd
  q=$(printf '%q' "$name")
  # in a checkout with no conversation yet --continue exits 1: start fresh
  # LAIB_SESSION: the name this session's own claude token is asked for under (#380)
  cmd="LAIB_SESSION=$q $CLAUDE --continue --remote-control $q || LAIB_SESSION=$q $CLAUDE --remote-control $q"
  # a checkout another uid made (a restore, a root clone): git refuses it otherwise
  git config --global --replace-all --fixed-value safe.directory "$path" "$path" 2>/dev/null || true
  if t has-session -t "=$S" 2>/dev/null; then
    t new-window -d -t "=$S:" -n "$name" -c "$path" "$cmd"
  else
    t new-session -d -s "$S" -n "$name" -c "$path" "$cmd"
  fi
}

# Claude Code's first-run questions, answered for the checkouts (#498): the
# onboarding (colour scheme, login hints) and the folder trust, both in
# ~/.claude.json. Unanswered, a session started in its window waits for
# somebody to answer and never reaches Remote Control. The file is written
# only when something is missing, with its mode kept; one that is not JSON
# is left alone. Everything else in it stays.
settle() { # settle <path>
  local f="$CLAUDE_JSON" tmp
  command -v jq >/dev/null || { echo "werkstatt: WARNING: no jq, $1 keeps Claude Code's first-run questions" >&2; return 0; }
  [ -s "$f" ] || { printf '{}\n' > "$f" && chmod 600 "$f"; }
  jq -e --arg p "$1" '.hasCompletedOnboarding == true and .projects[$p].hasTrustDialogAccepted == true' "$f" >/dev/null 2>&1 && return 0
  tmp="$f.werkstatt-$$"
  if jq --arg p "$1" '.hasCompletedOnboarding = true | .projects[$p].hasTrustDialogAccepted = true' "$f" > "$tmp" 2>/dev/null; then
    chmod --reference="$f" "$tmp" && mv -f "$tmp" "$f"
  else
    rm -f "$tmp"
    echo "werkstatt: WARNING: $f is not JSON, left alone; $1 keeps Claude Code's first-run questions" >&2
  fi
}

pass() {
  local d name idx
  for d in "$WS"/*/; do
    name=$(basename -- "$d")
    valid "$name" && [ -f "$d/CLAUDE.md" ] && [ ! -e "$OFF/$name" ] || continue
    settle "$WS/$name"
    idx=$(window "$name")
    if [ -z "$idx" ]; then
      start "$name"
      echo "werkstatt: Claude session $name started"
    elif [ "$(t display-message -p -t "=$S:$idx" '#{pane_dead}')" = 1 ]; then
      t respawn-pane -t "=$S:$idx"
      echo "werkstatt: Claude session $name had ended, started again"
    fi
  done
  # no checkout yet: still a session a login can attach to, with a window
  # no checkout can be named; once a checkout has its window, it goes
  # (unless somebody is attached and may be using it)
  if ! t has-session -t "=$S" 2>/dev/null; then
    t new-session -d -s "$S" -n "$EMPTY" -c "$WS"
  elif [ "$(t list-windows -t "=$S" | wc -l)" -gt 1 ] && [ "$(t display-message -p -t "=$S:" '#{session_attached}')" = 0 ]; then
    idx=$(window "$EMPTY")
    [ -z "$idx" ] || t kill-window -t "=$S:$idx"
  fi
}

case "${1:-}" in
"") pass ;;
off | on)
  name="${2:-}"
  valid "$name" || { echo "usage: werkstatt-sessions off|on <checkout>" >&2; exit 2; }
  mkdir -p "$OFF"
  if [ "$1" = off ]; then
    : > "$OFF/$name"
    idx=$(window "$name")
    [ -z "$idx" ] || t kill-window -t "=$S:$idx"
    echo "werkstatt: Claude session $name is off (also after a restart)"
  else
    rm -f -- "$OFF/$name"
    pass
  fi
  ;;
*) echo "usage: werkstatt-sessions [off|on <checkout>]" >&2; exit 2 ;;
esac
