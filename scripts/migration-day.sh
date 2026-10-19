#!/usr/bin/env bash
# shellcheck disable=SC2319  # check $? reads the test just before it, on purpose
# Migration day (#350): docs/MIGRATION.md, step by step, on the ServiceBay box,
# as core. The generalprobe (#248) runs this same script, so the rehearsal
# proves the real one. Where this script has to know something MIGRATION.md
# does not say, MIGRATION.md is the thing to fix. The handoff for the session
# that runs it: docs/MIGRATION-DAY.md.
#
# It runs as a transient systemd user unit (laib-migration), so an SSH session
# that ends does not end it. Every step goes to the unit's journal and to
# $MIG_DIR/log; its result to $MIG_DIR/progress. The first red step stops the
# run, and the service in flight goes back to ServiceBay (MIGRATION.md,
# "Rollback during the weekend"): out of Laib's state, ServiceBay's files back
# in the Quadlet root, its unit started. Immich after its library move is the
# one exception: its photos are in Laib's layout, so its ServiceBay unit stays
# stopped and the run says what to do.
#
#   migration-day.sh --dry-run            every step and every precondition; changes nothing
#   migration-day.sh [--until <step>]     start the unit (the first precondition: a fresh off-box backup)
#   migration-day.sh --resume [--until <step>]   every step that is not green yet, in order
#   migration-day.sh status | wait [seconds] | steps
#   migration-day.sh rollback <service>   hand one service back to ServiceBay; --resume takes it again
#   migration-day.sh rollback all         abort: every service back, Caddy first, in reverse order
#   --no-fresh-backup   the owner's explicit choice to go on without one
#   --foreground        run here instead of in the unit (the unit itself, and CI)
#
# Overrides (the Kraftwerk needs none of them): DOMAIN (ServiceBay's
# publicDomain), LAIB_VERSION (this checkout's version.txt), SRC and
# INSTALL_ARGS (install-box.sh --source / --tls internal: the generalprobe),
# HOOKS (the hooks image, CI), MQTT_USER (mqtt), IMMICH_ADMIN_EMAIL,
# SB_VERSION (5.44.3, what the generalprobe proved), BACKUP_MAX_AGE_H (24),
# MIN_FREE_GB (50), MIG_DIR (~/laib-migration); the paths and tools below for
# the tests.
set -uo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
SELF="$HERE/$(basename "$0")"
Q=${Q:-$HOME/.config/containers/systemd}
PARK=$HOME/servicebay-units
MIG=${MIG_DIR:-$HOME/laib-migration}
DATA=${DATA_DIR:-/mnt/data/stacks}
SB_DATA=${SB_DATA:-/mnt/data/servicebay}
LAIB_BIN=${LAIB_BIN:-/var/opt/laib/laib}
STATE=${LAIB_STATE:-/mnt/data/laib/state}
OUT=${OUT:-/mnt/data/laib/migration}
INSTALL_BOX=${INSTALL_BOX:-$HERE/install-box.sh}
MIGRATE_PY=${MIGRATE_PY:-$HERE/migrate-servicebay.py}
CARRY=${CARRY:-$HERE/carry-servicebay-secrets.sh}
MOVE=${MOVE:-$HERE/move-servicebay-data.sh}
TEMPLATES=${TEMPLATES:-$HERE/../templates}
PY=${PYTHON-python3}
CONTAINERENV=${CONTAINERENV:-/run/.containerenv}
SB_VERSION=${SB_VERSION:-5.44.3}
BACKUP_MAX_AGE_H=${BACKUP_MAX_AGE_H:-24}
MIN_FREE_GB=${MIN_FREE_GB:-50}
MQTT_USER=${MQTT_USER:-mqtt}
UNIT=laib-migration
PATH=$(dirname "$LAIB_BIN"):$PATH
export LAIB_HOME=${LAIB_HOME:-/mnt/data/laib} LAIB_ACTOR=human:core LAIB=$LAIB_BIN PATH

# MIGRATION.md's order: step 1 (install, drafts, secrets), 3 (auth), 4, 4d (caddy), 5 (werkstatt), 6 (ignore), 7 (retire)
STEPS="preflight install drafts secrets auth adguard llama radicale mosquitto file-share beets esphome usage-metrics immich paperless media home-assistant vaultwarden caddy werkstatt ignore retire"
# what stays on its ServiceBay unit, ignored (MIGRATION.md step 6; #160): Solaris until Phase 5,
# chronik until foundry-chronicle has its Laib template (foundry-chronicle#362). The Google import
# goes off with radicale: it writes into radicale's data, a KONFLIKT once radicale is Laib's (#371)
IGNORE=${IGNORE:-solaris solaris-tts solaris-whisper solaris-whisper-batch solaris-wakeword-trainer daggerheart-chronik}

sb_units() {   # the ServiceBay units a step takes out of the Quadlet root
  case $1 in
    llama) echo "llama llama-embed";; caddy) echo nginx;; werkstatt) echo "claude-dev pi-web";;
    radicale) echo "radicale solaris-import-google";;   # #371: the Google import writes into radicale's data
    retire) echo "flutstunde daggerheart-asteroids servicebay";;   # #160: those two go with ServiceBay
    preflight|install|drafts|secrets|ignore) ;;
    *) echo "$1";;
  esac
}
service_step() { [ -n "$(sb_units "$1")" ] && [ "$1" != retire ]; }
say() { printf '\n=== %s\n' "$*"; }
# a verb that meets the apply lock (the converge timer, #211) tries again
laib() { local rc; for _ in $(seq 1 60); do rc=0; "$LAIB_BIN" "$@" || rc=$?; [ "$rc" = 4 ] || return "$rc"; echo "laib $1: the lock is busy, trying again" >&2; sleep "${LAIB_RETRY_SLEEP:-3}"; done; return 4; }
files_of() { local u f; for u in "$@"; do for f in "$Q/$u.kube" "$Q/$u.yml" "$Q/$u.container" "$PARK/$u.kube" "$PARK/$u.yml" "$PARK/$u.container"; do [ -e "$f" ] && echo "$f"; done; done; }
# shellcheck disable=SC2046  # sb_units is a word list
on_box() { [ -n "$(files_of $(sb_units "$1"))" ]; }
record() { mkdir -p "$MIG"; printf '%s %s %s\n' "$1" "$2" "$(date -u +%FT%TZ)" >> "$MIG/progress"; }
last_status() { [ -f "$MIG/progress" ] && awk -v s="$1" '$1 == s {r = $2} END {print r}' "$MIG/progress"; }

