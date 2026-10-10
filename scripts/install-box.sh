#!/usr/bin/env bash
# Install or update the Laib binary and its user unit on this box. Idempotent.
# Changes nothing about services: after this, `laib plan` shows what would.
#
#   curl -fsSL https://raw.githubusercontent.com/mdopp/laib/main/scripts/install-box.sh | bash -s -- --domain example.home [--version vX.Y.Z]
set -euo pipefail
VERSION="latest"; DOMAIN=""; LAN_IP=""; MODEL_URL=""; MODEL_LEASE=""; GPU=""
while [ $# -gt 0 ]; do
  case "$1" in
    --version) VERSION="$2"; shift 2;;
    --domain) DOMAIN="$2"; shift 2;;
    --lan-ip) LAN_IP="$2"; shift 2;;
    --model-url) MODEL_URL="$2"; shift 2;;      # no GPU on this box: a peer's llama proxy (http://<peer>:11435/v1) or a cloud API
    --model-lease) MODEL_LEASE="$2"; shift 2;;
    --gpu) GPU="--gpu"; shift;;
    *) echo "unknown flag $1" >&2; exit 1;;
  esac
done
[ "$(id -un)" = "core" ] || { echo "run as the core user (rootless podman, user systemd)" >&2; exit 1; }

if [ "$VERSION" = "latest" ]; then
  URL="https://github.com/mdopp/laib/releases/latest/download/laib-linux-amd64"
else
  URL="https://github.com/mdopp/laib/releases/download/$VERSION/laib-linux-amd64"
fi
sudo mkdir -p /var/opt/laib
TMP="$(mktemp)"
curl -fsSL -o "$TMP" "$URL"
chmod 0755 "$TMP"
sudo install -m 0755 "$TMP" /var/opt/laib/laib.new
sudo mv /var/opt/laib/laib.new /var/opt/laib/laib
rm -f "$TMP"
sudo mkdir -p /mnt/data/laib && sudo chown core:core /mnt/data/laib

mkdir -p ~/.config/systemd/user
curl -fsSL -o ~/.config/systemd/user/laib.service https://raw.githubusercontent.com/mdopp/laib/main/dist/laib.service
systemctl --user daemon-reload
loginctl enable-linger core >/dev/null 2>&1 || sudo loginctl enable-linger core

if [ -n "$DOMAIN" ] && [ ! -f /mnt/data/laib/state/box.yml ]; then
  LAIB_HOME=/mnt/data/laib LAIB_ACTOR=human:core /var/opt/laib/laib init --domain "$DOMAIN" ${LAN_IP:+--lan-ip "$LAN_IP"} ${MODEL_URL:+--model-url "$MODEL_URL"} ${MODEL_LEASE:+--model-lease "$MODEL_LEASE"} $GPU
fi
systemctl --user enable --now laib.service
sleep 1
echo -n "ready: "; curl -fsS http://127.0.0.1:5900/ready || echo "(not yet)"
echo
echo "next: /var/opt/laib/laib plan   (shows every unit on the box as 'nicht verwaltet' until adopted or ignored)"
