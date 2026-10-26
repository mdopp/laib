#!/bin/sh
# Contract data and notice reminders (#709, #721), the host's part.
# paperless-vertraege.service runs it hourly: it feeds paperless-vertraege.py on
# stdin into the running Paperless container, as the paperless user (never
# root), and passes the script's lines on to the unit's journal. A line
# "meldung {json}" is a message for the owner on a reminder day: it goes to
# laib-notify's folder eingang/ (#703) as one file, {"an", "betreff", "text",
# "at"}, which laib-notify tells through the Bote first, else by mail, once,
# and drops after a day. The container cannot write there, and needs no token
# for it: the file is the whole hand-over (ADR 0027: the host does only the
# podman exec and the file).
#
# Haus's token reaches podman by name from the unit's environment (-e NAME),
# never on argv. The unit's exit code is the script's.
EINGANG=${NOTIFY_EINGANG:-${LAIB_HOME:-/mnt/data/laib}/notify/eingang}
RC=$(mktemp) || exit 1
n=0
{
	${PODMAN:-/usr/bin/podman} exec -i --user paperless -e VERTRAG_ARTEN -e VERTRAG_VORLAUF_TAGE -e HAUSHALT_URL -e HAUSHALT_TOKEN -e HAUSHALT_NAMEN \
		-e PYTHONUNBUFFERED=1 systemd-paperless python3 - <"${VERTRAEGE_SKRIPT:?paperless-vertraege.py}"
	echo $? >"$RC"
} | while IFS= read -r z || [ -n "$z" ]; do
	case "$z" in
	"meldung {"*)
		n=$((n + 1))
		f=$EINGANG/paperless-$(date +%s)-$$-$n.json
		if mkdir -p "$EINGANG" && printf '%s\n' "${z#meldung }" >"$f.neu" && mv "$f.neu" "$f"; then
			echo "vertraege: Mitteilung an laib-notify übergeben"
		else
			rm -f "$f.neu"
			echo "vertraege: Mitteilung nicht übergeben ($EINGANG)" >&2
		fi
		;;
	*) printf '%s\n' "$z" ;;
	esac
done
rc=$(cat "$RC")
rm -f "$RC"
exit "${rc:-1}"