# --- preconditions --------------------------------------------------------------------------
sbjson() {   # one field of ServiceBay's config.json; never a secret (only these keys are read)
  python3 -I - "$SB_DATA/config.json" "$1" <<'PY' 2>/dev/null
import json, sys
c = json.load(open(sys.argv[1]))
if sys.argv[2] == "domain":
    print((c.get("reverseProxy") or {}).get("publicDomain", ""))
elif sys.argv[2] == "updates":
    print(bool((c.get("autoUpdate") or {}).get("enabled")), bool((c.get("updateWindow") or {}).get("enabled")))
else:
    b = c.get("externalBackup") or {}
    print(b.get("lastRun", ""), b.get("lastStatus", ""), ",".join(b.get("servicesIncomplete") or []) or "-")
PY
}

# The first precondition: a fresh off-box backup. ServiceBay's NAS backup
# (config.json externalBackup), or a restic repository the owner names
# (RESTIC_REPOSITORY + RESTIC_PASSWORD_FILE, `restic snapshots --latest 1`).
backup_fresh() {
  local run status inc age now
  now=$(date +%s)
  read -r run status inc <<<"$(sbjson backup)"
  if [ -n "$run" ]; then
    age=$(( (now - $(date -d "$run" +%s 2>/dev/null || echo 0)) / 3600 ))
    echo "ServiceBay's NAS backup: last run $run ($age h ago), $status; incomplete: $inc"
    case "$status" in ok|success|partial)
      [ "$age" -le "$BACKUP_MAX_AGE_H" ] && return 0;;
    esac
  else
    echo "ServiceBay's NAS backup: never ran (config.json has no externalBackup.lastRun)"
  fi
  if [ -n "${RESTIC_REPOSITORY:-}" ] && command -v restic >/dev/null; then
    run=$(restic snapshots --latest 1 --json 2>/dev/null | python3 -I -c 'import json,sys; s=json.load(sys.stdin); print(s[-1]["time"] if s else "")')
    if [ -n "$run" ]; then
      age=$(( (now - $(date -d "$run" +%s)) / 3600 ))
      echo "restic $RESTIC_REPOSITORY: latest snapshot $run ($age h ago)"
      [ "$age" -le "$BACKUP_MAX_AGE_H" ] && return 0
    fi
  fi
  return 1
}

PRE_RED=0
check() {   # check <ok?> <what>: one line per precondition
  if [ "$1" = 0 ]; then echo "  ok    $2"; else echo "  RED   $2"; PRE_RED=$((PRE_RED + 1)); fi
}
preconditions() {
  local v free sbv
  PRE_RED=0
  echo "preconditions:"
  [ "$(id -un)" = "${MIG_USER:-core}" ]; check $? "running as ${MIG_USER:-core} (rootless podman, user systemd)"
  [ ! -e "$CONTAINERENV" ]; check $? "on the host, not in a container (claude-dev goes away in the werkstatt step)"
  v=$(loginctl show-user "$(id -un)" -p Linger --value 2>/dev/null)
  [ "$v" = yes ]; check $? "linger is on (the unit outlives the SSH session): ${v:-unknown}"
  command -v python3 >/dev/null; check $? "python3 on the host (migrate-servicebay.py)"
  # what laib shells out to (#367); install-box.sh brings restic, never by layering
  command -v git >/dev/null; check $? "git on the host (laib's state repo; in the CoreOS image)"
  command -v diff >/dev/null; check $? "diff on the host (laib plan; in the CoreOS image)"
  if command -v restic >/dev/null; then echo "  ok    restic on the host: $(restic version 2>/dev/null | cut -d' ' -f2)"
  else echo "  WARN  restic is missing: install puts restic's static release in /usr/local/bin (checksum-pinned, no rpm-ostree)"; fi
  # a staged deployment boots at the next reboot, a power cut included: mid-migration, into an old tree (#367)
  if command -v rpm-ostree >/dev/null; then
    rpm-ostree status --pending-exit-77 >/dev/null 2>&1
    [ $? != 77 ] || echo "  WARN  a staged rpm-ostree deployment: any reboot boots it. Drop it first: sudo rpm-ostree cleanup --pending"
  fi
  if backup_fresh | sed 's/^/        /'; [ "${PIPESTATUS[0]}" = 0 ]; then check 0 "a fresh off-box backup (at most $BACKUP_MAX_AGE_H h old)"
  elif [ -n "${NO_FRESH_BACKUP:-}" ]; then echo "  WARN  no fresh off-box backup: going on because the owner passed --no-fresh-backup"
  else check 1 "no fresh off-box backup (at most $BACKUP_MAX_AGE_H h old): run ServiceBay's backup to the NAS first, or pass --no-fresh-backup"; fi
  if [ "$(last_status install)" != green ]; then   # what step 1 needs while ServiceBay still runs
    [ "$(podman container inspect -f '{{.State.Running}}' servicebay 2>/dev/null)" = true ]; check $? "ServiceBay's container runs (the secrets and templates come out of it)"
    sbv=$(podman exec servicebay node -p "require('/app/package.json').version" 2>/dev/null)
    [ "$sbv" = "$SB_VERSION" ]; check $? "ServiceBay $SB_VERSION, the version the generalprobe proved: the box runs ${sbv:-unknown}"
    podman exec servicebay test -d /app/templates; check $? "ServiceBay's templates at servicebay:/app/templates"
    podman exec servicebay test -d /app/.servicebay/registries || echo "  WARN  no external registries in servicebay:/app/.servicebay/registries"
  fi
  read -r v free <<<"$(sbjson updates)"
  if [ "$v $free" != "False False" ]; then echo "  WARN  ServiceBay's auto-update ($v) or update window ($free) is on: switch both off for the weekend (ServiceBay → Settings), so nothing restarts a service mid-move"; fi
  [ -n "$DOMAIN" ]; check $? "the domain: ${DOMAIN:-none} (ServiceBay's reverseProxy.publicDomain)"
  free=$(df -P -BG "$(dirname "$DATA")" 2>/dev/null | awk 'NR == 2 {sub("G", "", $4); print $4}')
  [ "${free:-0}" -ge "$MIN_FREE_GB" ]; check $? "free on the data disk: ${free:-?} GiB (at least $MIN_FREE_GB)"
  if [ -z "${SRC:-}" ]; then
    [ "v$(cat "$HERE/../version.txt" 2>/dev/null)" = "$LAIB_VERSION" ]; check $? "this checkout is Laib $LAIB_VERSION (scripts and binary from one release): $(cat "$HERE/../version.txt" 2>/dev/null)"
  fi
  if [ -x "$LAIB_BIN" ]; then
    v=$("$LAIB_BIN" version 2>/dev/null | awk '{print $NF}')
    [ "$v" = "$LAIB_VERSION" ]; check $? "the Laib binary on the box is $LAIB_VERSION: ${v:-unknown}"
  else
    echo "  ok    no Laib binary yet: install puts $LAIB_VERSION in place"
  fi
  [ "$PRE_RED" = 0 ]
}

