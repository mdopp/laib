#!/bin/bash
# laib-self-update (#547), as core, before the nightly image run
# (laib-images.service ExecStartPre, rendered from box.yml updates): Laib
# follows its own releases the way the box follows its images. The target is
# the newest release that has its binaries, or the version box.yml
# updates.laib holds; off only looks, and auto never goes back to an older
# version. It installs through that release's own install-box.sh, verified
# against the release's SHA256SUMS, and only while no backup runs, nobody
# holds laib's lock and laib is green. Afterwards /ready must report the new
# version green; else the binaries that ran before come back, the old laib
# converges the box back to its own rendering, and that release is not tried
# again (a newer one is). An install that stops before it changed a binary
# is no verdict on the release: the next night tries again. Every run leaves
# Home/self-update.json for laib diag and laib updates.
set -u
HOME_DIR="${LAIB_HOME:-/mnt/data/laib}"; BIN="${LAIB_BIN_DIR:-/var/opt/laib}"
API="${LAIB_RELEASES:-https://api.github.com/repos/mdopp/laib/releases}"   # these four: the test harness only
DL="${LAIB_DOWNLOAD:-https://github.com/mdopp/laib/releases/download}"
READY="${LAIB_READY_URL:-http://127.0.0.1:5900/ready}"
WAIT="${LAIB_SELF_UPDATE_WAIT:-180}"         # seconds for /ready to be green on the new version
LIMIT="${LAIB_SELF_UPDATE_TIMEOUT:-1200}"    # seconds the installer may take; a stalled download must not hold the night
MODE="${LAIB_UPDATE:-auto}"                  # box.yml updates.laib: auto, off, or the version to hold
REC="$HOME_DIR/self-update.json"
say() { echo "laib-self-update: $*"; }
laib() { LAIB_HOME="$HOME_DIR" LAIB_ACTOR=laib "$BIN/laib" "$@"; }
note() { laib note --kind "$1" "$2" >/dev/null 2>&1 || true; }
version() { "$BIN/laib" version --json 2>/dev/null | jq -r '.version // empty' 2>/dev/null; }
# the last run's marks: the release that was rolled back, since when the box is behind
failed="$(jq -r '.failed // empty' "$REC" 2>/dev/null)"; since="$(jq -r '.behindSince // empty' "$REC" 2>/dev/null)"
record() {
  local behind=""
  if [ -n "$target" ] && newer "$target" "$running" && [ "$1" != updated ]; then behind="${since:-$(date -u +%FT%TZ)}"; fi
  [ -n "$target" ] || behind="$since"   # the releases were not readable: nothing new is known
  jq -n --arg at "$(date -u +%FT%TZ)" --arg running "$running" --arg latest "$target" --arg result "$1" --arg detail "$2" \
    --arg failed "$failed" --arg behindSince "$behind" '{$at, $running, $latest, $result, $detail, $failed, $behindSince}' >"$REC.tmp" && mv "$REC.tmp" "$REC"
  say "$1: $2"
}
green() { curl -fsS --max-time 10 "$READY" 2>/dev/null | jq -e --arg v "$1" '.state == "bereit" and .red == 0 and ($v == "" or .version == $v)' >/dev/null 2>&1; }
newer() { [ "$1" != "$2" ] && [ "$(printf '%s\n%s\n' "${1#v}" "${2#v}" | sort -V | tail -1)" = "${1#v}" ]; }   # $1 is newer than $2

running="$(version)"; target=""
[ -n "$running" ] || { say "no laib in $BIN that tells its version"; record skipped "$BIN/laib version --json antwortet nicht"; exit 1; }
case "$MODE" in
  v*) target="$MODE" ;;
  *) target="$(curl -fsSL --max-time 30 "$API?per_page=20" | jq -r '[.[] | select((.draft or .prerelease) | not) | select(any(.assets[]; .name == "laib-linux-amd64"))][0].tag_name // empty' 2>/dev/null)" ;;
esac
[ -n "$target" ] || { record skipped "die Releases sind gerade nicht lesbar"; exit 0; }
[ "$running" != "$target" ] || { record current "läuft $running"; exit 0; }
if [ "$MODE" = auto ] && ! newer "$target" "$running"; then record current "läuft $running, neuer als das jüngste Release $target"; exit 0; fi
[ "$MODE" != off ] || { record off "aus (box.yml updates.laib: off): läuft $running, neu ist $target"; exit 0; }
[ "$failed" != "$target" ] || { record failed "$target wurde schon zurückgenommen: erst ein neueres wird versucht"; exit 0; }
case "$(systemctl --user is-active laib-backup.service 2>/dev/null)" in
  active | activating) record skipped "die Sicherung läuft: $target wartet auf die nächste Nacht"; exit 0 ;;
