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
# The permission mode is the owner's value PERMISSION_MODE (#898):
# `default` asks for a tool call that needs a permission in the app (#383);
# `bypass` runs every tool call without asking, as claude-dev did, a risk
# the owner takes knowingly: the session is reachable from the phone and
# holds an apply-capable Laib hand. The mode is passed on every start, so a
# `--continue` does not fall back to the mode its conversation last ran in,
# and in bypass Claude Code's one-time warning is answered in
# ~/.claude/settings.json (nobody is at the window).
#
#   werkstatt-sessions             one pass (idempotent)
#   werkstatt-sessions off <name>  switch a checkout's session off: a marker
#                                  in ~/.werkstatt/no-autostart (the kept
#                                  home), so a restart keeps it off
#   werkstatt-sessions on <name>   switch it on again, and start it
#
# A window whose session ended with an error stays visible
# (remain-on-exit failed, tmux.conf) and is started again on the next pass;
# a session that was ended cleanly is started again too. Off is the marker,
# and every path that starts a session reads it: the pass, and the window's
# own fresh start after `--continue` (#498: a `--continue` that ended after
# hours started `claude --remote-control flut` again, marker or not).
#
# It runs as dev, whose kept home holds the markers and whose tmux server
# runs the windows. As root (`podman exec` without `-u dev`) `off` wrote the
# marker into root's home and killed nothing in root's empty tmux, and said
# it had (#498): it refuses instead.
set -euo pipefail
WS="${WERKSTATT_WORKSPACE:-/workspace}"
OFF="${WERKSTATT_SESSIONS_OFF:-$HOME/.werkstatt/no-autostart}"
S="${WERKSTATT_TMUX_SESSION:-claude}"
CLAUDE="${WERKSTATT_CLAUDE:-claude}"
CONF="${WERKSTATT_TMUX_CONF:-/etc/tmux.conf}"
CLAUDE_JSON="${WERKSTATT_CLAUDE_JSON:-$HOME/.claude.json}"
case "${WERKSTATT_PERMISSION_MODE:-default}" in
bypass) MODE=--dangerously-skip-permissions ;;
*) MODE="--permission-mode default" ;;
esac
EMPTY=".werkstatt"   # the empty session's window: a leading dot, which no checkout's name has (valid)
t() { tmux -f "$CONF" "$@"; }
AS="${WERKSTATT_USER:-dev}"
if [ "$(id -un)" != "$AS" ]; then
  echo "werkstatt-sessions: runs as $AS, not as $(id -un): its markers are in $AS's home, its windows in $AS's tmux (podman exec -u $AS systemd-werkstatt bash -lc 'werkstatt-sessions …')" >&2
  exit 2
fi

# a checkout's name crosses into a shell command and a tmux target: no
# dot first (hidden, or . and ..), no dash first, no slash, no colon
valid() { case "$1" in '' | .* | -* | */* | *:*) return 1 ;; esac; }

window() { # window <name>: the window's index, or nothing
  { t list-windows -t "=$S" -F '#{window_index} #{window_name}' 2>/dev/null || true; } |
    awk -v n="$1" '{ i = $1; sub(/^[0-9]+ /, "") } $0 == n { print i }'
}

start() { # start <name>
  local name="$1" path="$WS/$1" q off cmd
  q=$(printf '%q' "$name")
  off=$(printf '%q' "$OFF/$name")
  # in a checkout with no conversation yet --continue exits 1: start fresh,
  # unless the session was switched off meanwhile (#498)
  # LAIB_SESSION: the name this session's own claude token is asked for under (#380)
  cmd="LAIB_SESSION=$q $CLAUDE $MODE --continue --remote-control $q || { test -e $off || LAIB_SESSION=$q $CLAUDE $MODE --remote-control $q; }"
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

# bypass (#898): Claude Code asks once whether the mode is meant; the
# owner said so in PERMISSION_MODE, so the answer is written, file kept
bypass_settled() {
  local f="$HOME/.claude/settings.json" tmp
  [ "$MODE" = --dangerously-skip-permissions ] && command -v jq >/dev/null || return 0
  mkdir -p "$HOME/.claude"
  [ -s "$f" ] || printf '{}\n' > "$f"
  jq -e '.skipDangerousModePermissionPrompt == true' "$f" >/dev/null 2>&1 && return 0
  tmp="$f.werkstatt-$$"
  if jq '.skipDangerousModePermissionPrompt = true' "$f" > "$tmp" 2>/dev/null; then
    mv -f "$tmp" "$f"
  else
    rm -f "$tmp"
    echo "werkstatt: WARNING: $f is not JSON, left alone; a bypass session waits at Claude Code's warning" >&2
  fi
}

pass() {
  local d name idx
  bypass_settled
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
