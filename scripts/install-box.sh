#!/usr/bin/env bash
# Install or update the Laib binaries and the user unit on this box. Idempotent.
# Changes nothing about services: after this, `laib plan` shows what would.
#
#   curl … install-box.sh | bash -s -- --domain example.home [--lan-ip <ip>] \
#       [--gpu | --model-url http://<gpu-box>:11435/v1 [--model-lease <url>]] \
#       [--version vX.Y.Z]
#       [--dns-provider desec|cloudflare|…  (token: LAIB_DNS_TOKEN)] [--tls internal]
#       [--source <base url>]   one build served over HTTP instead of a release (the VM job, #38)
#   … | bash -s -- --box-config  only the box's own config below (journald, podman's log driver), then exit
#
# mdopp/laib, its releases and its images are public: no token. A private fork
# or mirror exports LAIB_GH_TOKEN (Contents: read, Packages: read); never as a
# flag: ps shows every argv on the box to every local user (#217).
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
    --token) echo "--token puts the token on argv, where ps shows it (#217): export LAIB_GH_TOKEN instead" >&2; exit 1;;
    --dns-provider) DNS_PROVIDER="$2"; shift 2;;
    --tls) TLS="$2"; shift 2;;
    --box-config) BOXCONF=1; shift;;
    *) echo "unknown flag $1" >&2; exit 1;;
  esac
done

# The box's own config (#257, #281), one source for every box: the stick's
# first boot runs this script, a migrated box runs it in step 1
# (docs/MIGRATION.md); laib diag is red while either file is missing.
# journald: persistent (the boot before a crash is the one to read), 4G and
# four weeks, whichever comes first. Four weeks answers "since when" for
# anything the family reports; 4G holds that at up to ~145 MB a day of the
# services' output (laib's own journal is a file on /mnt/data) and is a small
# share of the OS disk (the NUC's is 256G; journald still keeps SystemKeepFree,
# 15 %, free on a small disk). A noisier box loses days, not disk. Weekly
# files, so the age limit drops a week at a time instead of a month.
# podman: log driver journald, so a container's output is in its unit's
# journal (laib logs) and podman logs reads it; Quadlet passes no --log-driver
# unless a unit says LogDriver=. A drop-in, so a containers.conf already on
# the box (ServiceBay's) stays as it is.
box_config() {
  local etc="${LAIB_ETC:-/etc}" s="" conf want   # LAIB_ETC, LAIB_QUADLET: the test harness only
  [ "$etc" = /etc ] && s=sudo
  want="$(printf '[Journal]\nStorage=persistent\nSystemMaxUse=4G\nMaxRetentionSec=4week\nMaxFileSec=1week')"
  if [ "$(cat "$etc/systemd/journald.conf.d/50-laib.conf" 2>/dev/null)" != "$want" ]; then
    $s mkdir -p "$etc/systemd/journald.conf.d"
    printf '%s\n' "$want" | $s tee "$etc/systemd/journald.conf.d/50-laib.conf" >/dev/null
    [ -z "$s" ] || sudo systemctl restart systemd-journald   # it reads its config only at start
    echo "journald: persistent, at most 4G and four weeks"
  fi
  conf="$(dirname "${LAIB_QUADLET:-$HOME/.config/containers/systemd}")/containers.conf.d"
  mkdir -p "$conf" && printf '[containers]\nlog_driver = "journald"\n' > "$conf/50-laib.conf"
}
[ -z "${BOXCONF:-}" ] || { box_config; exit 0; }
[ "$(id -un)" = "core" ] || { echo "run as the core user (rootless podman, user systemd)" >&2; exit 1; }
REPO="mdopp/laib"
API="https://api.github.com/repos/$REPO"

# Resolve what to install: a release (its tag and the API url of every asset),
# or one build served at --source under the release's asset names plus VERSION.
TMPD="$(mktemp -d)"; trap 'rm -rf "$TMPD"' EXIT
# The token reaches curl as a header file only core can read, never on its argv (#217).
auth=()
if [ -n "$TOKEN" ]; then
  (umask 077; printf 'Authorization: Bearer %s\n' "$TOKEN" > "$TMPD/auth")
  auth=(-H "@$TMPD/auth")
fi
if [ -n "$SOURCE" ]; then
  TAG="$(curl -fsSL "$SOURCE/VERSION")" || { echo "cannot read $SOURCE/VERSION" >&2; exit 1; }
  [ -n "$TAG" ] || { echo "$SOURCE/VERSION is empty" >&2; exit 1; }
  get=(-H "Accept: application/octet-stream")   # the GitHub token never goes to a build server
  asset_url() { printf '%s/%s' "$SOURCE" "$1"; }
