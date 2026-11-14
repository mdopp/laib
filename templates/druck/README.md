# druck — every printer for every device, without an app (owner 2026-11-14)

The household's Epson WF-3540 takes only `image/urf`, `image/jpeg` and its own
ESC/P-R over IPP, so Android's built-in print service could not print to it
without Epson's app. The print server takes over: it finds every printer in the
home network and at the box's USB, gives each the right driver, and offers it
on the LAN as an IPP Everywhere / AirPrint / Mopria printer that takes PDF.
Android's default print service, iOS, macOS, Windows and Linux print to it
with nothing installed. Home network only: no route, never public.

## What runs

One container, `ghcr.io/mdopp/laib-druck` (`druck/Containerfile` in this
repo, Debian trixie-slim pinned by digest; built and cosign-signed by
`druck-image.yml` on main, as laib-caddy, ADR 0034). In it: the system D-Bus,
`avahi-daemon`, `cupsd` and the printer search `druck-finden.py`. The start
script and the search are this template's files, mounted read-only: a change
to the rule reaches the box with a template update, without a new image.

The image bundles the drivers; nothing is installed at run time:

| | |
|---|---|
| driverless | cups-filters' `driverless` PPDs (IPP Everywhere, AirPrint's URF, PDF) and CUPS' `everywhere` |
| Epson | `printer-driver-escpr` (ESC/P-R: the WF-3540 and most Epson inkjets up to ~2019) |
| HP | `printer-driver-hpcups` (hplip's driver; the hplip toolbox, its Python and scanning are left out, ~120 MB) |
| many others | `printer-driver-gutenprint` |

Epson's newer `escpr2` is not in Debian and Epson keeps no fixed source
address to pin; the printers it serves speak IPP Everywhere and get the
driverless queue.

## The rule (`druck-finden.py`, every 2 minutes)

1. **Found** is what CUPS finds: `lpinfo -v` with the `dnssd` backend (every
   printer announced as `_ipp`, `_ipps`, `_pdl-datastream` or `_printer`) and
   the `usb` backend. A network printer is one DNS-SD name, whichever of its
   services is there (IPP first). Its queue's URI is that name
   (`dnssd://EPSON%20WF-3540%20Series._ipp._tcp.local/`), resolved at every
   job: a new address after a DHCP change, or a printer that sleeps and comes
   back, is the same queue. No address is stored.
2. **Driver** is CUPS' own matching, `lpinfo --device-id <IEEE 1284 ID> -m`:
   a printer that takes PDF or PWG raster gets the driverless queue; any
   other the vendor driver CUPS matches, escpr before hpcups before
   Gutenprint before the rest, never a generic PPD; none of those and URF:
   driverless over URF; nothing: no queue, and the status says which printer
   and why. A USB printer always takes its vendor driver (driverless over USB
   would need ipp-usb).
3. **Shared or not.** A network printer that already reaches every device by
   itself — it announces URF (AirPrint: iPhone, iPad, Mac) *and* PDF or PWG
   raster (Android, Windows, Linux) — is not offered a second time; its queue
   stays the box's own (Homes and Paperless may print through it later),
   unshared. Every other printer is shared as „<Name> (über die Box)“: a USB
   printer has no other way into the home network, and one like the Epson
   gains PDF. The WF-3540 is the second kind.
4. **The box's own shared queues** are announced too, and never found again
   as printers: a name with „(über die Box)“ is skipped.
5. **Gone:** a queue whose printer was not seen for `DRUCK_VERGESSEN_TAGE`
   days (30) is removed. Until then it stays, and a job waits for the
   printer (`printer-error-policy retry-job`, retried every minute, two hours
   at most).

The queue names are stable (`bekannt.json`): `Epson_WF-3540`, its
description „Epson WF-3540 (über die Box)“, which is what phones show.

## Discovery: avahi in the container, on its own host name

DNS-SD is multicast on the LAN, so the service is on the host network:
box.yml `hostNetwork` must list `druck` — **the owner's decision**, put on the
proposal card when the template is added.

The box host runs no avahi of its own: on 2026-11-14 every socket on UDP
5353 belonged to a container's user (`/proc/net/udp` read from a
host-network container: root of core's containers, and uid 86, Alpine's
avahi, which is Samba's image in file-share), none to a host user; the others
are Home Assistant's, Music Assistant's and ESPHome's zeroconf, so there is no host D-Bus avahi to borrow, and borrowing
file-share's would tie printing to Samba. CUPS needs avahi's client API, so
the container runs its own `avahi-daemon` with:

- `disallow-other-stacks=no`: it shares 5353 with the others by
  SO_REUSEPORT, as they share it with each other today;
- its own `host-name` (`DRUCK_HOSTNAME`, `drucker`): it announces
  `drucker.local` and never the box's own name, which Samba's avahi
  announces, so the two never contend for one name;
- `allow-interfaces` set to the default route's interface: nothing goes out
  on podman's bridges; no workstation or HINFO records.

`libnss-mdns` lets the backends resolve `.local` names.

## Ports and administration

| Port | | |
|---|---|---|
| 631/tcp | `lan: true` | IPP: printing from every device at home. cupsd allows `localhost` and `@LOCAL` (the box's own subnets); no route through Caddy, the router keeps it off the internet |
| 5353/udp | — | mDNS, shared as above |

The CUPS web interface is off (`WebInterface No`). `/admin` answers only
`localhost`, and every printer and server operation (add, delete, pause,
accept, the defaults) requires `@SYSTEM`: root inside this container over
cupsd's own socket. No password exists for a remote client to try. The
queues are the search's to make; by hand, `laib exec druck -- lpstat -t`.
A job's owner may cancel their own job (`@OWNER`), nobody another's.

## Nothing kept after printing

`PreserveJobHistory No` and `PreserveJobFiles No`: a job leaves no record
once it is printed. The spool is a tmpfs: a job waiting for a printer that is
off lives in memory, at most two hours, and is gone on a restart. No page log
(`PageLogFormat` empty); the access log has configuration changes only.
docs/DATA.md has the flow.

## Health and status

The container's `HealthCmd` (`druck-start.sh gesund`) is red when cupsd does
not answer `lpstat -r`, avahi-daemon is not running, or the search has not
written its status for 10 minutes; three reds end the container
(`HealthOnFailure=kill`) and `Restart=` brings it back, so the unit state
carries it (laib.yml `health: none`). Zero printers is not red: the status
says „Noch kein Drucker im Heimnetz oder am USB der Box gefunden.“

`${DATA_DIR}/druck/laib-status.json`: `detail` („2 Drucker eingerichtet,
über die Box angeboten: Epson WF-3540.“), `warteschlangen` (the number of
queues; `null` when the search did not run through), `geteilt`, and a finding
per printer without a driver or a queue that could not be made.

## A printer at the box's USB

As the scan service (#801): `/dev/bus/usb` is passed whole, and the host's
udev rule `99-laib-printer.rules` (written by `install-box.sh`, so with the
next Laib release on a box in use) makes every USB device with a printer
interface (class 07) core's, which the rootless container opens. A printer
plugged in after the start: the search sees it in sysfs without its node,
exits once, and `Restart=` brings the container back with it.

## Home Assistant

`/data/ha-drucker.json` lists what Home Assistant's `ipp` integration should
have: each network printer that speaks IPP by its own name and path (the
direct entry reports its ink levels), each printer only the box reaches (USB,
or one without IPP) by the box's queue, `127.0.0.1:631/printers/<queue>`.
The re-share of a network printer is never on it. The home-assistant
template's `home-assistant-drucker` (every 15 minutes) makes the entries.

## Values

| Value | Default | |
|---|---|---|
| `DRUCK_IMAGE` | `ghcr.io/mdopp/laib-druck:1` | the box pins the digest it runs („Images festhalten“, ADR 0034) |
| `DRUCK_HOSTNAME` | `drucker` | the name the queues are announced under, `drucker.local` |
| `DRUCK_VERGESSEN_TAGE` | `30` | |

No secret. `laib add druck`; the card names the host network carve-out.

## Accepted when

1. An Android phone with no Epson app prints a PDF to „Epson WF-3540 (über
   die Box)“ from its own print menu; an iPhone prints by AirPrint.
2. The Epson gets a new address from the router: the next job still prints.
3. `laib net` shows 631 open to the LAN only, and no route.
