#!/usr/bin/env bash
set -euo pipefail
SYS="${LAIB_SYS:-/sys}"                                   # test seams (internal/stick)
INSTALLER_D="${LAIB_INSTALLER_D:-/etc/coreos/installer.d}"

# Set hostname in live environment so the DHCP lease registers the correct name
# (routers like FritzBox learn the hostname from DHCP and cache it)
hostnamectl set-hostname "$SERVER_NAME" 2>/dev/null || hostname "$SERVER_NAME"

# Find the smallest internal disk for the OS: never the live stick, never a USB
# or removable disk (#32). /run is a tmpfs, so asking findmnt about /run never
# named the stick; the live ISO is mounted at /run/media/iso, and USB is the
# guard that holds either way.
LIVE_DISK=$(lsblk -ndo PKNAME "$(findmnt -n -o SOURCE /run/media/iso 2>/dev/null)" 2>/dev/null || echo "")

BEST=""
BEST_SIZE=0

# Every whole disk by type, not by major number: SATA/SAS (8), NVMe (259),
# virtio (253/254 — a VM, the CI boot test), MMC/eMMC (179). Not: rom
# (the live CD), loop, zram.
for dev in $(lsblk -dnpo NAME,TYPE | awk '$2=="disk"{print $1}'); do
  name=$(basename "$dev")
  # Skip live USB, zram and loop devices
  [[ "$name" == "$LIVE_DISK" ]] && continue
  [[ "$name" == zram* || "$name" == loop* ]] && continue
  # Skip removable and USB disks: the install stick, a backup drive, an SD reader
  [[ "$(cat $SYS/block/${name}/removable 2>/dev/null)" == "1" ]] && continue
  [[ "$(lsblk -dno TRAN "$dev" 2>/dev/null | tr -d ' ')" == "usb" ]] && continue
  size=$(blockdev --getsize64 "$dev" 2>/dev/null || echo 0)
  (( size == 0 )) && continue
  # Pick smallest
  if [[ -z "$BEST" ]] || (( size < BEST_SIZE )); then
    BEST_SIZE=$size
    BEST=$dev
  fi
done

if [[ -z "$BEST" ]]; then
  echo "pre-install: ERROR: no suitable OS disk found" >&2
  exit 1
fi

echo "pre-install: selected $BEST ($(( BEST_SIZE / 1073741824 )) GiB) as OS disk"

# An installed box is not installed again by accident (#33): a stick left in a
# machine that boots USB first runs this live system at every reboot. Only a
# stick built with `laib-stick --reinstall` reinstalls; the data array stays
# either way. Internal disks only, like the OS disk above (the stick itself
# carries an EFI-SYSTEM partition too).
INSTALLED=""
for dev in $(lsblk -dnpo NAME,TYPE | awk '$2=="disk"{print $1}'); do
  name=$(basename "$dev")
  [[ "$name" == "$LIVE_DISK" || "$name" == zram* || "$name" == loop* ]] && continue
  [[ "$(cat $SYS/block/${name}/removable 2>/dev/null)" == "1" ]] && continue
  [[ "$(lsblk -dno TRAN "$dev" 2>/dev/null | tr -d ' ')" == "usb" ]] && continue
  labels=" $(lsblk -nro LABEL "$dev" | tr '\n' ' ') "
  if [[ "$labels" == *" boot "* && "$labels" == *" root "* ]]; then
    INSTALLED="$INSTALLED $dev (Fedora CoreOS)"
  fi
  for part in $(lsblk -nrpo NAME "$dev"); do
    if mdadm --examine --export "$part" 2>/dev/null | grep -q '^MD_NAME=.*:data$'; then
      INSTALLED="$INSTALLED $part (Laib-Daten)"
      break
    fi
  done
done
if [[ -n "$INSTALLED" && "${LAIB_REINSTALL:-}" != "1" ]]; then
  MSG="laib: Diese Box ist schon installiert:$INSTALLED. Dieser Stick installiert sie nicht noch einmal - bitte den Stick abziehen und die Box neu starten. Neu aufsetzen (die Daten bleiben) kann nur ein Stick von: laib-stick --reinstall"
  echo "$MSG" >&2
  echo "$MSG" > /dev/console 2>/dev/null || true
  exit 1
fi
[[ -n "$INSTALLED" ]] && echo "pre-install: reinstalling over$INSTALLED (laib-stick --reinstall); the data array is kept"

# Write dest-device into installer.d so coreos-installer picks it up
mkdir -p "$INSTALLER_D"
cat > "$INSTALLER_D/0050-dest-device.yaml" <<DISKEOF
dest-device: $BEST
DISKEOF
# In a VM (the CI job) the installed system also talks to the serial console:
# a boot that never reaches SSH is otherwise invisible (#37, run 38160584395).
if systemd-detect-virt --quiet 2>/dev/null; then
  cat > "$INSTALLER_D/0060-vm-console.yaml" <<KARGEOF
append-karg: ["console=tty0", "console=ttyS0,115200n8"]
KARGEOF
  echo "pre-install: virtual machine — serial console karg appended"
fi
