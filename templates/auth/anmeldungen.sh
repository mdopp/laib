#!/bin/sh
# Failed logins for the owner (#653), the host's part. auth-anmeldungen.service
# runs it every five minutes, twice:
#
#   zaehlen  runs anmeldungen.py in a stock Python image (the script on stdin),
#            with no network, Authelia's data and LLDAP's read-only, its own
#            folder $AUTH_DATA/anmeldungen writable. The LAN is what the host's
#            own interfaces are on (ip addr), so a request from there counts
#            as the Heimnetz. Before it, each other login door's unit journal
#            after its cursor goes into journal/<door>.json (#703), which the
#            counter reads and moves the cursor of.
#   melden   sends what it wrote, from the host (sh and curl, which Fedora
#            CoreOS has): each line of meldungen.jsonl to bote-notify on the
#            host's loopback, and each line of notizen.tsv as a journal note
#            through laib note, as actor laib and at most the propose tier.
#
# The bearer reaches curl as its config on stdin (-K -), never on argv. A send
# that fails (unreachable, 429, 5xx) or a note laib refuses stays for the next
# run; a message older than a day is dropped. Without the Bote no message
# piles up, the notes still go. melden never fails the unit.
#
# The mail fallback (#703): a message the Bote has not taken for 15 minutes
# (unreachable, 429, 5xx), one it refused (a person it does not know), or
# every one when there is no Bote, goes to laib-notify's folder eingang/ as
# one file, when the box has a mail server (SMTP_HOST, Authelia's): laib-notify
# tries the Bote once more and else mails it, once (notify.go, #441/#659).
# Without a mail server it waits for the Bote, a day at most, as before.
D=${AUTH_DATA:?the auth data folder}
S=$D/anmeldungen
EINGANG=${NOTIFY_EINGANG:-${LAIB_HOME:-/mnt/data/laib}/notify/eingang}
NACH=900 # seconds without the Bote before the mail

# the other doors (#703): <door>:<unit>, the unit's journal is where it logs a failed login
TUEREN="vaultwarden:vaultwarden.service samba:file-share-samba.service home-assistant:home-assistant.service"

exportieren() { # each door's journal after its cursor; the first time the last hour. Never fails zaehlen
	mkdir -p "$S/journal"
	for t in $TUEREN; do
		c=$(cat "$S/journal/${t%%:*}.cursor" 2>/dev/null)
		if [ -n "$c" ]; then
			set -- --after-cursor "$c"
		else
			set -- --since -1h
		fi
		${JOURNALCTL:-journalctl} --user -u "${t#*:}" -o json --output-fields=MESSAGE --no-pager -q "$@" \
			>"$S/journal/${t%%:*}.json" 2>/dev/null || : >"$S/journal/${t%%:*}.json"
	done
}

zaehlen() {
	mkdir -p "$S"
	exportieren
	netze=$(ip -o addr show 2>/dev/null | awk '{print $4}' | paste -sd, -)
	exec ${PODMAN:-/usr/bin/podman} run --rm -i --name auth-anmeldungen --network none --read-only \
		--cap-drop all --security-opt no-new-privileges \
		-e TZ -e AUTH_MAX_RETRIES -e AUTH_FIND_TIME -e AUTH_BAN_TIME -e NOTIFY_PERSON -e SMTP_HOST -e LAN_NETZE="$netze" \
		-e STATE_DIR=/state -e AUTH_DB=/authelia/db.sqlite3 -e LLDAP_DB=/lldap/users.db \
		-v "$D/authelia-data:/authelia:ro,z" -v "$D/lldap:/lldap:ro,z" -v "$S:/state:z" \
		"${ANMELDUNGEN_IMAGE:?the image}" python3 - <"${ANMELDUNGEN_SKRIPT:?anmeldungen.py}"
}

sende() { # the payload in $1; prints bote-notify's HTTP code, 000 when unreachable
	printf '%s' "$1" >"$S/zeile.json"
	printf 'header = "Authorization: Bearer %s"\n' "$NOTIFY_TOKEN" |
		curl -s -m 60 -K - -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' \
			--data-binary @"$S/zeile.json" "${NOTIFY_URL:-http://127.0.0.1:8645/mitteilung}" 2>/dev/null
}

