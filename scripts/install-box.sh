#!/usr/bin/env bash
# Install or update the Laib binaries and the user unit on this box. Idempotent.
# Changes nothing about services: after this, `laib plan` shows what would.
#
#   curl … install-box.sh | bash -s -- --domain example.home [--lan-ip <ip>] \
#       [--gpu | --model-url http://<gpu-box>:11435/v1 [--model-lease <url>]] \
#       [--version vX.Y.Z] [--token <github token, while the repo is private>]
#       [--dns-provider desec|cloudflare|…  (token: LAIB_DNS_TOKEN)] [--tls internal]
#       [--source <base url>]   one build served over HTTP instead of a release (the VM job, #38)
#
# While mdopp/laib is private, release assets need a GitHub token (fine-grained,
# Contents: read on this repo): pass --token or export LAIB_GH_TOKEN. A public
# repo needs none.
set -euo pipefail
VERSION="latest"; DOMAIN=""; LAN_IP=""; MODEL_URL=""; MODEL_LEASE=""; GPU=""; TOKEN="${LAIB_GH_TOKEN:-}"; SOURCE=""
DNS_PROVIDER=""; DNS_TOKEN="${LAIB_DNS_TOKEN:-}"; TLS=""   # certificates (#45): DNS-01 via the provider's API, or Caddy's own CA
while [ $# -gt 0 ]; do
  case "$1" in
    --version) VERSION="$2"; shift 2;;
    --source) SOURCE="${2%/}"; shift 2;;
    --domain) DOMAIN="$2"; shift 2;;
    --lan-ip) LAN_IP="$2"; shift 2;;
    --model-url) MODEL_URL="$2"; shift 2;;
    --model-lease) MODEL_LEASE="$2"; shift 2;;
    --gpu) GPU="--gpu"; shift;;
    --token) TOKEN="$2"; shift 2;;
    --dns-provider) DNS_PROVIDER="$2"; shift 2;;
    --tls) TLS="$2"; shift 2;;
    *) echo "unknown flag $1" >&2; exit 1;;
  esac
done
[ "$(id -un)" = "core" ] || { echo "run as the core user (rootless podman, user systemd)" >&2; exit 1; }
REPO="mdopp/laib"
API="https://api.github.com/repos/$REPO"

# Resolve what to install: a release (its tag and the API url of every asset),
# or one build served at --source under the release's asset names plus VERSION.
auth=()
[ -n "$TOKEN" ] && auth=(-H "Authorization: Bearer $TOKEN")
if [ -n "$SOURCE" ]; then
  TAG="$(curl -fsSL "$SOURCE/VERSION")" || { echo "cannot read $SOURCE/VERSION" >&2; exit 1; }
  [ -n "$TAG" ] || { echo "$SOURCE/VERSION is empty" >&2; exit 1; }
  get=(-H "Accept: application/octet-stream")   # the GitHub token never goes to a build server
  asset_url() { printf '%s/%s' "$SOURCE" "$1"; }
else
  if [ "$VERSION" = "latest" ]; then REL="$API/releases/latest"; else REL="$API/releases/tags/$VERSION"; fi
  RELEASE_JSON="$(curl -fsSL "${auth[@]}" -H "Accept: application/vnd.github+json" "$REL")" || { echo "cannot read release $VERSION (private repo? pass --token)" >&2; exit 1; }
  # No python3 on Fedora CoreOS: pick the fields out of the JSON with sed. An
  # asset object starts with its API "url" and carries "name" before any nested
  # object, so url..name within one object is a match without a brace in between.
  FLAT="$(printf '%s' "$RELEASE_JSON" | tr -d '\n')"
  TAG="$(printf '%s' "$FLAT" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p')"
  [ -n "$TAG" ] || { echo "release JSON without tag_name" >&2; exit 1; }
  get=("${auth[@]}" -H "Accept: application/octet-stream")
  asset_url() {
    local u
    u="$(printf '%s' "$FLAT" | grep -o '"url": *"[^"]*/releases/assets/[0-9]*"[^{}]*"name": *"'"$1"'"' | head -1 | sed -n 's/^"url": *"\([^"]*\)".*/\1/p')"
    [ -n "$u" ] || { echo "release $TAG has no asset $1" >&2; return 1; }
    printf '%s' "$u"
  }
