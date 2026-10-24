#!/bin/sh
# The Musik-Eingang's messages (#639), sent from the host: beets-eingang.service's
# ExecStartPost, after musik-eingang.py ran in the container. beets is not on the
# host network and bote-notify listens on the host's loopback only, so the
# importer writes each message, bote-notify's payload {"an", "text"} plus "at"
# (epoch), as a JSON line into $MELDUNGEN, and this sends them: sh and curl,
# which Fedora CoreOS has (no python on the host).
#
# The bearer reaches curl as its config on stdin (-K -, printf is the shell's
# own), never on argv. A person bote-notify does not know (404) gets the line
# once more to $NOTIFY_PERSON. A send that fails (unreachable, 429, 5xx) stays
# for the next run; an unreachable bote-notify is asked once per run; a line
# older than a day is dropped. Every failure is one line in the journal
# (journalctl --user -u beets-eingang), never the text. Never fails the unit.
F=${MELDUNGEN:?the file musik-eingang.py writes}
URL=${NOTIFY_URL:-http://127.0.0.1:8645/mitteilung}
[ -s "$F" ] || exit 0
if [ -z "$NOTIFY_TOKEN" ]; then   # no Bote: none goes out, and none piles up
	rm -f "$F"
	exit 0
fi
W="$F.senden"
mv "$F" "$W" || exit 0
: >"$W.rest"
jetzt=$(date +%s)
weg=""

sende() { # the payload in $1; prints bote-notify's HTTP code, 000 when unreachable
	printf '%s' "$1" >"$W.zeile"
	printf 'header = "Authorization: Bearer %s"\n' "$NOTIFY_TOKEN" |
		curl -s -m 60 -K - -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' \
			--data-binary @"$W.zeile" "$URL" 2>/dev/null
}

while IFS= read -r z || [ -n "$z" ]; do
	[ -n "$z" ] || continue
	an=$(printf '%s\n' "$z" | sed -n 's/^{"an": "\([^"]*\)".*/\1/p')
	at=$(printf '%s\n' "$z" | sed -n 's/.*"at": *\([0-9][0-9]*\)}$/\1/p')
	if [ -z "$at" ] || [ $((jetzt - at)) -gt 86400 ]; then
		echo "Musik-Eingang: Mitteilung an ${an:-?} verworfen (älter als ein Tag oder unlesbar)" >&2
		continue
	fi
	if [ -n "$weg" ]; then
		printf '%s\n' "$z" >>"$W.rest"
		continue
	fi
	code=$(sende "$z")
	if [ "$code" = 404 ] && [ -n "$NOTIFY_PERSON" ] && [ "$an" != "$NOTIFY_PERSON" ]; then
		echo "Musik-Eingang: bote-notify kennt ${an:-?} nicht, die Mitteilung geht an $NOTIFY_PERSON" >&2
		an=$NOTIFY_PERSON
		code=$(sende "$(printf '%s\n' "$z" | sed "s|^{\"an\": \"[^\"]*\"|{\"an\": \"$NOTIFY_PERSON\"|")")
	fi
	case "$code" in
	2??) ;;
	000 | "")
		weg=1
		echo "Musik-Eingang: bote-notify nicht erreichbar ($URL), Mitteilungen bleiben für den nächsten Lauf" >&2
		printf '%s\n' "$z" >>"$W.rest"
		;;
	429 | 5??)
		echo "Musik-Eingang: Mitteilung an ${an:-?} nicht zugestellt (HTTP $code), der nächste Lauf versucht es wieder" >&2
		printf '%s\n' "$z" >>"$W.rest"
		;;
	*) echo "Musik-Eingang: Mitteilung an ${an:-?} nicht zugestellt (HTTP $code), verworfen" >&2 ;;
	esac
done <"$W"

if [ -s "$W.rest" ]; then   # back for the next run, before anything the importer wrote since
	cat "$W.rest" "$F" >"$W.neu" 2>/dev/null
	mv "$W.neu" "$F"
fi
rm -f "$W" "$W.rest" "$W.zeile" "$W.neu"
exit 0