abgeben() { # the line in $1, its time in $2: to laib-notify for the mail; 1 when it stays here
	[ -n "$SMTP_HOST" ] || return 1
	n=$((n + 1))
	f=$EINGANG/anmeldungen-$2-$$-$n.json
	mkdir -p "$EINGANG" && printf '%s\n' "$1" >"$f.neu" && mv "$f.neu" "$f" || {
		rm -f "$f.neu"
		return 1
	}
	echo "Anmeldungen: der Bote hat die Mitteilung nicht genommen, laib-notify schickt sie per Mail" >&2
}

meldungen() {
	F=$S/meldungen.jsonl
	[ -s "$F" ] || return 0
	if [ -z "$NOTIFY_TOKEN" ] && [ -z "$SMTP_HOST" ]; then
		rm -f "$F"
		return 0
	fi
	n=0
	W=$F.senden
	mv "$F" "$W" || return 0
	: >"$W.rest"
	jetzt=$(date +%s)
	weg=""
	while IFS= read -r z || [ -n "$z" ]; do
		[ -n "$z" ] || continue
		at=$(printf '%s\n' "$z" | sed -n 's/.*"at": *\([0-9][0-9]*\)}$/\1/p')
		if [ -z "$at" ] || [ $((jetzt - at)) -gt 86400 ]; then
			echo "Anmeldungen: Mitteilung verworfen (älter als ein Tag oder unlesbar)" >&2
			continue
		fi
		bote=$NOTIFY_TOKEN
		case "$z" in *'"an": ""'*) bote="" ;; esac # nobody the Bote could reach (box.yml notify.person)
		if [ -z "$bote" ]; then # no Bote: the mail at once
			abgeben "$z" "$at" || printf '%s\n' "$z" >>"$W.rest"
			continue
		fi
		if [ -n "$weg" ]; then
			code=000
		else
			code=$(sende "$z")
		fi
		code=${code:-000}
		case "$code" in
		2??) ;;
		000 | 429 | 5??)
			if [ "$code" = 000 ] && [ -z "$weg" ]; then
				weg=1
				echo "Anmeldungen: bote-notify nicht erreichbar" >&2
			fi
			if [ $((jetzt - at)) -lt $NACH ] || ! abgeben "$z" "$at"; then
				echo "Anmeldungen: Mitteilung nicht zugestellt (HTTP $code), der nächste Lauf versucht es wieder" >&2
				printf '%s\n' "$z" >>"$W.rest"
			fi
			;;
		*) abgeben "$z" "$at" || echo "Anmeldungen: Mitteilung nicht zugestellt (HTTP $code), verworfen" >&2 ;;
		esac
	done <"$W"
	if [ -s "$W.rest" ]; then # back, before anything the counter wrote since
		cat "$W.rest" "$F" >"$W.neu" 2>/dev/null
		mv "$W.neu" "$F"
	fi
	rm -f "$W" "$W.rest" "$W.neu" "$S/zeile.json"
}

notizen() {
	F=$S/notizen.tsv
	[ -s "$F" ] || return 0
	W=$F.senden
	mv "$F" "$W" || return 0
	tab=$(printf '\t')
	while IFS="$tab" read -r kind text || [ -n "$kind" ]; do
		[ -n "$text" ] || continue
		LAIB_HOME=${LAIB_HOME:-/mnt/data/laib} LAIB_ACTOR=laib LAIB_CEILING=propose \
			"${LAIB_BIN:-/var/opt/laib/laib}" note --kind "$kind" "$text" >/dev/null ||
			printf '%s\t%s\n' "$kind" "$text" >>"$F"
	done <"$W"
	rm -f "$W"
}

case "$1" in
zaehlen) zaehlen ;;
melden)
	meldungen
	notizen
	exit 0
	;;
*)
	echo "usage: anmeldungen.sh zaehlen|melden" >&2
	exit 2
	;;
esac
