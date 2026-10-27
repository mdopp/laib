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
STATE="${LAIB_STATE:-/mnt/data/laib/state}"   # LAIB_STATE: the test harness only
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

# The booted deployment's container image (#486): "<reference> <digest>",
# empty on a box without rpm-ostree or one booted from a plain ostree commit.
os_image() {
  rpm-ostree status --json 2>/dev/null | jq -r '.deployments[] | select(.booted) | select(."container-image-reference") | "\(."container-image-reference") \(."container-image-reference-digest" // "")"' 2>/dev/null || true
}

# The box's own config (#257, #281), one source for every box: the stick's
# first boot runs this script, a migrated box ran it once (--box-config);
# laib diag is red while either file is missing.
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
  # OS updates (#368): Zincati stages an update and asks laib before it
  # reboots (fleet_lock): laib grants it in the window after the backup,
  # green and proven. 60- wins over ServiceBay's 55-servicebay-lock.toml;
  # the pause files (90-, 95-: updates.enabled = false) still switch it off.
  # #486: a box booted from an image Fedora's update graph does not know
  # (uCore) gets no update from Zincati, only "not found in the update
  # graph" at every poll, and rpm-ostree's own timer is a second driver that
  # ignores the pin and the pauses. There laib-host-image is the one update
  # path (state/host/image, the reboot through the same lock): both are
  # masked, so no image brings them back. The pause files stay the one
  # pause; laib-host-image reads them as Zincati does.
  case "$(os_image)" in
    "" | *quay.io/fedora/fedora-coreos*)
      if [ -e /usr/lib/zincati ] || [ -d "$etc/zincati" ]; then
        $s mkdir -p "$etc/zincati/config.d"
        printf '[updates]\nstrategy = "fleet_lock"\n\n[updates.fleet_lock]\nbase_url = "http://127.0.0.1:5900/"\n' | $s tee "$etc/zincati/config.d/60-laib-updates.toml" >/dev/null
        [ -z "$s" ] || sudo systemctl try-restart zincati.service
      fi ;;
    *)
      for u in zincati.service rpm-ostreed-automatic.timer; do
        [ -n "$s" ] || break
        case "$(systemctl is-enabled "$u" 2>/dev/null || true)" in
          masked* | "") ;;
          *) sudo systemctl mask --now "$u" >/dev/null && echo "$u: masked (this image's OS updates come from laib-host-image, #486)" ;;
        esac
      done ;;
  esac
  # One update path for the images (#528, ADR 0002 amendment): laib-images.timer
  # (laib updates --run) pulls, verifies and rolls back under laib's lock.
  # podman's own timer would restart units beside it and judge an image only
  # by its start: masked, so no image brings it back.
  case "$(systemctl --user is-enabled podman-auto-update.timer 2>/dev/null || true)" in
    masked*) ;;
    *) systemctl --user mask --now podman-auto-update.timer >/dev/null 2>&1 && echo "podman-auto-update.timer: masked (laib-images.timer updates the images, #528)" ;;
  esac
  # One firewall mechanism (#445): Laib's nftables table (inet laib). uCore
  # enables firewalld (zone FedoraServer: ssh only), which blocked the
  # household from every port but SSH; masked, so no image brings it back.
  if [ -n "$s" ] && { systemctl is-active --quiet firewalld.service || systemctl is-enabled --quiet firewalld.service; } 2>/dev/null; then
    sudo systemctl mask --now firewalld.service >/dev/null && echo "firewalld: masked and stopped (Laib's nftables table is the one firewall, #445)"
  fi
}