# --- the steps --------------------------------------------------------------------------------
# MIGRATION.md step 4a: ServiceBay's unit stopped, its files out of the Quadlet root
servicebay_out() {
  local u f moved=""
  mkdir -p "$PARK"
  for u in "$@"; do
    systemctl --user stop "$u.service" || [ -z "$(files_of "$u" | grep "^$Q/")" ]
    for f in "$Q/$u.kube" "$Q/$u.yml" "$Q/$u.container"; do
      if [ -e "$f" ]; then mv "$f" "$PARK/"; moved="$moved $(basename "$f")"; fi
    done
  done
  systemctl --user daemon-reload
  echo "ServiceBay's $*: stopped,${moved:- already} out of the Quadlet root (in $PARK)"
}
servicebay_back() {
  local u f
  for u in "$@"; do for f in "$PARK/$u.kube" "$PARK/$u.yml" "$PARK/$u.container"; do if [ -e "$f" ]; then mv "$f" "$Q/"; fi; done; done
  systemctl --user daemon-reload
  for u in "$@"; do
    [ -n "$(files_of "$u" | grep "^$Q/")" ] || continue
    systemctl --user start "$u.service" && echo "ServiceBay's $u.service runs again" || echo "ServiceBay's $u.service did not start: systemctl --user status $u.service"
  done
}

# the copies a red or handed-back attempt made are stale: ServiceBay wrote on (MIGRATION.md, Rollback)
stale_copies() {
  local s=$1 log=$MIG/move-$1.log t
  [ -s "$log" ] || return 0
  sed -n "s/^$s: copy .* -> \(.*\)$/\1/p" "$log" | while read -r t; do
    t=${t%/}
    case "$t" in "$DATA"/*/*) ;; *) echo "not removing $t: not inside a service's data dir"; continue;; esac
    podman unshare rm -rf "$t" && echo "removed the stale copy $t"
  done
  mv "$log" "$log.$(date +%s)"
}
move() { HOOKS_IMAGE="${HOOKS:-ghcr.io/mdopp/laib-hooks:latest}" "$MOVE" "$@"; }
move_logged() { mkdir -p "$MIG"; move "$1" 2>&1 | tee "$MIG/move-$1.log"; return "${PIPESTATUS[0]}"; }

# what only the owner can supply, set before the service goes in (laib secret set);
# the other external secrets of the built-ins are seeded or minted by their hooks
owner_secrets() { case $1 in werkstatt) echo "CLAUDE_CODE_OAUTH_TOKEN LAIB_CLAUDE_TOKEN LAIB_PI_TOKEN WERKSTATT_GIT_TOKEN WERKSTATT_PI_GIT_TOKEN";; esac; }
external_missing() {
  local have s
  [ -n "$(owner_secrets "$1")" ] || return 0
  have=$(laib secret list 2>/dev/null)
  for s in $(owner_secrets "$1"); do
    grep -qx "$s" <<<"$have" || echo "$s"
  done
}

# MIGRATION.md step 4c: the `laib add` line migrate-servicebay.py printed for it
add_line() { sed -n "s/^  \(.*laib add $1\( .*\)\?\)$/\1/p" "$OUT.txt"; }
laib_add() {
  local n=$1 line extra="" rc=0 c
  line=$(add_line "$n")
  case "$n" in caddy|werkstatt) [ -n "$line" ] || line="laib add $n";; esac
  [ -n "$line" ] || { echo "migrate-servicebay.py printed no laib add line for $n ($OUT.txt)"; return 1; }
  # "One value goes with them" (MIGRATION.md, secrets that carry over)
  if [ "$n" = mosquitto ] && [[ "$line" != *MQTT_USER=* ]]; then extra=" MQTT_USER=$MQTT_USER"; fi
  if [ "$n" = immich ] && [[ "$line" != *ADMIN_EMAIL=* ]] && [ -n "${IMMICH_ADMIN_EMAIL:-}" ]; then extra=" ADMIN_EMAIL=$IMMICH_ADMIN_EMAIL"; fi
  if [ -d "$STATE/services/$n" ]; then   # an attempt cut off after its add (MIGRATION.md: a laib add that refused has put it in the state)
    line="laib apply $n" extra=""
    echo "$n is in Laib's state already (an earlier attempt): its apply again"
  fi
  echo "\$ $line$extra"
  laib plan "$n" > "$MIG/plan-$n.txt" 2>&1 || true
  if grep -q KONFLIKT "$MIG/plan-$n.txt"; then echo "laib plan $n still reports KONFLIKT:"; cat "$MIG/plan-$n.txt"; return 1; fi
  eval "$line$extra" || rc=$?
  laib status "$n" || true
  if [ "$rc" != 0 ]; then   # what the apply's red verify cannot say: the containers' own words
    for c in $(podman ps -a --format '{{.Names}}' 2>/dev/null | grep "^systemd-$n" || true); do
      echo "--- $c"; journalctl --user --no-pager -o cat CONTAINER_NAME="$c" 2>/dev/null | tail -30
    done
    echo "--- owners and labels of $n's data"; ls -laZn "$DATA/$n" 2>&1 | head -20
  fi
  return "$rc"
}

# MIGRATION.md step 4c, last line: NPM still serves the moved service's names
# (Vaultwarden's not: it moves last, right before Caddy takes over, 4d)
# Names whose built-in listens on 127.0.0.1 only while ServiceBay's proxy host
# forwards to the LAN address: they answer 502 through NPM until Caddy serves
# them (MIGRATION.md step 2): Vaultwarden, Authelia's portal, llama.
WAITS_FOR_CADDY="vault auth llama"
npm_serves() {
  local n=$1 h code
  for h in $(sed -n 's/^  - {host: \([^,]*\),.*/\1/p' "$OUT/$n/laib.yml" 2>/dev/null); do
    case " $WAITS_FOR_CADDY " in *" $h "*) echo "NPM: $h.$DOMAIN waits for Caddy (4d): Laib's $n listens on 127.0.0.1, NPM forwards to the LAN address (MIGRATION.md)"; continue;; esac
    code=$(curl -s -o /dev/null -w '%{http_code}' -H "Host: $h.$DOMAIN" http://127.0.0.1/ || true)
    echo "NPM: http://$h.$DOMAIN/ -> HTTP $code"
    case "$code" in 2??|3??|401) ;; *) echo "NPM does not serve $h.$DOMAIN after $n moved"; return 1;; esac
  done
}

