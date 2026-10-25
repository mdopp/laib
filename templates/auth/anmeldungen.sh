#!/bin/sh
# Failed logins for the owner (#653), the host's part. auth-anmeldungen.service
# runs it every five minutes, twice:
#
#   zaehlen  runs anmeldungen.py in a stock Python image (the script on stdin),
#            with no network, Authelia's data and LLDAP's read-only, its own
#            folder $AUTH_DATA/anmeldungen writable. The LAN is what the host's
#            own interfaces are on (ip addr), so a request from there counts
#            as the Heimnetz.
#   melden   sends what it wrote, from the host (sh and curl, which Fedora
#            CoreOS has): each line of meldungen.jsonl to bote-notify on the
#            host's loopback, and each line of notizen.tsv as a journal note
#            through laib note, as actor laib and at most the propose tier.
#
# The bearer reaches curl as its config on stdin (-K -), never on argv. A send
# that fails (unreachable, 429, 5xx) or a note laib refuses stays for the next
# run; a message older than a day is dropped. Without the Bote no message
# piles up, the notes still go. melden never fails the unit.
D=${AUTH_DATA:?the auth data folder}
S=$D/anmeldungen

zaehlen() {
	mkdir -p "$S"
	netze=$(ip -o addr show 2>/dev/null | awk '{print $4}' | paste -sd, -)
	exec ${PODMAN:-/usr/bin/podman} run --rm -i --name auth-anmeldungen --network none --read-only \
		--cap-drop all --security-opt no-new-privileges \
		-e TZ -e AUTH_MAX_RETRIES -e AUTH_FIND_TIME -e AUTH_BAN_TIME -e NOTIFY_PERSON -e LAN_NETZE="$netze" \
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

meldungen() {
	F=$S/meldungen.jsonl
	[ -s "$F" ] || return 0
	if [ -z "$NOTIFY_TOKEN" ]; then
		rm -f "$F"
		return 0
	fi
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
		if [ -n "$weg" ]; then
			printf '%s\n' "$z" >>"$W.rest"
			continue
		fi
		code=$(sende "$z")
		case "$code" in
		2??) ;;
		000 | "")
			weg=1
			echo "Anmeldungen: bote-notify nicht erreichbar, die Mitteilung bleibt für den nächsten Lauf" >&2
			printf '%s\n' "$z" >>"$W.rest"
			;;
		429 | 5??)
			echo "Anmeldungen: Mitteilung nicht zugestellt (HTTP $code), der nächste Lauf versucht es wieder" >&2
			printf '%s\n' "$z" >>"$W.rest"
			;;
		*) echo "Anmeldungen: Mitteilung nicht zugestellt (HTTP $code), verworfen" >&2 ;;
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
