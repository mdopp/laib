#!/usr/bin/env bash
# Generalprobe (#248): docs/MIGRATION.md, step by step, on the ServiceBay box,
# as core. Every command a human would type is here, in MIGRATION.md's order
# and words; where this script has to know something MIGRATION.md does not
# say, MIGRATION.md is the thing to fix, not this script.
#
# Runs from a checkout of this commit at ~/laib (MIGRATION.md step 1: the
# migration scripts come from the repo, the box has no other copy).
#   SRC     the build to install (install-box.sh --source; a release on the box)
#   DOMAIN  the box's domain;  HOOKS  the hooks image (this commit's, in CI)
#   STEP    one of: install, auth, services, rollback, caddy, retire (default: all in order)
set -euo pipefail
: "${SRC:?}" "${DOMAIN:?}"
cd ~/laib
export PATH=/var/opt/laib:$PATH LAIB_HOME=/mnt/data/laib LAIB_ACTOR=human:core LAIB=/var/opt/laib/laib
Q=~/.config/containers/systemd
OUT=/mnt/data/laib/migration
say() { printf '\n=== %s\n' "$*"; }
# a verb that meets the apply lock (the converge timer, #211) tries again
laib() { local i rc; for i in $(seq 1 60); do rc=0; /var/opt/laib/laib "$@" || rc=$?; [ "$rc" = 4 ] || return "$rc"; echo "laib $1: the lock is busy, trying again" >&2; sleep 3; done; return 4; }

# MIGRATION.md step 4a: ServiceBay's unit stopped, its files out of the Quadlet root
servicebay_out() {
  local n=$1 f moved=""
  systemctl --user stop "$n.service"
  mkdir -p ~/servicebay-units
  for f in "$Q/$n.kube" "$Q/$n.yml" "$Q/$n.container"; do
    [ -e "$f" ] && { mv "$f" ~/servicebay-units/; moved="$moved $(basename "$f")"; }
  done
  systemctl --user daemon-reload
  [ -n "$moved" ] || { echo "$n: no ServiceBay file in $Q"; return 1; }
  echo "$n: ServiceBay's unit stopped,$moved moved to ~/servicebay-units"
}

# MIGRATION.md step 4c: the `laib add` line migrate-servicebay.py printed for it
add_line() { sed -n "s/^  \(.*laib add $1\( .*\)\?\)$/\1/p" "$OUT.txt"; }
laib_add() {
  local n=$1 line; shift
  line=$(add_line "$n")
  [ -n "$line" ] || { echo "migrate-servicebay.py printed no laib add line for $n"; return 1; }
  echo "\$ $line $*"
  laib plan "$n" > /tmp/plan-$n.txt 2>&1 || true
  if grep -q KONFLIKT /tmp/plan-$n.txt; then echo "laib plan $n still reports KONFLIKT:"; cat /tmp/plan-$n.txt; return 1; fi
  local rc=0
  eval "$line $*" || rc=$?
  laib status "$n"
  if [ "$rc" != 0 ]; then   # what the apply's red verify cannot say: the containers' own words
    for c in $(podman ps -a --format '{{.Names}}' | grep "^systemd-$n" || true) $(journalctl --user --no-pager -o export --since -15min 2>/dev/null | sed -n "s/^CONTAINER_NAME=\(systemd-$n.*\)/\1/p" | sort -u); do
      echo "--- $c"; journalctl --user --no-pager -o cat CONTAINER_NAME="$c" | tail -30
    done
    return "$rc"
  fi
}

move() { HOOKS_IMAGE="$HOOKS" scripts/move-servicebay-data.sh "$@"; }

