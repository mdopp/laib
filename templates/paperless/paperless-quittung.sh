#!/bin/sh
# The mail receipt (owner 2026-11-09), the host's part. paperless-quittung.service
# runs it every five minutes, twice:
#
#   sammeln  runs paperless-quittung.py inside the running Paperless container
#            (the script on stdin, --user paperless as the check) and appends
#            what it prints, bote-notify's payloads {"an", "text", "at"}, to
#            $QUITTUNG/meldungen.jsonl on the host. Without PAPERLESS_MAIL_HOST
#            nothing runs.
#   melden   sends each line to bote-notify on the host's loopback (sh and curl,
#            which Fedora CoreOS has), as beets' musik-meldungen.sh does.
#
# The bearer reaches curl as its config on stdin (-K -), never on argv. A person
# bote-notify does not know (404) gets the line once more to $NOTIFY_PERSON. A
# send that fails (unreachable, 429, 5xx) stays for the next run; a line older
# than a day is dropped. Each failure is one line in the journal (journalctl
# --user -u paperless-quittung), never the text. melden never fails the unit.
S=${QUITTUNG:?the folder for the messages}
F=$S/meldungen.jsonl
URL=${NOTIFY_URL:-http://127.0.0.1:8645/mitteilung}

sammeln() {
	[ -n "$PAPERLESS_MAIL_HOST" ] || exit 0
	mkdir -p "$S" || exit 1
	${PODMAN:-/usr/bin/podman} exec -i --user paperless -e PAPERLESS_MAIL_HOST -e PAPERLESS_MAIL_PORT \
		-e PAPERLESS_MAIL_SECURITY -e PAPERLESS_MAIL_USER -e PAPERLESS_MAIL_PASSWORD -e NOTIFY_PERSON \
		"${CONTAINER:-systemd-paperless}" python3 - quittung <"${QUITTUNG_SKRIPT:?paperless-quittung.py}" >"$S/neu.jsonl"
	rc=$?
	cat "$S/neu.jsonl" >>"$F" && rm -f "$S/neu.jsonl"
	exit $rc
}

sende() { # the payload in $1; prints bote-notify's HTTP code, 000 when unreachable
	printf '%s' "$1" >"$W.zeile"
	printf 'header = "Authorization: Bearer %s"\n' "$NOTIFY_TOKEN" |
		curl -s -m 60 -K - -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' \
			--data-binary @"$W.zeile" "$URL" 2>/dev/null
}

melden() {
	[ -s "$F" ] || exit 0
	if [ -z "$NOTIFY_TOKEN" ]; then # no Bote: none goes out, and none piles up
		rm -f "$F"
		exit 0
	fi
	W="$F.senden"
	mv "$F" "$W" || exit 0
	: >"$W.rest"
	jetzt=$(date +%s)
	weg=""
	while IFS= read -r z || [ -n "$z" ]; do
		[ -n "$z" ] || continue
		an=$(printf '%s\n' "$z" | sed -n 's/^{"an": "\([^"]*\)".*/\1/p')
		at=$(printf '%s\n' "$z" | sed -n 's/.*"at": *\([0-9][0-9]*\)}$/\1/p')
		if [ -z "$at" ] || [ $((jetzt - at)) -gt 86400 ]; then
			echo "Mail-Quittung: Mitteilung an ${an:-?} verworfen (älter als ein Tag oder unlesbar)" >&2
			continue
		fi
		if [ -n "$weg" ]; then
			printf '%s\n' "$z" >>"$W.rest"
			continue
		fi
		code=$(sende "$z")
		if [ "$code" = 404 ] && [ -n "$NOTIFY_PERSON" ] && [ "$an" != "$NOTIFY_PERSON" ]; then
			echo "Mail-Quittung: bote-notify kennt ${an:-?} nicht, die Mitteilung geht an $NOTIFY_PERSON" >&2
			an=$NOTIFY_PERSON
			code=$(sende "$(printf '%s\n' "$z" | sed "s|^{\"an\": \"[^\"]*\"|{\"an\": \"$NOTIFY_PERSON\"|")")
		fi
		case "$code" in
		2??) ;;
		000 | "")
			weg=1
			echo "Mail-Quittung: bote-notify nicht erreichbar ($URL), Mitteilungen bleiben für den nächsten Lauf" >&2
			printf '%s\n' "$z" >>"$W.rest"
			;;
		429 | 5??)
			echo "Mail-Quittung: Mitteilung an ${an:-?} nicht zugestellt (HTTP $code), der nächste Lauf versucht es wieder" >&2
			printf '%s\n' "$z" >>"$W.rest"
			;;
		*) echo "Mail-Quittung: Mitteilung an ${an:-?} nicht zugestellt (HTTP $code), verworfen" >&2 ;;
		esac
	done <"$W"
	if [ -s "$W.rest" ]; then # back for the next run, before anything sammeln wrote since
		cat "$W.rest" "$F" >"$W.neu" 2>/dev/null
		mv "$W.neu" "$F"
	fi
	rm -f "$W" "$W.rest" "$W.zeile" "$W.neu"
	exit 0
}

case "$1" in
sammeln) sammeln ;;
melden) melden ;;
*)
	echo "usage: paperless-quittung.sh sammeln|melden" >&2
	exit 2
	;;
esac