step_preflight() { set +e; say "preconditions"; preconditions; }   # every check runs, then the verdict

step_install() {
  say "1. Laib beside ServiceBay: the binary $LAIB_VERSION, the state repo, the box's own config"
  local from=(--version "$LAIB_VERSION")
  [ -z "${SRC:-}" ] || from=(--source "$SRC")   # one build served over HTTP: the generalprobe
  # shellcheck disable=SC2086  # INSTALL_ARGS is a list of flags
  bash "$INSTALL_BOX" "${from[@]}" --domain "$DOMAIN" --gpu ${INSTALL_ARGS:-}   # the Kraftwerk is the GPU box
  if [ -n "${HOOKS:-}" ]; then   # this commit's hooks image (CI only; the box takes :latest)
    local b=$STATE/box.yml
    if grep -q "^laib:" "$b"; then sed -i "s|^laib:|laib:\n    hooksImage: $HOOKS|" "$b"; else printf "laib:\n    hooksImage: %s\n" "$HOOKS" >> "$b"; fi
    grep -n hooksImage "$b"
  fi
  laib plan 2>&1 | head -40 || true
}

drafts() {   # drafts <out>: ServiceBay's templates as the box runs them, and the drafts
  local out=$1 tpl=$2 regs
  rm -rf "$tpl"; mkdir -p "$tpl"
  podman cp servicebay:/app/templates "$tpl/servicebay"
  podman cp servicebay:/app/.servicebay/registries "$tpl/registries" 2>/dev/null || mkdir -p "$tpl/registries"
  regs=$(ls -d "$tpl"/registries/*/templates 2>/dev/null | paste -sd, -) || true
  ${PY:+"$PY"} "$MIGRATE_PY" --config "$SB_DATA/config.json" --templates "$tpl/servicebay${regs:+,$regs}" --out "$out" | tee "$out.txt"
  return "${PIPESTATUS[0]}"
}
step_drafts() {
  say "1. the drafts: migrate-servicebay.py reads ServiceBay's config once"
  drafts "$OUT" "$HOME/servicebay-templates"
  if on_box immich && [[ "$(add_line immich)" != *ADMIN_EMAIL=* ]] && [ -z "${IMMICH_ADMIN_EMAIL:-}" ]; then
    echo "immich: the line has no ADMIN_EMAIL and IMMICH_ADMIN_EMAIL is not set: Immich → Administration → Users, then IMMICH_ADMIN_EMAIL=<it> … --resume"
    return 1
  fi
}

step_secrets() {
  say "1. the secrets the data was made with, carried while ServiceBay still runs (#228)"
  local carry
  carry=$(sed -n 's/^ServiceBay still runs: scripts\/carry-servicebay-secrets.sh \(.*\)$/\1/p' "$OUT.txt")
  [ -n "$carry" ] || { echo "migrate-servicebay.py printed no carry line"; return 1; }
  echo "\$ scripts/carry-servicebay-secrets.sh $carry"
  # shellcheck disable=SC2086
  LAIB="$LAIB_BIN" "$CARRY" $carry
  carry_smtp || return 1
  laib secret list
}

# ServiceBay's mail password (#359), beside the settings the auth line carries: plain in
# its config.json (notifications.email.pass), read by python and piped into laib secret
# set, never on argv, in a variable or a file. Only a complete setting, as ServiceBay's
# auth hook took it; without one, Authelia writes its mails into a file and diag says so.
smtp_py='import json, sys
em = (json.load(open(sys.argv[1])).get("notifications") or {}).get("email") or {}
ok = all(em.get(k) for k in ("host", "user", "pass", "from"))
sys.stdout.write(em["pass"] if ok and sys.argv[2] == "pass" else "yes" if ok else "no")'
carry_smtp() {
  if [ "$(python3 -I -c "$smtp_py" "$SB_DATA/config.json" ok 2>/dev/null)" != yes ]; then
    echo "ServiceBay has no complete mail setting (notifications.email): Laib's Authelia writes its mails into a file (laib diag says so)"
    return 0
  fi
  python3 -I -c "$smtp_py" "$SB_DATA/config.json" pass | "$LAIB_BIN" secret set AUTHELIA_SMTP_PASSWORD >/dev/null ||
    { echo "AUTHELIA_SMTP_PASSWORD: not set from ServiceBay's notifications.email"; return 1; }
  echo "AUTHELIA_SMTP_PASSWORD set from ServiceBay's notifications.email (the settings go with laib add auth)"
}

# a step's images, pulled while ServiceBay's unit still runs: its AdGuard is the box's resolver,
# and once it is stopped no pull resolves (#370). Best effort: the apply pulls again.
prepull() {
  local img
  for img in $(sed -n 's/^[A-Z_]*IMAGE: *"\{0,1\}\([^" ]*\)"\{0,1\} *$/\1/p' "$TEMPLATES/$1/values.example.yml" 2>/dev/null); do
    if podman pull -q "$img" >/dev/null; then echo "pulled $img"; else echo "WARN  pulling $img failed: laib apply tries again"; fi
  done
}

step_service() {   # MIGRATION.md step 3 and 4, a–c
  local s=$1 miss
  say "4. $s"
  on_box "$s" || { echo "$s: ServiceBay does not run it here: skipped"; return 10; }
  miss=$(external_missing "$s")
  [ -z "$miss" ] || { echo "$s: set these first, nothing changed yet: $(for m in $miss; do printf 'laib secret set %s  ' "$m"; done)"; return 1; }
  miss=$(konflikte | grep "at the $s step$" || true)
  [ -z "$miss" ] || { echo "$miss"; echo "$s: laib add would refuse (KONFLIKT), nothing changed yet"; return 1; }
  prepull "$s"
  # shellcheck disable=SC2046
  servicebay_out $(sb_units "$s")
  case "$s" in
    vaultwarden|home-assistant|file-share|radicale|werkstatt) stale_copies "$s"; move_logged "$s";;
    immich)   # "Immich's external libraries": both Syncthings stopped (ServiceBay's is out with file-share)
      systemctl --user stop file-share-syncthing.service || true
      local rc=0
      { move --dry-run immich && move_logged immich; } || rc=$?
      systemctl --user start file-share-syncthing.service || true
      [ "$rc" = 0 ] || return "$rc";;
  esac
  laib_add "$s"
  npm_serves "$s"
}

step_caddy() {
  say "4d. Caddy replaces NPM"
  on_box caddy || { echo "no NPM here: skipped"; return 10; }
  prepull caddy
  public_dns "$STATE"/services/*/laib.yml || { echo "caddy: NPM stays, nothing changed yet"; return 1; }
  servicebay_out nginx
  laib_add caddy
  # every name of the state through Caddy: none answers 502
  local ca=$DATA/caddy/data/caddy/pki/authorities/local/root.crt h code bad="" cacert=()
  for _ in $(seq 1 30); do [ -s "$ca" ] && break; [ -z "${INSTALL_ARGS:-}" ] && break; sleep 2; done
  [ -s "$ca" ] && cacert=(--cacert "$ca")
  for h in $(cat "$STATE"/services/*/laib.yml 2>/dev/null | sed -n 's/^ *- *{\{0,1\}host: *\([a-z0-9-]*\).*/\1/p' | sort -u); do
    for _ in $(seq 1 12); do   # a Let's Encrypt certificate can take a minute per name
      code=$(curl -s "${cacert[@]}" --resolve "$h.$DOMAIN:443:127.0.0.1" -o /dev/null -w '%{http_code}' "https://$h.$DOMAIN/" || true)
      case "$code" in 2??|3??|401|403) break;; esac
      sleep "${CADDY_RETRY_SLEEP:-10}"
    done
    echo "Caddy: https://$h.$DOMAIN/ -> HTTP $code"
    case "$code" in 2??|3??|401|403) ;; *) bad="$bad $h";; esac
  done
  [ -z "$bad" ] || { echo "Caddy does not serve:$bad"; return 1; }
  echo "AdGuard's rewrites, if any, must point at the box as before: check dns.$DOMAIN by hand"
}

