#!/bin/bash
# Druck's container (owner 2026-11-14): the system D-Bus, avahi-daemon, cupsd and the printer
# search (druck-finden.py), in this container's own namespaces but the host's network. When
# any of them ends, the container ends and Restart= brings all four back.
#
#   druck-start.sh          start
#   druck-start.sh gesund   the HealthCmd: cupsd answers, avahi runs, the search ran lately
set -eu
D=/data
if [ "${1:-}" = gesund ]; then
  lpstat -h /run/cups/cups.sock -r | grep -q "is running" || { echo "cupsd antwortet nicht"; exit 1; }
  avahi-daemon -c || { echo "avahi-daemon läuft nicht"; exit 1; }
  # the search writes its status every round (DRUCK_RUNDE, 2 min); 10 minutes without is a stuck search
  [ -n "$(find "$D/laib-status.json" -mmin -10 2>/dev/null)" ] || { echo "die Druckersuche lief seit 10 Minuten nicht"; exit 1; }
  exit 0
fi

mkdir -p "$D/cups/ppd" "$D/cups/ssl" /run/dbus /run/cups /run/avahi-daemon /var/spool/cups/tmp /var/cache/cups
# cupsd checks these owners and modes at its start; the spool is a tmpfs (druck.container)
chown root:lp "$D/cups" "$D/cups/ppd" /var/spool/cups /var/spool/cups/tmp && chmod 0755 "$D/cups" "$D/cups/ppd"
chmod 0710 /var/spool/cups && chmod 1770 /var/spool/cups/tmp && chmod 0700 "$D/cups/ssl"
for f in snmp.conf raw.types raw.convs; do [ -e /etc/cups/$f ] && [ ! -e "$D/cups/$f" ] && cp /etc/cups/$f "$D/cups/"; done

# avahi only on the LAN interface (the default route's): never on podman's bridges. Its own
# host name, so it never contends with another responder on the host for the box's name; the
# other stacks keep 5353 too (disallow-other-stacks=no: SO_REUSEPORT, as Samba's avahi and Home
# Assistant's zeroconf share it today).
dev=$(awk '$2 == "00000000" { print $1; exit }' /proc/net/route)
cat > /etc/avahi/avahi-daemon.conf <<EOF
[server]
host-name=${DRUCK_HOSTNAME:-drucker}
use-ipv4=yes
use-ipv6=yes
${dev:+allow-interfaces=$dev}
disallow-other-stacks=no
ratelimit-interval-usec=1000000
ratelimit-burst=1000
[wide-area]
enable-wide-area=no
[publish]
publish-hinfo=no
publish-workstation=no
publish-aaaa-on-ipv4=no
[reflector]
enable-reflector=no
EOF

cat > "$D/cups/cups-files.conf" <<EOF
ServerRoot $D/cups
RequestRoot /var/spool/cups
TempDir /var/spool/cups/tmp
CacheDir /var/cache/cups
StateDir /run/cups
SystemGroup root lpadmin
ErrorLog stderr
AccessLog stderr
PageLog /dev/null
EOF

# Printing on 631 from the box and the home network (@LOCAL: the subnets of the box's own
# interfaces; nftables keeps 631 off nothing, the router keeps it off the internet). Every
# administration only from inside this container as root: no web interface, /admin to
# localhost, every printer and server operation for @SYSTEM, which no remote client can be.
# Nothing kept after a job: no history, no files, no page log.
cat > "$D/cups/cupsd.conf" <<'EOF'
LogLevel warn
MaxLogSize 0
PageLogFormat
AccessLogLevel config
Listen /run/cups/cups.sock
Port 631
ServerAlias *
Browsing Yes
BrowseLocalProtocols dnssd
DefaultShared No
WebInterface No
DefaultAuthType Basic
PreserveJobHistory No
PreserveJobFiles No
MaxJobs 200
ErrorPolicy retry-job
JobRetryInterval 60
JobRetryLimit 120
<Location />
  Order allow,deny
  Allow localhost
  Allow @LOCAL
</Location>
<Location /admin>
  Order allow,deny
  Allow localhost
</Location>
<Location /admin/conf>
  AuthType Default
  Require user @SYSTEM
  Order allow,deny
  Allow localhost
</Location>
<Policy default>
  JobPrivateAccess default
  JobPrivateValues default
  SubscriptionPrivateAccess default
  SubscriptionPrivateValues default
  <Limit Create-Job Print-Job Print-URI Validate-Job>
    Order deny,allow
  </Limit>
  <Limit Send-Document Send-URI Hold-Job Release-Job Restart-Job Purge-Jobs Set-Job-Attributes Create-Job-Subscription Renew-Subscription Cancel-Subscription Get-Notifications Reprocess-Job Cancel-Current-Job Suspend-Current-Job Resume-Job Cancel-My-Jobs Close-Job CUPS-Move-Job CUPS-Get-Document Cancel-Job CUPS-Authenticate-Job>
    Require user @OWNER @SYSTEM
    Order deny,allow
  </Limit>
  <Limit CUPS-Add-Modify-Printer CUPS-Delete-Printer CUPS-Add-Modify-Class CUPS-Delete-Class CUPS-Set-Default CUPS-Get-Devices Pause-Printer Resume-Printer Enable-Printer Disable-Printer Pause-Printer-After-Current-Job Hold-New-Jobs Release-Held-New-Jobs Deactivate-Printer Activate-Printer Restart-Printer Shutdown-Printer Startup-Printer Promote-Job Schedule-Job-After Cancel-Jobs CUPS-Accept-Jobs CUPS-Reject-Jobs>
    AuthType Default
    Require user @SYSTEM
    Order deny,allow
  </Limit>
  <Limit All>
    Order deny,allow
  </Limit>
</Policy>
EOF

dbus-daemon --system --nofork --nopidfile &
for _ in 1 2 3 4 5 6 7 8 9 10; do [ -S /run/dbus/system_bus_socket ] && break; sleep 0.5; done
avahi-daemon --no-chroot --no-rlimits &
cupsd -f -c "$D/cups/cupsd.conf" -s "$D/cups/cups-files.conf" &
for _ in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do lpstat -h /run/cups/cups.sock -r >/dev/null 2>&1 && break; sleep 0.5; done
CUPS_SERVER=/run/cups/cups.sock python3 /laib/druck-finden.py &
wait -n 2>/dev/null || wait
echo "ein Teil des Druckservers endete; der Container startet neu" >&2
exit 1
