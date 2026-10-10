#!/usr/bin/env bash
set -euo pipefail

# Set hostname in live environment so the DHCP lease registers the correct name
# (routers like FritzBox learn the hostname from DHCP and cache it)
hostnamectl set-hostname "$SERVER_NAME" 2>/dev/null || hostname "$SERVER_NAME"

# Find the smallest non-removable disk (that's not the live USB) for OS install.
# The live USB is the device backing /run/media or the ISO boot.
LIVE_DISK=$(lsblk -ndo PKNAME "$(findmnt -n -o SOURCE /run)" 2>/dev/null || echo "")

BEST=""
BEST_SIZE=0

for dev in $(lsblk -dnpo NAME -I 8,259,254); do
  name=$(basename "$dev")
  # Skip live USB
  [[ "$name" == "$LIVE_DISK" ]] && continue
  # Skip removable
  [[ "$(cat /sys/block/${name}/removable 2>/dev/null)" == "1" ]] && continue
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