step_ignore() {
  say "6. what does not move stays on its ServiceBay unit, ignored: $IGNORE"
  local u f any=""
  for u in $IGNORE; do
    for f in "$Q/$u.kube" "$Q/$u.yml" "$Q/$u.container"; do
      if [ -e "$f" ]; then laib ignore "$(basename "$f")"; any=1; fi
    done
  done
  [ -n "$any" ] || { echo "none of them here: skipped"; return 10; }
}

step_retire() {
  say "7. ServiceBay off (its data dir stays 30 days)"
  on_box retire || { echo "no ServiceBay here: skipped"; return 10; }
  # shellcheck disable=SC2046
  servicebay_out $(sb_units retire)
  laib status || true
  local left
  left=$(laib plan --json 2>/dev/null | python3 -I -c 'import json,sys; print(" ".join(json.load(sys.stdin).get("unmanaged") or []))' 2>/dev/null || true)
  [ -z "$left" ] || { echo "units no service and no ignore.yml line claims: $left (laib adopt or laib ignore, then --resume)"; return 1; }
}

run_step() {
  case $1 in
    preflight) step_preflight;; install) step_install;; drafts) step_drafts;; secrets) step_secrets;;
    caddy) step_caddy;; ignore) step_ignore;; retire) step_retire;;
    *) step_service "$1";;
  esac
}

# --- the service in flight goes back to ServiceBay ---------------------------------------------
handback() {
  local s=$1 n f
  n=$s
  if [ "$s" = immich ] && [ -e "$DATA/immich/library-move.json" ]; then
    echo "immich: NOT handed back. Its photos are already in Laib's layout, and ServiceBay's Immich would"
    echo "  find its libraries empty. ServiceBay's immich stays stopped. Fix forward: read the log above,"
    echo "  then --resume (the move finishes from its journal). Or roll back by hand: docs/MIGRATION.md,"
    echo "  'Rollback during the weekend', immich (rename the photos back, pg_restore the dump)."
    return 1
  fi
  if [ -d "$STATE/services/$n" ]; then
    # a role it holds is released first (laib remove refuses otherwise); its next laib add claims it again
    if [ -f "$STATE/box.yml" ] && grep -Eq "(proxy|sso|identity|dns|model): $n([,} ]|$)" "$STATE/box.yml"; then
      sed -i -E "s/(proxy|sso|identity|dns|model): $n([,} ]|$)/\1: \"\"\2/g" "$STATE/box.yml"
      echo "box.yml: the roles $n held are free again"
    fi
    laib remove "$n" || echo "laib remove $n failed: laib status $n"
  fi
  if [ "$s" = home-assistant ]; then
    for f in "$DATA/home-assistant/zwave-js/settings.json" "$DATA/home-assistant/homeassistant/.storage/core.config_entries" "$DATA/home-assistant/homeassistant/configuration.yaml"; do
      podman unshare sh -c "test -e '$f.servicebay' && mv '$f.servicebay' '$f'" && echo "put back $f"
    done
  fi
  # shellcheck disable=SC2046
  servicebay_back $(sb_units "$s")
}

