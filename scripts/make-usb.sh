#!/usr/bin/env bash
# Build a Fedora CoreOS install stick for a Laib box — the TUI's `sb build`
# as one script. Needs podman (or docker) on this machine; butane and
# coreos-installer run from their official containers.
#
#   scripts/make-usb.sh --hostname nuc --domain trial.example --lan-ip 192.168.178.30 \
#       --ssh-key ~/.ssh/id_ed25519.pub [--password] \
#       [--model-url http://192.168.178.100:11435/v1 --model-lease http://192.168.178.100:8787/api/model-lease | --gpu] \
#       [--token <github token while the repo is private>] [--device /dev/sdX] [--out laib-nuc.iso]
#
# Boot the stick: the live system picks the smallest non-removable disk
# (usb/pre-install.sh), installs CoreOS with this Ignition, promotes the SSD
# in the EFI boot order (usb/post-install.sh), reboots; on the first boot
# laib-firstboot.service runs scripts/install-box.sh with your flags.
set -euo pipefail
HERE="$(cd "$(dirname "$0")/.." && pwd)"
HOSTNAME_=""; DOMAIN=""; LAN_IP=""; SSH_KEY=""; PASSWORD=""; MODEL_URL=""; MODEL_LEASE=""; GPU="0"; TOKEN="${LAIB_GH_TOKEN:-}"; DEVICE=""; OUT=""; STREAM="stable"
while [ $# -gt 0 ]; do
  case "$1" in
    --hostname) HOSTNAME_="$2"; shift 2;;
    --domain) DOMAIN="$2"; shift 2;;
    --lan-ip) LAN_IP="$2"; shift 2;;
    --ssh-key) SSH_KEY="$2"; shift 2;;
    --password) read -r -s -p "console password for core: " PASSWORD; echo; shift;;
    --model-url) MODEL_URL="$2"; shift 2;;
    --model-lease) MODEL_LEASE="$2"; shift 2;;
    --gpu) GPU="1"; shift;;
    --token) TOKEN="$2"; shift 2;;
    --device) DEVICE="$2"; shift 2;;
    --out) OUT="$2"; shift 2;;
    --stream) STREAM="$2"; shift 2;;
    *) echo "unknown flag $1" >&2; exit 1;;
  esac
done
[ -n "$HOSTNAME_" ] && [ -n "$DOMAIN" ] && [ -n "$SSH_KEY" ] || { echo "need --hostname, --domain, --ssh-key" >&2; exit 1; }
RUN=podman; command -v podman >/dev/null || RUN=docker
WORK="$(mktemp -d)"; trap 'rm -rf "$WORK"' EXIT
OUT="${OUT:-laib-$HOSTNAME_.iso}"

HASH=""
if [ -n "$PASSWORD" ]; then HASH="$(openssl passwd -6 "$PASSWORD")"; fi
export SSH_AUTHORIZED_KEY="$(cat "$SSH_KEY")" PASSWORD_HASH="$HASH" HOSTNAME="$HOSTNAME_" DOMAIN LAN_IP MODEL_URL MODEL_LEASE GPU GH_TOKEN="$TOKEN"
python3 - "$HERE/usb/fedora-coreos.bu" "$WORK/box.bu" <<'PY'
import os, re, sys
src = open(sys.argv[1]).read()
out = re.sub(r"\$\{([A-Z_]+)\}", lambda m: os.environ.get(m.group(1), ""), src)
open(sys.argv[2], "w").write(out)
PY
if [ -z "$HASH" ]; then sed -i '/password_hash: ""/d' "$WORK/box.bu"; fi

echo "butane → ignition"
$RUN run --rm -i quay.io/coreos/butane:release --pretty --strict < "$WORK/box.bu" > "$WORK/box.ign"
echo "download fedora coreos $STREAM"
$RUN run --rm -v "$WORK:/data:z" -w /data quay.io/coreos/coreos-installer:release download -s "$STREAM" -p metal -f iso --decompress >/dev/null
ISO="$(ls "$WORK"/fedora-coreos-*.iso | head -1)"
cp "$HERE/usb/pre-install.sh" "$HERE/usb/post-install.sh" "$WORK/"
echo "customize: ignition + pre/post install (auto disk, EFI order)"
$RUN run --rm -v "$WORK:/data:z" -w /data quay.io/coreos/coreos-installer:release iso customize \
  --dest-ignition box.ign --pre-install pre-install.sh --post-install post-install.sh \
  --dest-device /dev/disk/by-id/placeholder \
  -o out.iso "$(basename "$ISO")"
# pre-install.sh overrides the placeholder through /etc/coreos/installer.d
cp "$WORK/out.iso" "$OUT"
echo "iso: $OUT ($(du -h "$OUT" | cut -f1))"
if [ -n "$DEVICE" ]; then
  echo "writing to $DEVICE (everything on it is lost) — Ctrl-C within 5 s to stop"; sleep 5
  sudo dd if="$OUT" of="$DEVICE" bs=4M status=progress conv=fsync
  echo "stick written; boot the box from it"
fi
