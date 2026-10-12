#!/usr/bin/env bash
# Runs from the live-USB environment AFTER coreos-installer has written
# FCoS to the SSD and registered the new EFI boot entry, BEFORE the
# system reboots into the installed OS for the first time.
#
# Goal: make the FIRST reboot land on installed FCoS, not back into the
# live USB. Without this, the BIOS happily picks the still-first USB
# entry from BootOrder, the live ISO runs the install again, the OS
# reboots, the BIOS picks the USB again, ... — the #930 reboot loop.
#
# Strategy (defense in depth):
#   1. Modify BootOrder so the OS entry is first and USB entries are
#      pushed to the end. Persistent across reboots.
#   2. Set BootNext to the OS entry. One-shot override the BIOS
#      consumes on the next boot — this is the bulletproof guarantee
#      even if (1) somehow fails or the BIOS reverts NVRAM writes.
#
# After we reach the installed OS, laib-boot-order.service puts the
# entry it booted from first and deactivates USB entries with
# `efibootmgr -A` at every boot. Whatever the firmware does anyway,
# the live system refuses to reinstall an installed box unless the
# stick was built with --reinstall (pre-install.sh, #33).
#
# Verbose throughout — any failure visible in the live-ISO console.

set -euo pipefail

echo "post-install: starting (date=$(date -u +%FT%TZ))"
echo "post-install: efibootmgr -v before:"
efibootmgr -v || true
echo "----"

# The entry for the disk just installed, found by its EFI partition's PARTUUID
# and created here when the firmware has none (#33): coreos-installer writes no
# NVRAM entry, and the old path pattern also matched the removable-media path
# \EFI\BOOT\ — the stick's own entry — and promoted it.
if [[ ! -d /sys/firmware/efi ]]; then
  echo "post-install: BIOS boot, nothing to do"
  exit 0
fi
DEST=$(sed -n 's/^dest-device: *//p' /etc/coreos/installer.d/0050-dest-device.yaml 2>/dev/null || true)
ESP=$(lsblk -nrpo NAME,LABEL "$DEST" 2>/dev/null | awk '$2=="EFI-SYSTEM" {print $1; exit}')
if [[ -z "$DEST" || -z "$ESP" ]]; then
  echo "post-install: WARNING — no EFI-SYSTEM partition on '$DEST'; BootOrder unchanged, pick the disk in the firmware's boot menu" >&2
  exit 0
fi
PARTUUID=$(lsblk -no PARTUUID "$ESP")
entry_of_esp() { efibootmgr -v | grep -i "$PARTUUID" | grep -oP '^Boot\K[0-9A-Fa-f]{4}' | head -1 || true; }
ENTRY=$(entry_of_esp)
if [[ -z "$ENTRY" ]]; then
  PARTNUM=$(cat "/sys/class/block/$(basename "$ESP")/partition")
  efibootmgr -q --create --disk "$DEST" --part "$PARTNUM" --label "Fedora CoreOS (Laib)" --loader '\EFI\fedora\shimx64.efi'
  ENTRY=$(entry_of_esp)
fi
if [[ -z "$ENTRY" ]]; then
  echo "post-install: WARNING — no boot entry for $ESP ($PARTUUID); pick the disk in the firmware's boot menu" >&2
  exit 0
fi

echo "post-install: OS boot entry = Boot$ENTRY"

# --- BootOrder: OS first, USB last ---

CURRENT=$(efibootmgr | grep -oP 'BootOrder: \K.*' || true)
if [[ -z "$CURRENT" ]]; then
  echo "post-install: BootOrder was empty; setting to $ENTRY"
  efibootmgr -o "$ENTRY"
else
  # strip OS entry from current order, then prepend it
  WITHOUT_ENTRY=$(echo "$CURRENT" | sed -E "s/(^|,)$ENTRY(,|\$)/\1\2/" | sed -E 's/^,+//; s/,+$//; s/,,+/,/g')
  NEW_ORDER="$ENTRY${WITHOUT_ENTRY:+,$WITHOUT_ENTRY}"

  # collect USB / removable entries
  USB_LIST=$(efibootmgr -v | grep -i -E 'usb|removable' | grep -oP 'Boot\K[0-9A-Fa-f]+' | tr '\n' ',' | sed 's/,$//' || true)
  if [[ -n "$USB_LIST" ]]; then
    DEMOTED=""
    IFS=',' read -ra USB_ARR <<< "$USB_LIST"
    for u in "${USB_ARR[@]}"; do
      if [[ "$u" != "$ENTRY" ]]; then
        # remove $u from NEW_ORDER if present
        NEW_ORDER=$(echo "$NEW_ORDER" | sed -E "s/(^|,)$u(,|\$)/\1\2/" | sed -E 's/^,+//; s/,+$//; s/,,+/,/g')
        DEMOTED="${DEMOTED:+$DEMOTED,}$u"
      fi
    done
    [[ -n "$DEMOTED" ]] && NEW_ORDER="$NEW_ORDER,$DEMOTED"
  fi
  NEW_ORDER=$(echo "$NEW_ORDER" | sed -E 's/^,+//; s/,+$//; s/,,+/,/g')

  echo "post-install: setting BootOrder $CURRENT -> $NEW_ORDER"
  efibootmgr -o "$NEW_ORDER" || echo "post-install: WARNING — efibootmgr -o exited non-zero" >&2
fi

# --- BootNext: one-shot override for the FIRST reboot ---
#
# Even if the BootOrder write above is reverted by the BIOS or another
# layer, BootNext wins for the next boot. The BIOS clears BootNext
# automatically after consuming it, so this only redirects the single
# critical first reboot — subsequent reboots fall through to BootOrder
# (which we also set above; laib-boot-order.service puts the installed
# entry first and switches USB entries off at every boot of the installed OS).
echo "post-install: setting BootNext = $ENTRY (one-shot override for first reboot)"
efibootmgr -n "$ENTRY" || echo "post-install: WARNING — efibootmgr -n exited non-zero" >&2

# --- Verify ---
VERIFY_ORDER=$(efibootmgr | grep -oP 'BootOrder: \K.*' || true)
VERIFY_NEXT=$(efibootmgr | grep -oP 'BootNext: \K[0-9A-Fa-f]+' || true)
echo "post-install: after — BootOrder=$VERIFY_ORDER  BootNext=$VERIFY_NEXT"
if [[ "${VERIFY_NEXT,,}" == "${ENTRY,,}" ]]; then
  echo "post-install: BootNext set successfully — first reboot will pick installed FCoS"
else
  echo "post-install: WARNING — BootNext does not match expected $ENTRY; operator may need to pick SSD from BIOS boot menu" >&2
fi
echo "post-install: done"