esac
if [ -e "$HOME_DIR/apply.lock" ] && ! flock -n "$HOME_DIR/apply.lock" true; then
  record skipped "gesperrt durch $(cat "$HOME_DIR/apply.lock"): $target wartet auf die nächste Nacht"; exit 0
fi
green "" || { record skipped "laib ist nicht grün: $target wartet auf die nächste Nacht"; exit 0; }

tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
for f in SHA256SUMS laib-install-box.sh; do
  curl -fsSL --max-time 120 -o "$tmp/$f" "$DL/$target/$f" || { record skipped "$target: $f lässt sich nicht laden"; exit 0; }
done
want="$(grep ' laib-install-box.sh$' "$tmp/SHA256SUMS" | cut -d' ' -f1)"
[ -n "$want" ] || { failed="$target"; record failed "$target trägt kein install-box.sh in SHA256SUMS: von Hand aktualisieren"; exit 0; }
[ "$want" = "$(sha256sum "$tmp/laib-install-box.sh" | cut -d' ' -f1)" ] || { failed="$target"; record failed "$target: install-box.sh passt nicht zu SHA256SUMS"; exit 0; }

# the binaries that run now, for the way back (*.prev of a binary the last
# update did not change is older than what runs). laib-operator,
# laib-notify, laib-haushalt and laib-lease are containers whose images the
# laib that renders them pins (ADR 0027): they come back with the old
# laib's converge below.
mkdir "$tmp/keep"
for b in laib laib-disk; do [ ! -f "$BIN/$b" ] || cp -p "$BIN/$b" "$tmp/keep/"; done
changed() { for f in "$tmp/keep"/*; do cmp -s "$f" "$BIN/$(basename "$f")" || return 0; done; return 1; }
say "$running → $target"
LAIB_SELF_UPDATE=1 LAIB_BIN_DIR="$BIN" timeout "$LIMIT" bash "$tmp/laib-install-box.sh" --version "$target" >"$HOME_DIR/self-update.log" 2>&1
rc=$?
if [ "$rc" != 0 ] && ! changed; then
  record skipped "install-box.sh brach ab, bevor es ein Binary tauschte (Exit $rc): $(tail -2 "$HOME_DIR/self-update.log" | tr '\n' ' ')"; exit 0
fi
if [ "$rc" = 0 ]; then
  end=$((SECONDS + WAIT))
  while [ "$SECONDS" -lt "$end" ]; do
    green "$target" && { record updated "$running → $target"; note watch "Laib aktualisiert: $running → $target"; exit 0; }
    sleep 1
  done
  why="nach dem Update nicht grün ($(curl -fsS --max-time 10 "$READY" 2>/dev/null | jq -c '{state, red, version}' 2>/dev/null))"
else
  why="install-box.sh brach ab (Exit $rc): $(tail -2 "$HOME_DIR/self-update.log" | tr '\n' ' ')"
fi
for f in "$tmp/keep"/*; do
  sudo install -m 0755 "$f" "$BIN/$(basename "$f").new" && sudo mv -f "$BIN/$(basename "$f").new" "$BIN/$(basename "$f")"
done
systemctl --user try-restart laib.service 2>/dev/null || true
# the new release's converge rendered its own files; the old laib renders its own back
sleep 2; laib converge --force >>"$HOME_DIR/self-update.log" 2>&1
end=$((SECONDS + WAIT)); back="grün"
while ! green "$running"; do [ "$SECONDS" -lt "$end" ] || { back="NICHT grün, ein Mensch muss ran"; break; }; sleep 1; done
failed="$target"
record failed "$target zurückgenommen, $running läuft wieder ($back): $why"
note incident "Laib-Update auf $target zurückgenommen, $running läuft wieder ($back): $why (self-update.log)"
exit 1