# --- the commands -----------------------------------------------------------------------------
valid_step() { case " $STEPS " in *" $1 "*) return 0;; esac; echo "unknown step $1 (steps: $STEPS)" >&2; return 1; }

status() {
  echo "laib-migration: $(systemctl --user is-active "$UNIT" 2>/dev/null || true)"
  local s st
  for s in $STEPS; do
    st=$(last_status "$s")
    printf '  %-15s %s\n' "$s" "${st:-pending}"
  done
  [ -f "$MIG/log" ] && { echo "--- the last lines of $MIG/log"; tail -15 "$MIG/log"; }
  return 0
}

unclaimed() {   # Quadlet entries no step takes and IGNORE does not keep: the retire step is red while one is left
  local f base u claimed left=""
  for f in "$Q"/*; do
    [ -e "$f" ] || continue
    base=$(basename "$f"); base=${base%.*}
    claimed=""
    for u in $IGNORE $(for s in $STEPS; do sb_units "$s"; done); do [ "$u" = "$base" ] && claimed=1; done
    [ -n "$claimed" ] || [ -d "$STATE/services/$base" ] || left="$left $(basename "$f")"
  done
  if [ -n "$left" ]; then echo "  note  in the Quadlet root, claimed by no step and not in IGNORE:$left"
    echo "        retire stays red while they are there: move them out, or IGNORE=\"… them\" for laib ignore"; fi
}

# laib plan's KONFLIKT (internal/plan Conflicts, #227) ahead of the run (#371): at each step, the
# services Laib holds by then against every ServiceBay unit still in the Quadlet root (IGNORE, not
# claimed, or a later step's): the same host port, or a host path at, inside or above a backed-up
# data path of the service's template (at or below a backup: false path is shared, not its own)
konflikt_py='import os, re, sys
q, data, tpl = sys.argv[1:4]
yaml_re = re.compile(r"(?m)^Yaml=(.+)$")
port_re = re.compile(r"""(?m)(?:^PublishPort=(?:\[[0-9a-fA-F:.]*\]:|[0-9.]+:)?(\d+):|hostPort:\s*["\x27]?(\d+))""")
path_re = re.compile(r"""(?m)(?:^Volume=|^Mount=.*\b(?:source|src)=|hostPath:\s*\n\s*(?:type:.*\n\s*)?path:\s*["\x27]?)(/[^\s:,"\x27#]*)""")
inside = lambda a, b: a == b or a.startswith(b.rstrip("/") + "/")
def contract(s):
    own, shared, ports, block, last = [], [], set(), "", None
    for line in open(os.path.join(tpl, s, "laib.yml")):
        line = line.split("#")[0].rstrip()
        if re.match(r"^[a-z]", line): block = line.split(":")[0]
        m = re.match(r"^\s*- path: *(\S+)", line)
        if block == "data" and m: last = os.path.realpath(m.group(1).replace("${DATA_DIR}", data)); own.append(last)
        elif block == "data" and re.match(r"^\s+backup: *false", line) and last: own.remove(last); shared.append(last); last = None
        m = re.search(r"\bport: *(\d+)", line)
        if block == "ports" and m: ports.add(m.group(1))
    return own, shared, ports
steps = [a.split("=") for a in sys.argv[4:]]
held, taken, seen = [], set(), set()
for step, units in steps:
    held.append(step); taken |= set(units.split(","))
    for f in sorted(os.listdir(q)):
        base, ext = os.path.splitext(f)
        if ext not in (".kube", ".container") or base in taken: continue
        text = open(os.path.join(q, f)).read()
        m = yaml_re.search(text)
        if m:
            y = m.group(1).strip()
            try: text += open(y if os.path.isabs(y) else os.path.join(q, y)).read()
            except OSError: pass
        for s in held:
            try: own, shared, ports = contract(s)
            except OSError: continue
            why = ["host port " + a + b for a, b in port_re.findall(text) if a + b in ports]
            for p in path_re.findall(text):
                h = os.path.realpath(p)
                if any(inside(h, o) or inside(o, h) for o in own) and not any(inside(h, d) for d in shared): why.append("data " + p)
            if why and (s, f) not in seen:
                seen.add((s, f)); print("  RED   %s: %s runs it too (%s): it is still in the Quadlet root at the %s step" % (s, f, ", ".join(why), step))'
konflikte() {   # konflikte: RED lines, none when every step can take its service
  local s args=()
  for s in $STEPS; do
    service_step "$s" && on_box "$s" && args+=("$s=$(sb_units "$s" | tr ' ' ,)")
  done
  [ "${#args[@]}" -gt 0 ] || return 0
  python3 -I -c "$konflikt_py" "$Q" "$DATA" "$TEMPLATES" "${args[@]}"
}

# the carried admin passwords the post-deploy logs in with, tried read-only against the app
# ServiceBay still runs (#373): one changed inside the app after ServiceBay set it is red here,
# not at the post-deploy after the move. <drafts.txt>
logins() {
  local s v pw user out lrc red=0
  for s in home-assistant adguard; do
    on_box "$s" || continue
    case $s in home-assistant) v=HA_ADMIN_USER pw=HA_ADMIN_PASSWORD;; adguard) v=ADMIN_USER pw=ADGUARD_ADMIN_PASSWORD;; esac
    user=$(sed -n "s/^.*laib add $s .*\b$v=\([^ ]*\).*$/\1/p" "$1" | head -1)
    [ -n "$user" ] || user=$(sed -n "s/^$v: *\([^ #]*\).*/\1/p" "$TEMPLATES/$s/values.example.yml")
    out=$("$CARRY" --login "$s" "$user" 2>&1); lrc=$?
    case $lrc in
      0) echo "  ok    $s: the carried administrator login works ($out)";;
      1) echo "  RED   $s: the carried administrator login, user $user, meldet sich nicht an ($out)"
         echo "        after the secrets step: laib secret set $pw with the password the app has now (--until secrets, set it, --resume)"
         red=1;;
      *) echo "  WARN  $s: the carried administrator login was not tried ($out)";;
    esac
  done
  return "$red"
}