step_install() {
  say "1. Laib beside ServiceBay: the binary, the state repo, the drafts, the secrets"
  curl -fsSL "$SRC/install-box.sh" -o /tmp/install-box.sh
  bash /tmp/install-box.sh --source "$SRC" --domain "$DOMAIN" --tls internal
  if [ -n "${HOOKS:-}" ]; then   # this commit's hooks image (CI only; the box takes :latest)
    b=/mnt/data/laib/state/box.yml
    if grep -q "^laib:" $b; then sed -i "s|^laib:|laib:\n    hooksImage: $HOOKS|" $b; else printf "laib:\n    hooksImage: %s\n" "$HOOKS" >> $b; fi
    grep -n hooksImage $b
  fi
  laib plan > /tmp/plan-1.txt 2>&1 || true
  head -40 /tmp/plan-1.txt
  # ServiceBay's templates as the box runs them: the built-ins in its image,
  # the external registries it cloned
  rm -rf ~/servicebay-templates; mkdir -p ~/servicebay-templates
  podman cp servicebay:/app/templates ~/servicebay-templates/servicebay
  podman cp servicebay:/app/.servicebay/registries ~/servicebay-templates/registries
  python3 scripts/migrate-servicebay.py --config /mnt/data/servicebay/config.json \
    --templates ~/servicebay-templates/servicebay,$(ls -d ~/servicebay-templates/registries/*/templates | paste -sd,) \
    --out "$OUT" | tee "$OUT.txt"
  # carry over before the first apply, while ServiceBay still runs: the line it printed
  carry=$(sed -n 's/^ServiceBay still runs: \(scripts\/carry-servicebay-secrets.sh .*\)$/\1/p' "$OUT.txt")
  [ -n "$carry" ] || { echo "migrate-servicebay.py printed no carry line"; exit 1; }
  echo "\$ $carry"; $carry
  laib secret list
}

step_auth() {
  say "3. auth (Authelia + LLDAP), by the a–c of step 4"
  servicebay_out auth
  laib_add auth
}

step_services() {
  say "4. the household services, in MIGRATION.md's order"
  local n
  for n in ${SERVICES:-vaultwarden radicale mosquitto file-share immich paperless home-assistant}; do
    say "4. $n"
    servicebay_out "$n"
    case "$n" in
      vaultwarden|home-assistant|file-share|radicale) move "$n";;
      immich)   # "Immich's external libraries": both Syncthings stopped (ServiceBay's is out with file-share)
        systemctl --user stop file-share-syncthing.service
        move --dry-run immich; move immich
        systemctl --user start file-share-syncthing.service;;
    esac
    # "One value goes with them": the box's broker user (MIGRATION.md, secrets that carry over)
    if [ "$n" = mosquitto ]; then laib_add "$n" MQTT_USER=mqtt; else laib_add "$n"; fi
  done
}

# MIGRATION.md "Rollback during the weekend", mid-migration: hand a service back
# to ServiceBay, then take it over again
step_rollback() {
  local n=${ROLLBACK:-home-assistant}
  say "rollback: $n back to ServiceBay"
  laib remove "$n"
  if [ "$n" = home-assistant ]; then
    for f in /mnt/data/stacks/home-assistant/zwave-js/settings.json /mnt/data/stacks/home-assistant/homeassistant/.storage/core.config_entries; do
      podman unshare sh -c "test -e '$f.servicebay' && mv '$f.servicebay' '$f'" && echo "put back $f"
    done
  fi
  mv ~/servicebay-units/"$n".* "$Q/"
  systemctl --user daemon-reload
  systemctl --user start "$n.service"
  systemctl --user is-active "$n.service"
}
step_forward_again() {
  local n=${ROLLBACK:-home-assistant}
  say "rollback over: $n to Laib again"
  servicebay_out "$n"
  case "$n" in vaultwarden|home-assistant|file-share) move "$n";; esac
  laib_add "$n"
}

step_caddy() {
  say "4d. Caddy replaces NPM"
  # until now NPM has served the household's names, Laib's units behind it
  local h
  for h in vault:/alive photos:/api/server/ping home:/ caldav:/; do
    echo "NPM: http://${h%%:*}.$DOMAIN${h#*:} -> HTTP $(curl -s -o /dev/null -w '%{http_code}' -H "Host: ${h%%:*}.$DOMAIN" "http://127.0.0.1${h#*:}" || true)"
  done
  servicebay_out nginx
  laib add caddy
}

step_retire() {
  say "7. ServiceBay off"
  servicebay_out servicebay
  laib status
}

case "${STEP:-all}" in
  install) step_install;; auth) step_auth;; services) step_services;; rollback) step_rollback;;
  forward) step_forward_again;; caddy) step_caddy;; retire) step_retire;;
  all) step_install; step_auth; step_services; step_rollback; step_forward_again; step_caddy; step_retire;;
  *) echo "unknown STEP $STEP"; exit 2;;
esac
