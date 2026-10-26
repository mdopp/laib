#!/usr/bin/env bash
# Rotates the secrets that still have the value Laib carried over from
# ServiceBay (#665): the ones `laib diag` names under the probe servicebay,
# whose old value sits readable in ServiceBay's data dir until it goes.
# Throw-away like the carry-over itself (ADR 0010): it goes with that dir.
#
# The mechanics are laib's: `laib secret rotate` sets the new value, inside
# the app first where the template's hook can (rotate: true in laib.yml),
# applies, verifies, and on a red apply rotates back, once. This script only
# orders, gates and stops: one service at a time, green before, green after,
# stop at the first red. It prints no value, ever; laib does not either.
#
# Runs on the host as core, at the console: rotating is a human's (laib
# refuses any other hand). Steps with the owner's devices or a provider
# are not rotated here; --wie prints what the owner does for them.
#
# Usage: scripts/rotate-carried.sh                 the plan: what rotates, in which order, what is the owner's
#        scripts/rotate-carried.sh --los [svc …]   rotate (all, or these services), stop at the first red
#        scripts/rotate-carried.sh --wie NAME      the owner's steps for one secret, in German
#   LAIB (laib) overrides the binary.
set -uo pipefail
LAIB=${LAIB:-laib}
# the household's order: what the family misses least first, the login (auth) last
ORDER="paperless immich media adguard home-assistant auth"

wie() { # the owner's steps for one secret: one German sentence each
  case "$1" in
    MQTT_PASSWORD) cat <<'T'
Das MQTT-Passwort steht in jedem Gerät, das sich bei Mosquitto anmeldet (Steckdosen, Sensoren, Zigbee2MQTT) und in Home Assistants MQTT-Eintrag.
1. Auf der Box: laib secret rotate MQTT_PASSWORD (Mosquitto nimmt das neue sofort, die Geräte sind bis Schritt 3 getrennt).
2. Das neue Passwort steht im Wiederherstellungsblatt: laib recovery-sheet.
3. In Home Assistant unter Einstellungen → Geräte & Dienste → MQTT → Neu konfigurieren eintragen, dann in jedem Gerät.
T
    ;;
    SAMBA_PASSWORD) cat <<'T'
Das Passwort des gemeinsamen Netzlaufwerk-Kontos steht auf jedem Gerät, das das Laufwerk damit verbunden hat (Scanner, alte Laptops).
1. Auf der Box: laib secret rotate SAMBA_PASSWORD.
2. Das neue Passwort steht im Wiederherstellungsblatt: laib recovery-sheet.
3. Auf jedem dieser Geräte das Netzlaufwerk mit dem neuen Passwort neu verbinden; wer sein eigenes Familienkonto nutzt, merkt nichts.
T
    ;;
    AUTHELIA_STORAGE_KEY) cat <<'T'
Nicht nötig, und hier bewusst nicht gebaut: der Schlüssel verschlüsselt nur Authelias eigene Datenbank auf derselben Platte; wer die alte Kopie des Schlüssels hat, hat auch die alte Datenbank, das Erneuern schützt also nichts.
Wenn du es trotzdem willst: Authelia hat dafür einen eigenen Befehl (authelia storage encryption change-key), der den neuen Schlüssel auf der Befehlszeile verlangt, wo ihn jeder Benutzer der Box sieht; sag Claude Bescheid, dann bauen wir einen Weg über eine Datei.
T
    ;;
    *) echo "In der App (oder beim Anbieter) ein neues Passwort setzen; dann auf der Box: laib secret set $1 (der Wert auf stdin, nicht in der Befehlszeile); dann laib apply <Dienst>." ;;
  esac
}

# laib diag's servicebay findings as "service name fix": the detail starts with the name
carried() {
  "$LAIB" diag | awk '$2 == "servicebay" && /hat noch den Wert aus ServiceBay/ {
    if ($4 == "hat") { svc = "-"; name = $3 } else { svc = $3; name = $4 }
    fix = $0; sub(/.*→ /, "", fix); print svc, name, fix }'
}

# rotatable: laib secret rotate can (the fix names this script) and no device holds it
kind() { # kind <name> <fix…>
  local name=$1; shift
  case "$name" in MQTT_PASSWORD | SAMBA_PASSWORD | AUTHELIA_STORAGE_KEY) echo owner; return ;; esac
  case "$*" in *rotate-carried.sh*) echo rotate ;; *) echo owner ;; esac
}

state() { "$LAIB" status "$1" --fresh 2>/dev/null | awk -v s="$1" '$2 == s { print $1; exit }'; }

ordered() { # the carried lines, in ORDER, then the rest
  local all; all=$(carried)
  for s in $ORDER; do printf '%s\n' "$all" | awk -v s="$s" '$1 == s'; done
  printf '%s\n' "$all" | awk -v o=" $ORDER " 'NF && index(o, " " $1 " ") == 0'
}

case "${1:-}" in
  --wie) [ -n "${2:-}" ] || { echo "usage: $0 --wie NAME" >&2; exit 2; }; wie "$2"; exit 0 ;;
  --los) shift; want=" $* " go=1 ;;
  "") want="  " go="" ;;
  *) echo "usage: $0 [--los [service …] | --wie NAME]" >&2; exit 2 ;;
esac

lines=$(ordered)
if [ -z "$lines" ]; then
  echo "Kein Passwort hat mehr seinen Wert aus ServiceBay (laib diag). Weiter: laib repair servicebay."
  exit 0
fi
owner=""
while read -r svc name fix; do
  [ -n "$name" ] || continue
  if [ "$(kind "$name" "$fix")" = owner ]; then owner="$owner $name"; continue; fi
  [ "$want" = "  " ] || [[ "$want" == *" $svc "* ]] || continue
  if [ -z "$go" ]; then echo "rotiert: $svc $name"; continue; fi
  before=$(state "$svc")
  if [ "$before" != grün ]; then
    echo "STOPP vor $name: $svc ist ${before:-nicht da}, nicht grün; erst laib diag, laib logs $svc" >&2; exit 1
  fi
  echo "== $svc: $name"
  if ! "$LAIB" secret rotate "$name"; then
    echo "STOPP: laib secret rotate $name ist rot; was laib getan hat (behalten oder einmal zurückgedreht), steht darüber. laib logs $svc, laib journal --verb secret" >&2
    exit 1
  fi
  after=$(state "$svc")
  if [ "$after" != grün ]; then
    # no automatic way back here: the app holds the new value now, an old one in secrets.age would lock it out
    echo "STOPP nach $name: $svc ist $after; das neue Passwort gilt in der App und in Laib. laib logs $svc; zurück nur mit laib secret rotate $name, nie mit laib rollback" >&2
    exit 1
  fi
  echo "grün: $svc nach $name"
done <<<"$lines"
if [ -n "$owner" ]; then
  echo
  echo "Für dich (Besitzer), nicht hier rotiert:"
  for n in $owner; do echo "- $n: $(wie "$n" | head -1)"; done
  echo "Die Schritte: $0 --wie <NAME>"
fi
[ -z "$go" ] && echo && echo "Ausführen: $0 --los [Dienst …]"
exit 0
