#!/usr/bin/env bash
# Real-start test of the Werkstatt's home (#241), the boot job's way: no
# shims, a user systemd with Podman's Quadlet generator. The image is built
# from this commit and served from a registry on the runner (apply pulls it,
# as a box pulls ghcr), the template goes in with `laib add`. A session file
# in ~/.claude, an edited dotfile and sshd's host key — checked over SSH with
# the first known_hosts line — must survive a restart by apply, a
# `systemctl --user restart` and an image update; no token is in the home.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="${LAIB_BIN:-$ROOT/laib}"
WORK="$(mktemp -d)"
export LAIB_HOME="$WORK/home"
export LAIB_QUADLET="${LAIB_QUADLET:-$HOME/.config/containers/systemd}"
export LAIB_SYSTEMD="${LAIB_SYSTEMD:-$HOME/.config/systemd/user}"
REG=127.0.0.1:5055
IMG="$REG/laib-werkstatt"
DATA="$WORK/data"
REGCONF="$HOME/.config/containers/registries.conf.d/laib-boot-werkstatt.conf"
cleanup() {
  systemctl --user stop werkstatt.service werkstatt-autoloop.service 2>/dev/null || true
  podman rm -f laib-boot-registry >/dev/null 2>&1 || true
  for s in $(podman secret ls --format '{{.Name}}' 2>/dev/null | grep '^laib\.werkstatt\.' || true); do podman secret rm "$s" >/dev/null || true; done
  rm -rf "$LAIB_QUADLET/werkstatt" "$REGCONF"
  podman unshare rm -rf "$WORK" 2>/dev/null || rm -rf "$WORK"
  systemctl --user daemon-reload 2>/dev/null || true
}
trap cleanup EXIT
fail() { echo "FAIL: $*" >&2; journalctl --user -u werkstatt.service --no-pager | tail -30 >&2; exit 1; }

systemctl --user --version >/dev/null
podman --version
mkdir -p "$LAIB_QUADLET" "$(dirname "$REGCONF")"
printf '[[registry]]\nlocation = "%s"\ninsecure = true\n' "$REG" > "$REGCONF"
podman run -d --name laib-boot-registry -p "$REG:5000" docker.io/library/registry:2 >/dev/null
podman build -q -t "$IMG:1" "$ROOT/werkstatt" >/dev/null
mkdir -p "$WORK/v2"
# a rebuilt image is a new filesystem: here also new host keys in /etc/ssh, as a fresh build of the old image had
printf 'FROM %s:1\nRUN echo v2 > /etc/werkstatt-image && rm -f /etc/ssh/ssh_host_* && ssh-keygen -A\n' "$IMG" > "$WORK/v2/Containerfile"
podman build -q -t "$IMG:2" "$WORK/v2" >/dev/null
for t in 1 2; do podman push -q "$IMG:$t"; done
podman rmi "$IMG:2" >/dev/null   # the update is pulled, as on a box

LAIB_ACTOR=human:boot "$BIN" init --domain boot.test --data-dir "$DATA" >/dev/null
cat > "$LAIB_HOME/state/box.yml" <<YML
domain: boot.test
dataDir: $DATA
roles: {}
gpu: {present: false}
laib: {listen: 0.0.0.0}
YML
# the external secrets, each a canary the home must never contain
for s in CLAUDE_CODE_OAUTH_TOKEN LAIB_CLAUDE_TOKEN LAIB_PI_TOKEN WERKSTATT_GIT_TOKEN WERKSTATT_PI_GIT_TOKEN; do
  printf 'canary241-%s' "$s" | LAIB_ACTOR=human:boot "$BIN" secret set "$s" >/dev/null
done
LAIB_ACTOR=human:boot "$BIN" add werkstatt IMAGE="$IMG:1" "boot test #241" || fail "laib add exit $?"

