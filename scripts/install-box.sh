#!/usr/bin/env bash
# Install or update the Laib binaries and the user unit on this box. Idempotent.
# Changes nothing about services: after this, `laib plan` shows what would.
#
#   curl … install-box.sh | bash -s -- --domain example.home [--lan-ip <ip>] \
#       [--gpu | --model-url http://<gpu-box>:11435/v1 [--model-lease <url>]] \
#       [--version vX.Y.Z] [--token <github token, while the repo is private>]
#
# While mdopp/laib is private, release assets need a GitHub token (fine-grained,
# Contents: read on this repo): pass --token or export LAIB_GH_TOKEN. A public
# repo needs none.
set -euo pipefail
VERSION="latest"; DOMAIN=""; LAN_IP=""; MODEL_URL=""; MODEL_LEASE=""; GPU=""; TOKEN="${LAIB_GH_TOKEN:-}"
while [ $# -gt 0 ]; do
  case "$1" in
    --version) VERSION="$2"; shift 2;;
    --domain) DOMAIN="$2"; shift 2;;
    --lan-ip) LAN_IP="$2"; shift 2;;
    --model-url) MODEL_URL="$2"; shift 2;;
    --model-lease) MODEL_LEASE="$2"; shift 2;;
    --gpu) GPU="--gpu"; shift;;
    --token) TOKEN="$2"; shift 2;;
    *) echo "unknown flag $1" >&2; exit 1;;
  esac
done
[ "$(id -un)" = "core" ] || { echo "run as the core user (rootless podman, user systemd)" >&2; exit 1; }
REPO="mdopp/laib"
API="https://api.github.com/repos/$REPO"

# Resolve the release: its tag and the API url of every asset.
auth=()
[ -n "$TOKEN" ] && auth=(-H "Authorization: Bearer $TOKEN")
if [ "$VERSION" = "latest" ]; then REL="$API/releases/latest"; else REL="$API/releases/tags/$VERSION"; fi
RELEASE_JSON="$(curl -fsSL "${auth[@]}" -H "Accept: application/vnd.github+json" "$REL")" || { echo "cannot read release $VERSION (private repo? pass --token)" >&2; exit 1; }
TAG="$(printf '%s' "$RELEASE_JSON" | python3 -c 'import sys,json; print(json.load(sys.stdin)["tag_name"])')"
asset_url() { printf '%s' "$RELEASE_JSON" | python3 -c 'import sys,json; n=sys.argv[1]; print(next(a["url"] for a in json.load(sys.stdin)["assets"] if a["name"]==n))' "$1"; }

sudo mkdir -p /var/opt/laib
TMPD="$(mktemp -d)"; trap 'rm -rf "$TMPD"' EXIT
for BIN in laib laib-operator laib-lease; do
  curl -fsSL "${auth[@]}" -H "Accept: application/octet-stream" -o "$TMPD/$BIN" "$(asset_url "$BIN-linux-amd64")"
done
curl -fsSL "${auth[@]}" -H "Accept: application/octet-stream" -o "$TMPD/SHA256SUMS" "$(asset_url SHA256SUMS)" || true
if [ -s "$TMPD/SHA256SUMS" ]; then
  (cd "$TMPD" && for BIN in laib laib-operator laib-lease; do
     want="$(grep " $BIN-linux-amd64\$" SHA256SUMS | cut -d' ' -f1)"; have="$(sha256sum "$BIN" | cut -d' ' -f1)"
     [ "$want" = "$have" ] || { echo "checksum mismatch for $BIN" >&2; exit 1; }
   done)
fi
for BIN in laib laib-operator laib-lease; do
  sudo install -m 0755 "$TMPD/$BIN" "/var/opt/laib/$BIN.new" && sudo mv "/var/opt/laib/$BIN.new" "/var/opt/laib/$BIN"
done
sudo mkdir -p /mnt/data/laib && sudo chown core:core /mnt/data/laib

# The user unit, inline so a private repo needs no second fetch (dist/laib.service is the same text).
mkdir -p ~/.config/systemd/user
cat > ~/.config/systemd/user/laib.service <<'UNIT'
[Unit]
Description=Laib — control plane for this box
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/var/opt/laib/laib serve
Environment=LAIB_HOME=/mnt/data/laib
Environment=LAIB_ACTOR=laib
Restart=always
RestartSec=3

[Install]
WantedBy=default.target
UNIT
systemctl --user daemon-reload
loginctl enable-linger core >/dev/null 2>&1 || sudo loginctl enable-linger core

if [ -n "$DOMAIN" ] && [ ! -f /mnt/data/laib/state/box.yml ]; then
  LAIB_HOME=/mnt/data/laib LAIB_ACTOR=human:core /var/opt/laib/laib init --domain "$DOMAIN" ${LAN_IP:+--lan-ip "$LAN_IP"} ${MODEL_URL:+--model-url "$MODEL_URL"} ${MODEL_LEASE:+--model-lease "$MODEL_LEASE"} $GPU
fi
systemctl --user enable --now laib.service
sleep 1
echo "installed $TAG: $(/var/opt/laib/laib version)"
echo -n "ready: "; curl -fsS http://127.0.0.1:5900/ready || echo "(not yet)"
echo
echo "next: /var/opt/laib/laib plan   (shows every unit on the box as 'nicht verwaltet' until adopted or ignored)"
echo "      /var/opt/laib/laib operator enable   (mints the ops token and starts the operator; needs a model)"