# Let's Encrypt by HTTP-01 (no dns.provider, not Caddy's own CA) needs a public record per name (#377):
# the route names of the state-to-be, resolved against a public resolver (DNS over HTTPS, JSON),
# not the box's AdGuard, whose rewrites answer every name. PUBLIC_DNS overrides it.
http01() {
  if [ -f "$STATE/box.yml" ]; then
    ! grep -Eq '^tls: *"?internal|^ +provider: *"?[a-z]' "$STATE/box.yml"
  else
    case " ${INSTALL_ARGS:-} " in *" --tls internal "*|*" --dns-provider "*) return 1;; esac
  fi
}
route_names() {   # route_names <laib.yml …>: every host and redirectFrom name, as FQDNs ("@" is the apex)
  sed -n -e 's/^ *- *{\{0,1\}host: *\([a-z0-9-]*\).*/\1/p' -e 's/^ *redirectFrom: *\[\(.*\)\].*/\1/p' "$@" 2>/dev/null |
    tr ',' '\n' | tr -d ' "' | sed -e "s/^@$/$DOMAIN/" -e "/\./!s/\$/.$DOMAIN/" -e '/^\.\|^$/d' | sort -u
}
public_py='import json, sys, urllib.request
doh, missing = sys.argv[1], []
for n in sys.argv[2:]:
    found = False
    for t in ("A", "AAAA"):
        r = urllib.request.Request(doh + "?name=" + n + "&type=" + t, headers={"Accept": "application/dns-json"})
        try:
            with urllib.request.urlopen(r, timeout=10) as f:
                found = found or any(a.get("type") in (1, 28) for a in json.load(f).get("Answer") or [])
        except (OSError, ValueError):
            sys.exit(2)
    if not found: missing.append(n)
print(", ".join(missing)); sys.exit(1 if missing else 0)'
public_dns() {   # public_dns <laib.yml …>: one ok, RED or WARN line; 1 when a name has no record
  local names out rc
  http01 || return 0
  names=$(route_names "$@")
  [ -n "$names" ] || return 0
  # shellcheck disable=SC2086  # one name per word
  out=$(python3 -I -c "$public_py" "${PUBLIC_DNS:-https://dns.google/resolve}" $names 2>/dev/null); rc=$?
  case $rc in
    0) echo "  ok    every route name has a public DNS record (HTTP-01): $(echo $names)";;
    1) echo "  RED   öffentlicher DNS-Eintrag fehlt: $out"
       echo "        Let's Encrypt's HTTP-01 needs one per name: a CNAME to $DOMAIN at the DNS provider before the caddy step, or box.yml dns.provider (DNS-01, a wildcard)"
       return 1;;
    *) echo "  WARN  the public DNS records were not checked (${PUBLIC_DNS:-https://dns.google/resolve} did not answer): check $(echo $names) by hand";;
  esac
}
templates_of_steps() {   # the templates the run adds on this box
  local s
  for s in $STEPS; do service_step "$s" && on_box "$s" && echo "$TEMPLATES/$s/laib.yml"; done
}

dry_run() {
  local s st rc=0 tmp
  say "dry run: nothing on this box changes"
  echo "domain: $DOMAIN  Laib: $LAIB_VERSION  ServiceBay's units: $Q  progress: $MIG/progress"
  preconditions || rc=1
  say "the steps, in MIGRATION.md's order"
  for s in $STEPS; do
    st=$(last_status "$s")
    if service_step "$s" && ! on_box "$s"; then st="${st:-skip: ServiceBay does not run it here}"; fi
    case $s in
      preflight) d="the preconditions above";;
      install) d="install-box.sh $([ -n "${SRC:-}" ] && echo "--source $SRC" || echo "--version $LAIB_VERSION") --domain $DOMAIN --gpu ${INSTALL_ARGS:-}";;
      drafts) d="ServiceBay's templates out of its container, migrate-servicebay.py -> $OUT.txt";;
      secrets) d="carry-servicebay-secrets.sh, the line migrate-servicebay.py prints; ServiceBay's mail password (notifications.email)";;
      caddy) d="NPM out, laib add caddy, every name through Caddy";;
      ignore) d="laib ignore what stays: $IGNORE";;
      retire) d="out with ServiceBay: $(sb_units retire)";;
      *) d="ServiceBay's $(sb_units "$s") out, its data moved where MIGRATION.md says, its laib add line";;
    esac
    printf '  %-15s %-40s %s\n' "$s" "${st:-pending}" "$d"
  done
  unclaimed
  # shellcheck disable=SC2046
  public_dns $(templates_of_steps) || rc=1
  st=$(konflikte)
  if [ -n "$st" ]; then echo "$st"; echo "        laib add refuses these (KONFLIKT): stop the unit with the step that takes the service, or drop its mount"; rc=1; fi
  if [ "$(last_status drafts)" != green ] && [ "$(podman container inspect -f '{{.State.Running}}' servicebay 2>/dev/null)" = true ]; then
    say "the drafts, into a scratch directory (what the drafts step will print)"
    tmp=$(mktemp -d)
    drafts "$tmp/migration" "$tmp/templates" || rc=1
    if on_box immich && ! grep -q "laib add immich.*ADMIN_EMAIL=" "$tmp/migration.txt" && [ -z "${IMMICH_ADMIN_EMAIL:-}" ]; then
      echo "  RED   immich: set IMMICH_ADMIN_EMAIL=<Immich's admin address> (Immich → Administration → Users)"; rc=1
    fi
    on_box mosquitto && ! grep -q "laib add mosquitto.*MQTT_USER=" "$tmp/migration.txt" && echo "  note  mosquitto: MQTT_USER=$MQTT_USER goes on its line (the box's broker user)"
    logins "$tmp/migration.txt" || rc=1
    rm -rf "$tmp"
  fi
  [ "$rc" = 0 ] && echo "dry run: every precondition holds" || echo "dry run: RED, see above"
  return "$rc"
}