else
  api() { curl -fsSL "${auth[@]}" -H "Accept: application/vnd.github+json" "$API/$1"; }
  # No python3 on Fedora CoreOS: pick the fields out of the JSON with sed. An
  # asset object starts with its API "url" and carries "name" before any nested
  # object, so url..name within one object is a match without a brace in between.
  use_release() {
    FLAT="$(printf '%s' "$1" | tr -d '\n')"
    TAG="$(printf '%s' "$FLAT" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p')"
    [ -n "$TAG" ]   # empty: the API call failed, or the JSON has no tag_name
  }
  asset_url() {
    local u
    u="$(printf '%s' "$FLAT" | grep -o '"url": *"[^"]*/releases/assets/[0-9]*"[^{}]*"name": *"'"$1"'"' | head -1 | sed -n 's/^"url": *"\([^"]*\)".*/\1/p')"
    [ -n "$u" ] || { echo "release $TAG has no asset $1" >&2; return 1; }
    printf '%s' "$u"
  }
  if [ "$VERSION" = "latest" ]; then REL="releases/latest"; else REL="releases/tags/$VERSION"; fi
  use_release "$(api "$REL")" || { echo "cannot read release $VERSION (private repo? export LAIB_GH_TOKEN)" >&2; exit 1; }
  # A release is published before its binaries: they are built once vm-install
  # is green on it (#47), a VM run later. Until then "latest" means the newest
  # release that has them (#99).
  if [ "$VERSION" = "latest" ] && ! asset_url laib-linux-amd64 >/dev/null 2>&1; then
    skipped="$TAG"
    for t in $(api "releases?per_page=20" | tr -d '\n' | grep -o '"tag_name": *"[^"]*"' | sed 's/.*"\([^"]*\)"$/\1/'); do
      [ "$t" = "$skipped" ] && continue
      use_release "$(api "releases/tags/$t")" || continue
      grep -q '"draft": *false.*"prerelease": *false' <<<"$(printf '%s' "$FLAT")" || continue
      asset_url laib-linux-amd64 >/dev/null 2>&1 && break
    done
    asset_url laib-linux-amd64 >/dev/null 2>&1 || { echo "no release has its binaries yet (newest: $skipped)" >&2; exit 1; }
    echo "$skipped has no binaries yet (they follow its VM test); installing $TAG, the newest release that has them"
  fi
  get=("${auth[@]}" -H "Accept: application/octet-stream")
fi

# laib shells out to git, diff and restic (state repo, plan diffs, backups).
# git and diff are in the CoreOS image (git-core, diffutils); restic is not.
# Installing Laib is not an OS update (#367): no rpm-ostree layering, which
# re-resolves every layered package (98 packages on the Kraftwerk, its NVIDIA
# stack among them) and on a box with a staged deployment needs a reboot. So
# restic is restic's own static release, pinned by its SHA256 like Laib's
# binaries, into /usr/local/bin (/var/usrlocal on CoreOS: no deployment).
# A bump changes the version and both sums (restic's SHA256SUMS has the first).
RESTIC_VERSION=0.19.1
RESTIC_BZ2_SHA256=f415415624dcc452f2a02b8c33641791a8c6d6d3b65bbb3543fcf9a25151585c
RESTIC_SHA256=20d4142678d0d95ec11a4759def1b73fd9190abc9ca19e4b62d067c0b387e639
RESTIC_BIN="${LAIB_RESTIC_BIN:-/usr/local/bin/restic}"   # LAIB_RESTIC_BIN: the test harness only
for t in git diff; do
  command -v "$t" >/dev/null 2>&1 || { echo "$t missing: it is in every current CoreOS image; this script does not change the OS (#367)" >&2; exit 1; }
done
if [ "$(sha256sum "$RESTIC_BIN" 2>/dev/null | cut -d' ' -f1)" != "$RESTIC_SHA256" ]; then
  url="https://github.com/restic/restic/releases/download/v$RESTIC_VERSION/restic_${RESTIC_VERSION}_linux_amd64.bz2"
  if ! curl -fsSL -o "$TMPD/restic.bz2" "$url"; then
    command -v restic >/dev/null 2>&1 || { echo "cannot download restic $RESTIC_VERSION ($url); laib's backups need it" >&2; exit 1; }
    echo "cannot download restic $RESTIC_VERSION; keeping $(command -v restic) ($(restic version 2>/dev/null | cut -d' ' -f2))" >&2
  else
    [ "$(sha256sum "$TMPD/restic.bz2" | cut -d' ' -f1)" = "$RESTIC_BZ2_SHA256" ] || { echo "checksum mismatch for restic $RESTIC_VERSION: refusing an unverified restic" >&2; exit 1; }
    bunzip2 -c "$TMPD/restic.bz2" > "$TMPD/restic"
    [ "$(sha256sum "$TMPD/restic" | cut -d' ' -f1)" = "$RESTIC_SHA256" ] || { echo "checksum mismatch for the unpacked restic $RESTIC_VERSION: refusing it" >&2; exit 1; }
    sudo install -D -m 0755 "$TMPD/restic" "$RESTIC_BIN.new" && sudo mv "$RESTIC_BIN.new" "$RESTIC_BIN"
    echo "restic $RESTIC_VERSION in $RESTIC_BIN (checksum-verified)"
  fi
fi

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
box_config

# Laib's own images (ghcr.io/mdopp/laib-*) are public. A private fork's images
# are not: with a token, podman logs in so Quadlet and auto-update can pull.
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
# vm-install cuts the power on this line (the first boot's console): the
# binaries are in place, the first boot's marker is not (#37, #367)
echo "binaries $TAG in /var/opt/laib"
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