fi

# Fedora CoreOS ships without git, restic and diff; laib shells out to all
# three (state repo, backups, plan diffs). Layer them live, once. If the live
# apply is refused (a pending deployment), reboot into the layered tree: the
# first-boot unit re-runs this script, it is idempotent.
need=""
for t in git:git restic:restic diff:diffutils; do command -v "${t%%:*}" >/dev/null 2>&1 || need="$need ${t##*:}"; done
if [ -n "$need" ] && command -v rpm-ostree >/dev/null 2>&1; then
  echo "layering$need (rpm-ostree, live)"
  # shellcheck disable=SC2086
  if ! sudo rpm-ostree install --idempotent --allow-inactive --apply-live $need; then
    echo "live apply refused; rebooting into the layered tree, the first-boot unit continues" >&2
    sudo systemctl reboot; exit 75   # EX_TEMPFAIL: the caller keeps what the next run needs
  fi
fi
for t in git restic diff; do command -v "$t" >/dev/null 2>&1 || { echo "$t still missing" >&2; exit 1; }; done

# Rootless Caddy (80/443) and AdGuard (53): low ports for unprivileged users.
# The stick's Ignition sets it persistently; this covers a box installed otherwise.
if [ "$(sysctl -n net.ipv4.ip_unprivileged_port_start 2>/dev/null || echo 1024)" -gt 53 ]; then
  printf 'net.ipv4.ip_unprivileged_port_start=0\n' | sudo tee /etc/sysctl.d/90-laib-ports.conf >/dev/null
  sudo sysctl -q -p /etc/sysctl.d/90-laib-ports.conf
fi

# ~/.config must belong to core (Ignition may have created it as root): podman
# refuses to run otherwise, and so would every Quadlet service.
mkdir -p ~/.config/containers/systemd ~/.config/systemd/user 2>/dev/null || true
[ "$(stat -c %U ~/.config)" = core ] || sudo chown -R core:core ~/.config

# Laib's own images (ghcr.io/mdopp/laib-caddy, -frage, -werkstatt) are private
# while the repo is: log podman in with the token so Quadlet and auto-update can
# pull. Needs a token with packages:read; otherwise make the packages public.
# Without --authfile rootless podman writes ${XDG_RUNTIME_DIR}/containers/auth.json —
# a tmpfs, gone at the first reboot, and with it every pull of a private image (#37).
AUTHFILE="$HOME/.config/containers/auth.json"
if [ -n "$TOKEN" ]; then
  mkdir -p "$(dirname "$AUTHFILE")"
  if printf '%s' "$TOKEN" | podman login ghcr.io --authfile "$AUTHFILE" -u mdopp --password-stdin >/dev/null 2>&1; then
    chmod 0600 "$AUTHFILE"
    echo "podman: logged in to ghcr.io; the login is kept in $AUTHFILE (survives reboots; revoke the token to end it)"
  else
    echo "podman login ghcr.io failed: the token lacks packages:read, or the images are public and need none" >&2
  fi
fi

sudo mkdir -p /var/opt/laib
TMPD="$(mktemp -d)"; trap 'rm -rf "$TMPD"' EXIT
for BIN in laib laib-operator laib-lease; do
  curl -fsSL "${get[@]}" -o "$TMPD/$BIN" "$(asset_url "$BIN-linux-amd64")"