run() {
  local s st rc todo=()
  mkdir -p "$MIG"
  exec 9>"$MIG/lock"
  flock -n 9 || { echo "another migration-day.sh holds $MIG/lock" >&2; return 1; }
  if [ -s "$MIG/progress" ] && [ -z "$RESUME" ]; then
    echo "a migration ran here already ($MIG/progress): --resume continues it, status shows where" >&2; return 1
  fi
  for s in $STEPS; do
    st=$(last_status "$s")
    [ "$st" = green ] || [ "$st" = skip ] || todo+=("$s")
    [ "$s" = "$UNTIL" ] && break
  done
  echo "migration-day.sh $LAIB_VERSION on $(hostname): ${todo[*]:-nothing left to do}"
  for s in "${todo[@]}"; do
    ( set -euo pipefail; run_step "$s" ); rc=$?
    if [ "$rc" = 0 ]; then record "$s" green
    elif [ "$rc" = 10 ]; then record "$s" skip
    else
      record "$s" red
      echo
      echo "RED at $s (exit $rc). The run stops here."
      if service_step "$s"; then
        echo "--- $s goes back to ServiceBay (docs/MIGRATION.md, Rollback during the weekend)"
        ( handback "$s" ) || true
      fi
      if service_step "$s"; then echo "The box is in a known state: every step before $s is green ($MIG/progress), $s is ServiceBay's."
      else echo "The box is in a known state: every step before $s is green ($MIG/progress); $s moves no service."; fi
      echo "Next: read the log above, fix the cause, then: scripts/migration-day.sh --resume${UNTIL:+ --until $UNTIL}"
      return 1
    fi
  done
  echo; echo "green${UNTIL:+ up to $UNTIL}: ${todo[*]:-nothing to do}"
}

launch() {   # the run, as a transient user unit that outlives the SSH session
  if systemctl --user is-active --quiet "$UNIT"; then
    echo "$UNIT runs already: scripts/migration-day.sh status, or journalctl --user -u $UNIT -f" >&2; return 1
  fi
  local v envs=()
  for v in PATH DOMAIN LAIB_VERSION SRC INSTALL_ARGS HOOKS MQTT_USER IMMICH_ADMIN_EMAIL SB_VERSION BACKUP_MAX_AGE_H MIN_FREE_GB MIG_DIR IGNORE RESTIC_REPOSITORY RESTIC_PASSWORD_FILE \
           Q DATA_DIR SB_DATA LAIB_BIN LAIB_STATE OUT INSTALL_BOX MIGRATE_PY CARRY MOVE TEMPLATES PYTHON CONTAINERENV MIG_USER; do
    [ -n "${!v+x}" ] && envs+=("--setenv=$v=${!v}")
  done
  systemd-run --user --unit="$UNIT" --collect --same-dir "${envs[@]}" bash "$SELF" --foreground "${ARGS[@]}" || return 1
  echo "started $UNIT. It goes on when this SSH session ends. Watch it:"
  echo "  journalctl --user -u $UNIT -f        scripts/migration-day.sh status        scripts/migration-day.sh wait"
}

wait_unit() {   # wait [seconds]: until the unit ends (capped), then where it stands
  local max=${1:-10800} t=0
  while systemctl --user is-active --quiet "$UNIT"; do
    [ "$t" -ge "$max" ] && { echo "$UNIT still runs after ${max}s"; status; return 3; }
    sleep 15; t=$((t + 15))
  done
  status
  ! grep -q " red " <<<"$(tail -1 "$MIG/progress" 2>/dev/null)"
}

DRY="" RESUME="" FG="" UNTIL="" CMD="" ARGS=()
while [ $# -gt 0 ]; do
  case $1 in
    --dry-run) DRY=1;; --resume) RESUME=1; ARGS+=("$1");; --foreground) FG=1;;
    --no-fresh-backup) export NO_FRESH_BACKUP=1; ARGS+=("$1");;
    --until) UNTIL=${2:-}; valid_step "$UNTIL" || exit 2; ARGS+=("$1" "$2"); shift;;
    status|steps|wait|rollback) CMD=$1; shift; break;;
    *) echo "unknown argument $1 (see the head of $0)" >&2; exit 2;;
  esac
  shift
done
DOMAIN=${DOMAIN:-$(sbjson domain)}
if [ -z "${LAIB_VERSION:-}" ]; then
  if [ -n "${SRC:-}" ]; then LAIB_VERSION=$(curl -fsSL "$SRC/VERSION" 2>/dev/null); else LAIB_VERSION=v$(cat "$HERE/../version.txt"); fi
fi

case "$CMD" in
  steps) echo "$STEPS"; exit 0;;
  status) status; exit $?;;
  wait) wait_unit "${1:-}"; exit $?;;
  rollback)
    s=${1:-}
    if [ "$s" = all ]; then
      back=""
      for s in $STEPS; do service_step "$s" && case "$(last_status "$s")" in green|red) back="$s $back";; esac; done
    else
      valid_step "$s" && service_step "$s" || { echo "usage: migration-day.sh rollback <service step>|all" >&2; exit 2; }
      back=$s
    fi
    systemctl --user is-active --quiet "$UNIT" && { echo "$UNIT runs: wait for it, then roll back" >&2; exit 1; }
    for s in $back; do
      say "rollback: $s back to ServiceBay"
      handback "$s" || { echo "rollback stops at $s; the ones before it are ServiceBay's again"; exit 1; }
      record "$s" rolledback
      echo "$s is ServiceBay's again. scripts/migration-day.sh --resume takes it over again (the stale copies go first)."
    done
    exit 0;;
esac
if [ -n "$DRY" ]; then dry_run; exit $?; fi
if [ -z "$FG" ]; then launch; exit $?; fi
mkdir -p "$MIG"
run 2>&1 | tee -a "$MIG/log"
exit "${PIPESTATUS[0]}"
