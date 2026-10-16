#!/usr/bin/env bash
# On the VM, as core: ServiceBay as its own stick leaves it on a box
# (mdopp/servicebay tools/sb/internal/build/assets/fedora-coreos.bu): python3
# layered for its agent (install-python.sh), secret.key and AUTH_SECRET
# (servicebay-{secret-key,auth-secret}-init.service), the agent's SSH key in
# core's authorized_keys and nodes.json, and servicebay.container with the
# [Container] lines of the stick's Quadlet; the UserNS= line is what
# servicebay-userns-selfheal.sh derives from the image's USER. Then it waits
# until ServiceBay answers and its agent is connected.
#
# Usage: servicebay-box.sh <image> <domain> <admin user>   (the admin password on stdin)
set -euo pipefail
IMAGE=${1:?image}; DOMAIN=${2:?domain}; ADMIN=${3:?admin}
read -r PASS
D=/mnt/data/servicebay
[ "$(stat -c %U /var/mnt/data)" = core ] || sudo chown core:core /var/mnt/data
sudo chcon -R -t container_file_t /var/mnt/data/containers   # setup-raid.sh does the same
command -v python3 >/dev/null || sudo rpm-ostree install --apply-live --allow-inactive -q python3
python3 --version

umask 077
[ -s $D/secret.key ] || openssl rand 32 > $D/secret.key
[ -s $D/.auth-secret.env ] || printf 'AUTH_SECRET=%s\n' "$(openssl rand -hex 32)" > $D/.auth-secret.env
[ -s $D/ssh/id_rsa ] || ssh-keygen -q -t rsa -b 4096 -N "" -f $D/ssh/id_rsa -C servicebay
grep -qF "$(cut -d' ' -f2 $D/ssh/id_rsa.pub)" ~/.ssh/authorized_keys 2>/dev/null || cat $D/ssh/id_rsa.pub >> ~/.ssh/authorized_keys
[ -s $D/nodes.json ] || printf '[{"Name":"Local","URI":"ssh://core@127.0.0.1","Identity":"/app/data/ssh/id_rsa","Default":true}]\n' > $D/nodes.json
# config.json as the stick's builder writes it (tools/sb/internal/build/config.go),
# plus the external registry the box has (paperless lives in solarisbay) and the
# operator's mail (the auth and immich post-deploys read it); no self-update mid-run
[ -s $D/config.json ] || python3 -c '
import json, sys
u, d = sys.argv[1:]
print(json.dumps({"serverName": "kraftwerk", "auth": {"username": u}, "autoUpdate": {"enabled": False, "schedule": "0 0 * * *"},
  "templateSettings": {"DATA_DIR": "/mnt/data/stacks"}, "setupCompleted": True, "stackSetupPending": True,
  "reverseProxy": {"publicDomain": d},
  "registries": {"enabled": True, "items": [{"name": "solarisbay", "url": "https://github.com/mdopp/solarisbay"}]},
  "notifications": {"email": {"enabled": False, "to": ["admin@" + d]}}}, indent=2))' "$ADMIN" "$DOMAIN" > $D/config.json
umask 022
# the box's names resolve to the box (on the Kraftwerk: public DNS and AdGuard's rewrites)
for h in auth ldap home zwave photos paperless vault caldav files sync nginx admin laib; do
  grep -q " $h.$DOMAIN\$" /etc/hosts || echo "10.0.2.15 $h.$DOMAIN" | sudo tee -a /etc/hosts >/dev/null
done

podman pull -q "$IMAGE" >/dev/null
spec=$(podman image inspect "$IMAGE" --format '{{.Config.User}}' | tr -d '[:space:]')
userns=""
case "$spec" in ''|root|root:root|0|0:0) ;;
  *) u=${spec%%:*}; g=${spec#*:}; [ "$g" = "$spec" ] && g=$u
     case "$u" in *[!0-9]*) u=$(podman run --rm --entrypoint id "$IMAGE" -u); g=$(podman run --rm --entrypoint id "$IMAGE" -g);; esac
     userns="UserNS=keep-id:uid=$u,gid=$g";;
esac
echo "image user: ${spec:-root} ${userns:+-> $userns}"
# the admin password goes in as the stick writes it, in the unit (core-only here)
cat > ~/.config/containers/systemd/servicebay.container <<UNIT
[Unit]
Description=ServiceBay Rootless Management Interface
After=network-online.target

[Container]
Image=$IMAGE
ContainerName=servicebay
${userns}
Network=host
Volume=/run/user/1000/podman/podman.sock:/run/podman/podman.sock
Environment=CONTAINER_HOST=unix:///run/podman/podman.sock
Volume=/mnt/data/servicebay:/app/data:Z
Environment=HOST_DATA_DIR=/mnt/data/servicebay
Environment=PORT=5888
Environment=NODE_ENV=production
Environment=HOST_USER=core
EnvironmentFile=/mnt/data/servicebay/.auth-secret.env
Environment=SERVICEBAY_USERNAME=$ADMIN
Environment=SERVICEBAY_PASSWORD=$PASS
SecurityLabelDisable=true

[Service]
Restart=always
RestartSec=5

[Install]
WantedBy=default.target
UNIT
chmod 600 ~/.config/containers/systemd/servicebay.container
systemctl --user enable --now podman.socket
systemctl --user daemon-reload
systemctl --user start servicebay.service
for i in $(seq 1 90); do curl -fsS -o /dev/null http://127.0.0.1:5888/login 2>/dev/null && break; sleep 2; done
curl -fsS -o /dev/null http://127.0.0.1:5888/login || { echo "ServiceBay does not answer on 5888"; podman logs --tail 60 servicebay; exit 1; }
echo "ServiceBay answers on 5888"
for i in $(seq 1 90); do curl -fsS http://127.0.0.1:5888/api/system/health 2>/dev/null | grep -q '"isConnected":true' && break; sleep 2; done
curl -fsS http://127.0.0.1:5888/api/system/health | grep -q '"isConnected":true' || { echo "ServiceBay's agent never connected"; curl -sS http://127.0.0.1:5888/api/system/health; podman logs --tail 60 servicebay; exit 1; }
echo "ServiceBay's agent is connected"
