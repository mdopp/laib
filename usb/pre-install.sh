#!/usr/bin/env bash
set -euo pipefail

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
  [[ "$(cat /sys/block/${name}/removable 2>/dev/null)" == "1" ]] && continue
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

# Write dest-device into installer.d so coreos-installer picks it up
mkdir -p /etc/coreos/installer.d
cat > /etc/coreos/installer.d/0050-dest-device.yaml <<DISKEOF
dest-device: $BEST
DISKEOF