# OS updates without fear (#368). laib-cdi.service writes the CDI spec at
# every boot, so a new driver never leaves the GPU services with a spec that
# names the old one (ServiceBay generated it once, behind a done-marker).
# laib-boot-check.service runs scripts/boot-check.sh, which proves a new
# deployment or rolls it back once and reboots: that reboot is the boot
# check's own, never this script's (#367). The check is a release asset,
# verified against SHA256SUMS like the binaries, so it is written by the
# install, not by --box-config. host_units <etc> <sudo or ""> <boot-check.sh>
host_units() {
  local etc="$1" s="$2" d="$1/systemd/system"
  $s mkdir -p "$d" "$etc/laib"
  $s tee "$d/laib-cdi.service" >/dev/null <<'UNIT'
[Unit]
Description=Laib: the NVIDIA CDI spec, regenerated at every boot (#368)
ConditionPathExists=/usr/bin/nvidia-ctk
After=systemd-modules-load.service
Before=user@1000.service laib-boot-check.service

[Service]
Type=oneshot
ExecStartPre=-/usr/sbin/modprobe nvidia
ExecStart=/usr/bin/nvidia-ctk cdi generate --output=/etc/cdi/nvidia.yaml
TimeoutStartSec=120

[Install]
WantedBy=multi-user.target
UNIT
  # Type=exec: the check waits up to 15 minutes and must not hold up the boot
  $s tee "$d/laib-boot-check.service" >/dev/null <<'UNIT'
[Unit]
Description=Laib: prove this OS deployment, or roll it back once (#368)
After=laib-cdi.service network-online.target
Wants=network-online.target

[Service]
Type=exec
ExecStart=/bin/bash /etc/laib/boot-check.sh

[Install]
WantedBy=multi-user.target
UNIT
  # the boot check itself: the release's laib-boot-check.sh, verified with the binaries
  $s install -m 0755 "$3" "$etc/laib/boot-check.sh"
  [ -z "$s" ] || { sudo systemctl daemon-reload; sudo systemctl enable laib-cdi.service laib-boot-check.service >/dev/null; }
  [ -n "${4:-}" ] || return 0
  # the host OS follows state/host/image (#368): staged by rpm-ostree, the
  # reboot through laib's lock; scripts/host-image.sh, verified the same way
  $s tee "$d/laib-host-image.service" >/dev/null <<'UNIT'
[Unit]
Description=Laib: the host OS follows the image pinned in the state (#368)
ConditionPathExists=/mnt/data/laib/state/host/image
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=/bin/bash /etc/laib/host-image.sh
TimeoutStartSec=2h
UNIT
  $s tee "$d/laib-host-image.timer" >/dev/null <<'UNIT'
[Unit]
Description=Laib: look at the pinned host image every 15 minutes (#368)

[Timer]
OnBootSec=20min
OnUnitActiveSec=15min

[Install]
WantedBy=timers.target
UNIT
  $s install -m 0755 "$4" "$etc/laib/host-image.sh"
  [ -z "$s" ] || { sudo systemctl daemon-reload; sudo systemctl enable --now laib-host-image.timer >/dev/null; }
}
[ -z "${BOXCONF:-}" ] || { box_config; exit 0; }
[ "$(id -un)" = "core" ] || { echo "run as the core user (rootless podman, user systemd)" >&2; exit 1; }
# mark: a line vm-install cuts the power on. It goes to the journal and, via
# /dev/kmsg, to the console in one piece: the journal's console copy can be
# split by the serial getty's terminal reset (train 22: "binaries … in /var^[[!p…").
mark() { echo "$1"; printf '<5>laib: %s\n' "$1" | sudo tee /dev/kmsg >/dev/null 2>&1 || true; }
# vm-install cuts the power on this line (the first boot's console), before
# restic and the binaries: the resumed first boot starts over (#111)
mark "install-box.sh: start"
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
# laib-operator, laib-notify, laib-haushalt and laib-lease are containers
# since ADR 0027: their images are pinned by laib and pulled by its apply.
# A binary of theirs from an older release stays where it is: a self
# rollback to that release runs it.
for BIN in laib; do
  curl -fsSL "${get[@]}" -o "$TMPD/$BIN" "$(asset_url "$BIN-linux-amd64")"
done
# SHA256SUMS is required: an unverified build is refused, release or --source (#37)
curl -fsSL "${get[@]}" -o "$TMPD/SHA256SUMS" "$(asset_url SHA256SUMS)" || { echo "no SHA256SUMS for $TAG: refusing an unverified build" >&2; exit 1; }
[ -s "$TMPD/SHA256SUMS" ] || { echo "empty SHA256SUMS for $TAG: refusing an unverified build" >&2; exit 1; }
# The boot check (#368) is part of a release when SHA256SUMS lists it; then it
# must download and match. A release from before #368 has none: its binaries
# install, and laib diag's boot-check probe stays red as the reminder.
CHECK=""
if grep -q " laib-boot-check.sh\$" "$TMPD/SHA256SUMS"; then
  CHECK="$TMPD/laib-boot-check.sh"
  u="$(asset_url laib-boot-check.sh)" && curl -fsSL "${get[@]}" -o "$CHECK" "$u" || { echo "$TAG lists laib-boot-check.sh but it does not download: refusing" >&2; exit 1; }
else
  echo "$TAG carries no boot check (laib-boot-check.sh, #368): installing without it; laib diag shows it missing"
fi
FOLLOW=""   # the host image follower (#368), only with the boot check that guards it
if [ -n "$CHECK" ] && grep -q " laib-host-image.sh\$" "$TMPD/SHA256SUMS"; then
  FOLLOW="$TMPD/laib-host-image.sh"
  u="$(asset_url laib-host-image.sh)" && curl -fsSL "${get[@]}" -o "$FOLLOW" "$u" || { echo "$TAG lists laib-host-image.sh but it does not download: refusing" >&2; exit 1; }
fi
(cd "$TMPD" && for BIN in laib; do
   want="$(grep " $BIN-linux-amd64\$" SHA256SUMS | cut -d' ' -f1)"; have="$(sha256sum "$BIN" | cut -d' ' -f1)"
   [ -n "$want" ] || { echo "SHA256SUMS has no entry for $BIN" >&2; exit 1; }
   [ "$want" = "$have" ] || { echo "checksum mismatch for $BIN" >&2; exit 1; }
 done
 [ -z "$CHECK" ] || [ "$(grep " laib-boot-check.sh\$" SHA256SUMS | cut -d' ' -f1)" = "$(sha256sum laib-boot-check.sh | cut -d' ' -f1)" ] || { echo "checksum mismatch for laib-boot-check.sh" >&2; exit 1; }
 [ -z "$FOLLOW" ] || [ "$(grep " laib-host-image.sh\$" SHA256SUMS | cut -d' ' -f1)" = "$(sha256sum laib-host-image.sh | cut -d' ' -f1)" ] || { echo "checksum mismatch for laib-host-image.sh" >&2; exit 1; })
# the program out of the kernel that stays on the host (#443, ADR 0027):
# laib-disk, the data RAID and disk health. A release from before has it
# not; then laib diag says so
EXTRA=""
for P in laib-disk; do
  if grep -q " $P-linux-amd64\$" "$TMPD/SHA256SUMS"; then
    curl -fsSL "${get[@]}" -o "$TMPD/$P" "$(asset_url "$P-linux-amd64")" || { echo "$TAG lists $P but it does not download: refusing" >&2; exit 1; }
    [ "$(grep " $P-linux-amd64\$" "$TMPD/SHA256SUMS" | cut -d' ' -f1)" = "$(sha256sum "$TMPD/$P" | cut -d' ' -f1)" ] || { echo "checksum mismatch for $P" >&2; exit 1; }
    EXTRA="$EXTRA $P"
  else
    echo "$TAG carries no $P (#443): installing without it"
  fi
done
# the previous binaries stay as *.prev: `laib self rollback` puts them back
for BIN in laib $EXTRA; do
  if [ -f "/var/opt/laib/$BIN" ] && ! cmp -s "$TMPD/$BIN" "/var/opt/laib/$BIN"; then sudo cp -p "/var/opt/laib/$BIN" "/var/opt/laib/$BIN.prev"; fi
  sudo install -m 0755 "$TMPD/$BIN" "/var/opt/laib/$BIN.new" && sudo mv "/var/opt/laib/$BIN.new" "/var/opt/laib/$BIN"
done
# Laib's own update (#547): laib-images.service runs it before the nightly
# image run. A release from before has none; then laib diag says so
if grep -q " laib-self-update.sh\$" "$TMPD/SHA256SUMS"; then
  curl -fsSL "${get[@]}" -o "$TMPD/self-update.sh" "$(asset_url laib-self-update.sh)" || { echo "$TAG lists laib-self-update.sh but it does not download: refusing" >&2; exit 1; }
  [ "$(grep " laib-self-update.sh\$" "$TMPD/SHA256SUMS" | cut -d' ' -f1)" = "$(sha256sum "$TMPD/self-update.sh" | cut -d' ' -f1)" ] || { echo "checksum mismatch for laib-self-update.sh" >&2; exit 1; }
  sudo install -m 0755 "$TMPD/self-update.sh" /var/opt/laib/self-update.sh.new && sudo mv /var/opt/laib/self-update.sh.new /var/opt/laib/self-update.sh
fi
if [ -n "$CHECK" ]; then
  if [ -z "${LAIB_ETC:-}" ]; then host_units /etc sudo "$CHECK" "$FOLLOW"; else host_units "$LAIB_ETC" "" "$CHECK" "$FOLLOW"; fi   # LAIB_ETC: the test harness only
fi
# vm-install cuts the power on this line (the first boot's console): the
# binaries are in place, the first boot's marker is not (#37, #367)
mark "binaries $TAG in /var/opt/laib"
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

if [ -n "$DOMAIN" ] && [ ! -f "$STATE/box.yml" ]; then
  LAIB_HOME=/mnt/data/laib LAIB_ACTOR=human:core /var/opt/laib/laib init --domain "$DOMAIN" ${LAN_IP:+--lan-ip "$LAN_IP"} ${MODEL_URL:+--model-url "$MODEL_URL"} ${MODEL_LEASE:+--model-lease "$MODEL_LEASE"} ${DNS_PROVIDER:+--dns-provider "$DNS_PROVIDER"} ${TLS:+--tls "$TLS"} $GPU
  if [ -n "$DNS_PROVIDER" ]; then
    [ -n "$DNS_TOKEN" ] || { echo "--dns-provider $DNS_PROVIDER without a token: export LAIB_DNS_TOKEN" >&2; exit 1; }
    printf '%s' "$DNS_TOKEN" | LAIB_HOME=/mnt/data/laib LAIB_ACTOR=human:core /var/opt/laib/laib secret set DNS_API_TOKEN >/dev/null
    echo "DNS_API_TOKEN set for $DNS_PROVIDER (DNS-01 from the first laib add caddy)"
  fi
fi
# The host OS follows state/host/image (#486). On a box booted from an image
# Zincati does not serve, a state without a pin gets the booted one: the tag
# it was rebased to, and the digest it runs. Booted by digest, the tag it
# follows is unknown, and the pin is the owner's (docs/INSTALL.md).
pin_host_image() {
  local img ref dig name tag
  img="$(os_image)"; ref="${img%% *}"; dig="${img#* }"
  case "$ref" in "" | *quay.io/fedora/fedora-coreos*) return 0 ;; esac
  [ -d "$STATE" ] && [ ! -e "$STATE/host/image" ] || return 0
  if [[ "$ref" == *@* ]] || [ -z "$dig" ]; then
    echo "host/image: not pinned: the box runs $ref, and the tag it follows is not known; pin it by hand (docs/INSTALL.md, OS updates)" >&2; return 0
  fi
  tag="${ref##*:}"; name="${ref%:*}"
  case "$tag" in */*) tag=latest; name="$ref" ;; esac
  mkdir -p "$STATE/host" && printf '# tag: %s\n%s@%s\n' "$tag" "$name" "$dig" >"$STATE/host/image"
  git -C "$STATE" add host/image && git -C "$STATE" -c user.name=laib -c user.email=laib@localhost commit -qm "host: pin the booted image (#486)" -- host/image >/dev/null ||
    echo "host/image written but not committed: git -C $STATE status" >&2
  echo "host/image: pinned $name@$dig (tag $tag): laib-host-image follows it, laib updates shows a newer digest"
}
pin_host_image
systemctl --user enable --now laib.service
# enable --now does not restart a running service: an update would keep the old
# code running until the next reboot. try-restart touches only what runs (#37).
# (the containers of laib's own programs restart with the apply below when their images moved, ADR 0027)
systemctl --user try-restart laib.service 2>/dev/null || true
sleep 1
# Laib's own units (converge loop, auto-update verify) come from the state too:
# one apply, so the box is not "eingeschränkt" by its own drift on day one.
# A self-update (#547, self-update.sh) has no human to act for: laib's own
# converge brings the box to the state the new release renders.
if [ -n "${LAIB_SELF_UPDATE:-}" ]; then
  LAIB_HOME=/mnt/data/laib LAIB_ACTOR=laib /var/opt/laib/laib converge --force >/dev/null || echo "converge red: /var/opt/laib/laib plan shows why" >&2
else
  LAIB_HOME=/mnt/data/laib LAIB_ACTOR=human:core /var/opt/laib/laib apply >/dev/null || echo "first apply red: /var/opt/laib/laib plan shows why" >&2
fi
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
echo "next: /var/opt/laib/laib plan   (shows every unit on the box as 'nicht verwaltet' until ignored or brought back as a template)"
echo "      /var/opt/laib/laib operator enable   (mints the ops token and starts the operator; needs a model)"
