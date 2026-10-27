#!/bin/sh
# Contract and invoice data, notice reminders (#709, #721, #818), the host's part.
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
# never on argv. The unit's exit code is the script's. A run marks its start in
# VERTRAEGE_STAND and ends by refreshing the status (paperless-check), so laib
# status shows it at once (#829).
#
# "neu" (#829), from paperless-vertraege-neu.timer every two minutes: Paperless
# writes a thumbnail for each document it files, so its thumbnails folder
# changes with every new document, without a script or a mount in the
# container. Changed after the last run's start and quiet for RUHE seconds:
# start the run (paperless-vertraege.service, which systemd never runs twice
# at once). A stack of 13 documents is one run; one filed during a run, one
# more. Nothing to see (no folder yet): nothing, the hourly timer stays.
SYSTEMCTL=${SYSTEMCTL:-/usr/bin/systemctl}
STAND=${VERTRAEGE_STAND:-${XDG_STATE_HOME:-$HOME/.local/state}/laib/paperless-vertraege}
if [ "${1:-}" = neu ]; then
	neu=$(stat -c %Y "${VORSCHAU:?the thumbnails folder}" 2>/dev/null) || exit 0
	alt=$(stat -c %Y "$STAND" 2>/dev/null) || alt=0
	[ "$neu" -gt "$alt" ] && [ $(($(date +%s) - neu)) -ge "${RUHE:-120}" ] || exit 0
	exec "$SYSTEMCTL" --user start --no-block paperless-vertraege.service
fi
mkdir -p "${STAND%/*}" && touch "$STAND"
EINGANG=${NOTIFY_EINGANG:-${LAIB_HOME:-/mnt/data/laib}/notify/eingang}
RC=$(mktemp) || exit 1
n=0
{
	${PODMAN:-/usr/bin/podman} exec -i --user paperless -e VERTRAG_ARTEN -e VERTRAG_VORLAUF_TAGE -e RECHNUNG_ARTEN -e HAUSHALT_URL -e HAUSHALT_TOKEN -e HAUSHALT_NAMEN \
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
"$SYSTEMCTL" --user start --no-block paperless-check.service || echo "vertraege: Stand nicht aufgefrischt" >&2
exit "${rc:-1}"