ssh-keygen -q -t ed25519 -N '' -f "$WORK/id"
sed -i "s|^AUTHORIZED_KEY:.*|AUTHORIZED_KEY: \"$(cat "$WORK/id.pub")\"|" "$LAIB_HOME/state/services/werkstatt/values.yml"
SSHO=(-p 2222 -i "$WORK/id" -o BatchMode=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile="$WORK/known_hosts" -o ConnectTimeout=5)
waitSSH() {
  for _ in $(seq 60); do ssh-keyscan -p 2222 -t ed25519 127.0.0.1 2>/dev/null | grep -q ssh-ed25519 && return 0; sleep 2; done
  fail "sshd did not answer on 2222"
}
waitSSH
ssh-keyscan -p 2222 -t ed25519 127.0.0.1 2>/dev/null > "$WORK/known_hosts"
podman exec -u dev systemd-werkstatt bash -c 'test -f ~/.bashrc' || fail "the image's dotfiles were not seeded into the empty home"
podman exec -u dev systemd-werkstatt bash -c 'mkdir -p ~/.claude/projects/-workspace && echo "{\"session\":241}" > ~/.claude/projects/-workspace/s.jsonl && echo "# mine 241" >> ~/.bashrc && mkdir -p ~/.cache && echo x > ~/.cache/scratch'

kept() {   # $1: what happened since the files were written
  local id; id="$(podman inspect systemd-werkstatt --format '{{.Id}}')"
  [ "$id" != "$LAST" ] || fail "$1: the container was not recreated, so this proves nothing"
  LAST="$id"
  waitSSH
  ssh "${SSHO[@]}" dev@127.0.0.1 true || fail "$1: SSH refused with the first host key (the key changed, or the login broke)"
  [ "$(ssh "${SSHO[@]}" dev@127.0.0.1 cat .claude/projects/-workspace/s.jsonl)" = '{"session":241}' ] || fail "$1: the Claude session in ~/.claude is gone"
  [ "$(ssh "${SSHO[@]}" dev@127.0.0.1 tail -1 .bashrc)" = "# mine 241" ] || fail "$1: the edited ~/.bashrc is gone"
  echo "kept after $1"
}
LAST="$(podman inspect systemd-werkstatt --format '{{.Id}}')"
LAIB_ACTOR=human:boot "$BIN" apply --reason "authorized key" >/dev/null || fail "apply exit $?"
kept "a restart by apply"
systemctl --user restart werkstatt.service
kept "systemctl --user restart"
sed -i "s|^IMAGE:.*|IMAGE: $IMG:2|" "$LAIB_HOME/state/services/werkstatt/values.yml"
LAIB_ACTOR=human:boot "$BIN" apply --reason "image update" >/dev/null || fail "apply exit $?"
kept "an image update"
[ "$(ssh "${SSHO[@]}" dev@127.0.0.1 cat /etc/werkstatt-image)" = v2 ] || fail "the container does not run the updated image"

# caches are a data path of their own (backup: false), not inside the home's
podman unshare test -f "$DATA/werkstatt/cache/scratch" || fail "~/.cache is not the cache data path"
podman unshare test ! -e "$DATA/werkstatt/home/.cache/scratch" || fail "~/.cache landed in the backed-up home"
# no token in the home or the host keys; Claude Code's MCP header names the variable
if podman unshare grep -rlF canary241 "$DATA/werkstatt/home" "$DATA/werkstatt/ssh-host-keys"; then fail "a token is in the persistent home"; fi
podman unshare grep -qF 'Bearer ${LAIB_TOKEN}' "$DATA/werkstatt/home/.claude.json" || fail "laib is not Claude Code's MCP server in ~/.claude.json"
# the tokens' tmpfs (#44) is really one
podman exec systemd-werkstatt grep -q ' /run/laib tmpfs ' /proc/mounts || fail "/run/laib, where the tokens go, is not a tmpfs (#44)"
echo "werkstatt boot test ok: the home, the session and the host key survived a restart and an image update"