done
# SHA256SUMS is required: an unverified build is refused, release or --source (#37)
curl -fsSL "${get[@]}" -o "$TMPD/SHA256SUMS" "$(asset_url SHA256SUMS)" || { echo "no SHA256SUMS for $TAG: refusing an unverified build" >&2; exit 1; }
[ -s "$TMPD/SHA256SUMS" ] || { echo "empty SHA256SUMS for $TAG: refusing an unverified build" >&2; exit 1; }
(cd "$TMPD" && for BIN in laib laib-operator laib-lease; do
   want="$(grep " $BIN-linux-amd64\$" SHA256SUMS | cut -d' ' -f1)"; have="$(sha256sum "$BIN" | cut -d' ' -f1)"
   [ -n "$want" ] || { echo "SHA256SUMS has no entry for $BIN" >&2; exit 1; }
   [ "$want" = "$have" ] || { echo "checksum mismatch for $BIN" >&2; exit 1; }
 done)
# the previous binaries stay as *.prev: `laib self rollback` puts them back
for BIN in laib laib-operator laib-lease; do
  if [ -f "/var/opt/laib/$BIN" ] && ! cmp -s "$TMPD/$BIN" "/var/opt/laib/$BIN"; then sudo cp -p "/var/opt/laib/$BIN" "/var/opt/laib/$BIN.prev"; fi
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
  LAIB_HOME=/mnt/data/laib LAIB_ACTOR=human:core /var/opt/laib/laib init --domain "$DOMAIN" ${LAN_IP:+--lan-ip "$LAN_IP"} ${MODEL_URL:+--model-url "$MODEL_URL"} ${MODEL_LEASE:+--model-lease "$MODEL_LEASE"} ${DNS_PROVIDER:+--dns-provider "$DNS_PROVIDER"} ${TLS:+--tls "$TLS"} $GPU
  if [ -n "$DNS_PROVIDER" ]; then
    [ -n "$DNS_TOKEN" ] || { echo "--dns-provider $DNS_PROVIDER without a token: export LAIB_DNS_TOKEN" >&2; exit 1; }
    printf '%s' "$DNS_TOKEN" | LAIB_HOME=/mnt/data/laib LAIB_ACTOR=human:core /var/opt/laib/laib secret set DNS_API_TOKEN >/dev/null
    echo "DNS_API_TOKEN set for $DNS_PROVIDER (DNS-01 from the first laib add caddy)"
  fi
fi
systemctl --user enable --now laib.service
# enable --now does not restart a running service: an update would keep the old
# code running until the next reboot. try-restart touches only what runs (#37).
for u in laib.service laib-operator.service laib-lease.service; do
  systemctl --user try-restart "$u" 2>/dev/null || true
done
sleep 1
# Laib's own units (converge loop, auto-update verify) come from the state too:
# one apply, so the box is not "eingeschränkt" by its own drift on day one.
LAIB_HOME=/mnt/data/laib LAIB_ACTOR=human:core /var/opt/laib/laib apply >/dev/null || echo "first apply red: /var/opt/laib/laib plan shows why" >&2
echo "installed $TAG: $(/var/opt/laib/laib version)"
# the version the RUNNING service reports, not the file on disk
ready=""
for i in $(seq 1 30); do ready="$(curl -fsS http://127.0.0.1:5900/ready 2>/dev/null || true)"; [ -n "$ready" ] && break; sleep 1; done
echo "ready: ${ready:-(not yet)}"
case "$ready" in
  *'"version":'*)
    grep -q "\"version\":\"$TAG\"" <<<"$ready" || { echo "the running laib is not $TAG (restart failed?)" >&2; exit 1; }
    echo "running: $TAG";;
  *) echo "the running laib predates /ready's version field; the file on disk is $TAG" >&2;;
esac
echo "next: /var/opt/laib/laib plan   (shows every unit on the box as 'nicht verwaltet' until adopted or ignored)"
echo "      /var/opt/laib/laib operator enable   (mints the ops token and starts the operator; needs a model)"
