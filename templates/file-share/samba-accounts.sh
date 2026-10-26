#!/bin/sh
# file-share's Samba accounts (#296): a runit service beside smbd in the samba
# container (the unit's Exec installs it). One account per name in
# $DIR/members, the LLDAP users in family or admins (samba-members.py writes
# it); a name that leaves the list loses its account. The shared account
# ($SHARE_USER) is never touched: the fallback, as ServiceBay kept its own.
#
# The password is the person's own. Haus writes "set <name> <base64>" (or an
# admin's "clear <name>") into the FIFO $DIR/passwords; it is read here and
# handed to smbpasswd on stdin: on no argv (#214), in no log, in no file.
# Until a person sets one they have no account. $DIR/status is what Haus
# shows: "<name> <when the password was set, epoch; 0: none>" per member.
#
# The Werkstatt share (#666) is the admins' only: valid users = @admins. The
# POSIX group admins holds the accounts named in $DIR/admins (samba-members.py
# writes it, the LLDAP admins), never the shared account; without the list,
# nobody. Like /etc/passwd, /etc/group is new on every start: made again here.
#
# The passdb (smbpasswd, ServiceBay's on a migrated box) lives in
# samba-private and survives; the POSIX account smbd maps a login to does not,
# the container's /etc/passwd is new on every start: made again here, from the
# passdb file itself, since pdbedit stops listing at the first entry without
# one ("smbpasswd database is corrupt!").
D=${ACCOUNTS_DIR:-/samba-accounts}
PASSDB=${PASSDB:-/var/lib/samba/private/smbpasswd}
TICK=${TICK:-30}

name() { case $1 in "" | [!a-z0-9]* | *[!a-z0-9._-]*) return 1 ;; esac; [ ${#1} -le 32 ]; }
member() { [ -f "$D/members" ] && grep -qxF -- "$1" "$D/members"; }
accounts() { [ -f "$PASSDB" ] && cut -d: -f1,2 "$PASSDB"; }
# the POSIX account under the passdb's uid: pdbedit skips an entry whose uid differs
posix() {
	id -u "$1" >/dev/null 2>&1 && return
	[ -n "$2" ] && adduser -D -H -u "$2" -s /bin/false "$1" >/dev/null 2>&1 && return
	adduser -D -H -s /bin/false "$1" >/dev/null 2>&1 || return
	# the passdb's uid is taken (by the shared account): the entry follows the new one
	[ -n "$2" ] && sed -i "s/^$(echo "$1" | sed 's/\./\\./g'):$2:/$1:$(id -u "$1"):/" "$PASSDB"
}
admin() { [ -f "$D/admins" ] && grep -qxF -- "$1" "$D/admins"; }
gone() { pdbedit -x -u "$1" >/dev/null 2>&1 && echo "samba: account removed: $1 ($2)"; deluser "$1" >/dev/null 2>&1; }

# who has a password since when; the hashes pdbedit prints go no further than this pipe
status() {
	[ -f "$D/members" ] || return 0
	pdbedit -L -w 2>/dev/null | cut -d: -f1,6 | sed 's/:LCT-/ /' >"$D/lct.tmp"
	while read -r u; do
		lct=$(awk -v u="$u" '$1 == u { print $2; exit }' "$D/lct.tmp")
		case $lct in "" | *[!0-9A-Fa-f]*) lct=0 ;; esac
		echo "$u $((0x$lct))"
	done <"$D/members" >"$D/status.tmp"
	rm -f "$D/lct.tmp"
	mv "$D/status.tmp" "$D/status"
}

# the werkstatt share's group: the admins with an account, said when it changes
admins() {
	addgroup admins >/dev/null 2>&1
	in=""
	for e in $(accounts); do
		u=${e%%:*}
		if [ "$u" != "$SHARE_USER" ] && member "$u" && admin "$u"; then
			addgroup "$u" admins >/dev/null 2>&1
			in="$in $u"
		else
			delgroup "$u" admins >/dev/null 2>&1
		fi
	done
	[ "$in" = "${was-unset}" ] || echo "samba: werkstatt share for:${in:- nobody}"
	was=$in
}

# without a member list (LLDAP never answered yet) nobody is removed
reconcile() {
	for e in $(accounts); do
		name "${e%%:*}" && posix "${e%%:*}" "${e#*:}"
	done
	for e in $(accounts); do
		u=${e%%:*}
		[ "$u" = "$SHARE_USER" ] && continue
		if [ -f "$D/members" ] && ! member "$u"; then gone "$u" "not in family or admins"; fi
	done
	admins
	status
}

request() {
	case $1 in
	set)
		if ! name "$2" || ! member "$2"; then
			echo "samba: password refused: $2 is not in family or admins"
			return
		fi
		posix "$2"
		pw=$(printf %s "$3" | base64 -d 2>/dev/null)
		if [ -n "$pw" ] && printf '%s\n%s\n' "$pw" "$pw" | smbpasswd -s -a "$2" >/dev/null 2>&1 && smbpasswd -e "$2" >/dev/null 2>&1; then
			echo "samba: password set: $2"
		else
			echo "samba: password for $2 not set"
		fi
		pw=
		;;
	clear) name "$2" && gone "$2" "reset" ;;
	*) echo "samba: unknown request ${1:-(empty)}" ;;
	esac
	admins
	status
}

mkdir -p "$D"
[ -p "$D/passwords" ] || { rm -f "$D/passwords" && mkfifo -m 600 "$D/passwords"; }
reconcile
echo "samba: accounts in step with $D/members, passwords from $D/passwords"
# read-write: the FIFO always has a reader and a writer, so read never sees an end
exec 3<>"$D/passwords"
last=$(date +%s)
while :; do
	if read -r -t "$TICK" op who pw64 <&3; then
		request "$op" "$who" "$pw64"
		pw64=
	fi
	now=$(date +%s)
	if [ $((now - last)) -ge "$TICK" ]; then
		reconcile
		last=$now
	fi
done
