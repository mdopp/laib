#!/bin/bash
# laib-self-update (#547), as core, before the nightly image run
# (laib-images.service ExecStartPre, rendered from box.yml updates): Laib
# follows its own releases the way the box follows its images. The target is
# the newest release that has its binaries, or the version box.yml
# updates.laib holds; off only looks. It installs through that release's own
# install-box.sh, verified against the release's SHA256SUMS, and only while no
# backup runs, nobody holds laib's lock and laib is green. Afterwards /ready
# must report the new version green; else the binaries that ran before come
# back and are restarted, and that release is not tried again (a newer one
# is). Every run leaves Home/self-update.json for laib diag and laib updates.
set -u
HOME_DIR="${LAIB_HOME:-/mnt/data/laib}"; BIN="${LAIB_BIN_DIR:-/var/opt/laib}"
API="${LAIB_RELEASES:-https://api.github.com/repos/mdopp/laib/releases}"   # these four: the test harness only
DL="${LAIB_DOWNLOAD:-https://github.com/mdopp/laib/releases/download}"
READY="${LAIB_READY_URL:-http://127.0.0.1:5900/ready}"
WAIT="${LAIB_SELF_UPDATE_WAIT:-180}"   # seconds for /ready to be green on the new version
MODE="${LAIB_UPDATE:-auto}"   # box.yml updates.laib: auto, off, or the version to hold
REC="$HOME_DIR/self-update.json"
say() { echo "laib-self-update: $*"; }
note() { LAIB_HOME="$HOME_DIR" LAIB_ACTOR=laib "$BIN/laib" note --kind "$1" "$2" >/dev/null 2>&1 || true; }
record() {
  jq -n --arg at "$(date -u +%FT%TZ)" --arg running "$running" --arg latest "$target" --arg result "$1" --arg detail "$2" \
    '{$at, $running, $latest, $result, $detail}' >"$REC.tmp" && mv "$REC.tmp" "$REC"
  say "$1: $2"
}
green() { curl -fsS --max-time 10 "$READY" 2>/dev/null | jq -e --arg v "$1" '.state == "bereit" and .red == 0 and ($v == "" or .version == $v)' >/dev/null 2>&1; }

running="$("$BIN/laib" version 2>/dev/null | jq -r '.version // empty')"
[ -n "$running" ] || { say "no laib in $BIN"; exit 1; }
case "$MODE" in
  v*) target="$MODE" ;;
  *) target="$(curl -fsSL --max-time 30 "$API?per_page=20" | jq -r '[.[] | select((.draft or .prerelease) | not) | select(any(.assets[]; .name == "laib-linux-amd64"))][0].tag_name // empty' 2>/dev/null)" ;;
esac
[ -n "$target" ] || { record skipped "die Releases sind gerade nicht lesbar"; exit 0; }
[ "$running" != "$target" ] || { record current "läuft $running"; exit 0; }
[ "$MODE" != off ] || { record off "aus (box.yml updates.laib: off): läuft $running, neu ist $target"; exit 0; }
if [ "$(jq -r 'select(.result == "failed") | .latest' "$REC" 2>/dev/null)" = "$target" ]; then
  record failed "$(jq -r .detail "$REC")"; exit 0
fi
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
[ -n "$want" ] || { record failed "$target trägt kein install-box.sh in SHA256SUMS: von Hand aktualisieren"; exit 0; }
[ "$want" = "$(sha256sum "$tmp/laib-install-box.sh" | cut -d' ' -f1)" ] || { record failed "$target: install-box.sh passt nicht zu SHA256SUMS"; exit 0; }

# the binaries that run now, for the way back (*.prev of a binary the last
# update did not change is older than what runs)
mkdir "$tmp/keep"
for b in laib laib-operator laib-lease laib-disk laib-notify; do [ ! -f "$BIN/$b" ] || cp -p "$BIN/$b" "$tmp/keep/"; done
say "$running → $target"
why=""
if LAIB_SELF_UPDATE=1 LAIB_BIN_DIR="$BIN" bash "$tmp/laib-install-box.sh" --version "$target" >"$HOME_DIR/self-update.log" 2>&1; then
  end=$((SECONDS + WAIT))
  while [ "$SECONDS" -lt "$end" ]; do
    green "$target" && { record updated "$running → $target"; note watch "Laib aktualisiert: $running → $target"; exit 0; }
    sleep 1
  done
  why="nach dem Update nicht grün ($(curl -fsS --max-time 10 "$READY" 2>/dev/null | jq -c '{state, red, version}' 2>/dev/null))"
else
  why="install-box.sh brach ab: $(tail -2 "$HOME_DIR/self-update.log" | tr '\n' ' ')"
fi
for f in "$tmp/keep"/*; do
  sudo install -m 0755 "$f" "$BIN/$(basename "$f").new" && sudo mv -f "$BIN/$(basename "$f").new" "$BIN/$(basename "$f")"
done
for u in laib.service laib-operator.service laib-lease.service; do systemctl --user try-restart "$u" 2>/dev/null || true; done
end=$((SECONDS + WAIT))
while [ "$SECONDS" -lt "$end" ] && ! green "$running"; do sleep 1; done
record failed "$target zurückgenommen, $running läuft wieder: $why"
note incident "Laib-Update auf $target zurückgenommen, $running läuft wieder: $why (self-update.log)"
exit 1
