#!/usr/bin/env bash
# The Werkstatt's app path end to end (#262), walked as an agent walks it:
# the guide's commands, typed into this commit's Werkstatt container, which
# Laib installed for real (systemd --user + Quadlet on the runner, Caddy
# with tls internal, Authelia + LLDAP). No model: the agent's steps are
# scripted, every one through `laib` (MCP, the claude hand) or `laib-app`.
#
# Real here: the Werkstatt image and its tools, the scaffold and its tests,
# the template lint, proposals and the human's approve, `laib template
# sync` from a git registry, apply with Caddy, Authelia's forward-auth and
# the proxy secret, `laib-app check` against the running Caddy.
# Simulated, and said so in the output: GitHub (a bare repo on the runner;
# "the owner merged" = a push to it), the app's CI (podman build of the
# scaffolded Containerfile, pushed to a registry on the runner instead of
# ghcr), Strato (a fake DNS-over-HTTPS answer that turns on once "the owner
# created the record"), Let's Encrypt (Caddy's internal CA).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="${LAIB_BIN:-$ROOT/laib}"
# the caddy and auth units name /mnt/data/laib/render; the workflow makes /mnt/data ours
export LAIB_HOME=/mnt/data/laib
export LAIB_QUADLET="${LAIB_QUADLET:-$HOME/.config/containers/systemd}"
export LAIB_SYSTEMD="${LAIB_SYSTEMD:-$HOME/.config/systemd/user}"
DATA=/mnt/data/stacks
DOMAIN=boot.test
REG=127.0.0.1:5055
WORK="$(mktemp -d)"
REGCONF="$HOME/.config/containers/registries.conf.d/laib-werkstatt-app.conf"
ROOTCA="$DATA/caddy/data/caddy/pki/authorities/local/root.crt"
PIDS=()
cleanup() {
  for p in "${PIDS[@]}"; do kill "$p" 2>/dev/null || true; done
  systemctl --user stop hallo.service werkstatt.service werkstatt-autoloop.service auth-pod.service caddy.service 2>/dev/null || true
  podman rm -f laib-app-registry >/dev/null 2>&1 || true
  rm -f "$REGCONF"
}
trap cleanup EXIT
L() { LAIB_ACTOR=human:owner "$BIN" "$@"; }       # the owner, on the box
say() { printf '\n== %s\n' "$*"; }
fail() {
  echo "FAIL: $*" >&2
  for u in hallo werkstatt caddy auth-authelia; do echo "--- $u" >&2; journalctl --user -u "$u.service" --no-pager 2>/dev/null | tail -15 >&2 || true; done
  L journal --since 1h 2>/dev/null | tail -30 >&2 || true
  exit 1
}
# the agent: a login shell as dev in the Werkstatt, the script on stdin
agent() { podman exec -i -u dev -w /workspace -e LAIB_APP_DOH="http://host.containers.internal:18553/dns-query" -e LAIB_APP_CAFILE=/tmp/caddy-root.crt systemd-werkstatt bash -l -s; }
open_id() { L inbox --json | python3 -c 'import sys,json; o=[p for p in json.load(sys.stdin) if p["status"]=="open"]; print(o[-1]["id"] if o else "")'; }
curlc() { curl -s --cacert "$ROOTCA" --capath "$WORK/none" "$@"; }

say "the box: laib, Caddy (tls internal), Authelia + LLDAP"
mkdir -p "$LAIB_QUADLET" "$(dirname "$REGCONF")" "$WORK/none"
printf '[[registry]]\nlocation = "%s"\ninsecure = true\n' "$REG" > "$REGCONF"
podman run -d --name laib-app-registry -p "$REG:5000" docker.io/library/registry:2 >/dev/null
for img in ghcr.io/mdopp/laib-caddy:2 ghcr.io/mdopp/laib-hooks:latest docker.io/authelia/authelia:4 docker.io/lldap/lldap:stable docker.io/valkey/valkey:8-alpine; do podman pull -q "$img" >/dev/null; done
L init --domain "$DOMAIN" --data-dir "$DATA" >/dev/null
cat > "$LAIB_HOME/state/box.yml" <<YML
domain: $DOMAIN
dataDir: $DATA
tls: internal
roles: {}
gpu: {present: false}
laib: {listen: 0.0.0.0, port: 5900}
YML
L add caddy "e2e #262" >/dev/null || fail "laib add caddy exit $?"
for _ in $(seq 30); do [ -s "$ROOTCA" ] && break; sleep 2; done
[ -s "$ROOTCA" ] || fail "no Caddy root CA"
OPW="$(head -c 18 /dev/urandom | base64 | tr -d '/+=')"; LPW="$(head -c 18 /dev/urandom | base64 | tr -d '/+=')"
printf %s "$OPW" | L secret set OPERATOR_PASSWORD >/dev/null
printf %s "$LPW" | L secret set LLDAP_ADMIN_PASSWORD >/dev/null
L add auth OPERATOR_USER=core "OPERATOR_EMAIL=core@$DOMAIN" BASE_DN=dc=boot,dc=test "e2e #262" > "$WORK/add-auth.txt" || { cat "$WORK/add-auth.txt"; fail "laib add auth"; }
grep -q "lldap seeded" "$WORK/add-auth.txt" || fail "LLDAP not seeded: $(cat "$WORK/add-auth.txt")"
code=""; for _ in $(seq 30); do code="$(curlc -o /dev/null -w '%{http_code}' --resolve "auth.$DOMAIN:443:127.0.0.1" "https://auth.$DOMAIN/api/health" || true)"; [ "$code" = 200 ] && break; sleep 2; done
[ "$code" = 200 ] || fail "Authelia through Caddy: HTTP $code"
LAIB_LISTEN=0.0.0.0:5900 L serve >"$WORK/serve.log" 2>&1 & PIDS+=($!)
for _ in $(seq 30); do curl -fsS http://127.0.0.1:5900/ready >/dev/null 2>&1 && break; sleep 1; done

say "the Werkstatt, installed by laib with this commit's image"
podman build -q -t "$REG/laib-werkstatt:e2e" "$ROOT/werkstatt" >/dev/null
podman push -q "$REG/laib-werkstatt:e2e"
tok() { L token mint "$1" --note "e2e #262" --json | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])'; }
tok pi | L secret set LAIB_PI_TOKEN >/dev/null
for s in CLAUDE_CODE_OAUTH_TOKEN WERKSTATT_GIT_TOKEN WERKSTATT_PI_GIT_TOKEN; do printf 'canary262-%s' "$s" | L secret set "$s" >/dev/null; done
L add werkstatt "IMAGE=$REG/laib-werkstatt:e2e" "e2e #262" >/dev/null || fail "laib add werkstatt exit $?"
for _ in $(seq 30); do podman exec systemd-werkstatt test -r /run/laib/werkstatt-token 2>/dev/null && break; sleep 2; done
podman cp "$ROOTCA" systemd-werkstatt:/tmp/caddy-root.crt && podman exec systemd-werkstatt chmod 644 /tmp/caddy-root.crt

say "agent: the toolbox, the guide, Laib over MCP"
agent <<'SH' || fail "the Werkstatt's toolbox"
set -euo pipefail
for t in git gh go gofmt node npm python3 gcc make jq curl rg dig openssl vi nano tmux podlet laib laib-app claude pi; do command -v "$t" >/dev/null || { echo "missing: $t"; exit 1; }; done
python3 -m venv /tmp/venv && /tmp/venv/bin/pip --version >/dev/null
go version; podlet --version
test -s /etc/claude-code/CLAUDE.md && grep -q "laib-app check" /etc/claude-code/CLAUDE.md   # Claude Code's managed CLAUDE.md
grep -q "laib-app new" /opt/werkstatt/guide/apps.md
laib status > /tmp/status.txt && head -3 /tmp/status.txt   # the claude hand reaches laib over host.containers.internal
LAIB_SESSION=e2e laib --headers | python3 -c 'import sys,json; assert json.load(sys.stdin)["Authorization"].startswith("Bearer laib_claude_")'
git config --global user.name "Werkstatt e2e"; git config --global user.email e2e@boot.test
SH
# #380: no claude token at rest; each session got its own, with claude's ttl, named in the token list
L secret list > "$WORK/secrets.txt" 2>&1 || true
if grep -q LAIB_CLAUDE_TOKEN "$WORK/secrets.txt"; then fail "a claude token is at rest in secrets.age"; fi
L token list > "$WORK/tokens.txt"
cat "$WORK/tokens.txt"
grep -E '^[0-9a-f]+ +werkstatt ' "$WORK/tokens.txt" >/dev/null || fail "laib add werkstatt minted no werkstatt token"
for s in shell e2e; do grep -E "^[0-9a-f]+ +claude .*expires [0-9].*session werkstatt: $s\$" "$WORK/tokens.txt" >/dev/null || fail "no claude token for the session $s with an expiry"; done

say "agent: scaffold hallo, its tests, the template lint (guide steps 2-3)"
agent <<'SH' || fail "scaffold, tests or lint"
set -euo pipefail
laib-app new hallo --owner mdopp
cd /workspace/laib-apps/apps/hallo && go vet ./... && go test ./...
cd /workspace/laib-apps && laib template lint templates/hallo
git add -A && git commit -qm "feat(hallo): a greeting for the family" && git log --oneline -1
SH
PORT="$(sed -n 's/^    port: \([0-9]*\)$/\1/p' "$DATA/werkstatt/workspace/laib-apps/templates/hallo/laib.yml" | head -1)"
[ -n "$PORT" ] || fail "no port in the scaffolded laib.yml"

say "simulated: the PR merged on GitHub, the repo's CI built the image"
podman unshare git -c safe.directory='*' clone -q --bare "$DATA/werkstatt/workspace/laib-apps" "$WORK/github/laib-apps.git"
git clone -q "$WORK/github/laib-apps.git" "$WORK/ci-checkout"
python3 -c 'import sys,yaml; w=yaml.safe_load(open(sys.argv[1])); j=w["jobs"]; assert "test" in j["image"]["needs"], "publish without tests"; assert j["image"]["if"].startswith("github.event_name == '"'"'push'"'"'")' "$WORK/ci-checkout/.github/workflows/apps.yml" || fail "the scaffolded CI publishes without its tests"
podman build -q -t "$REG/hallo:1" "$WORK/ci-checkout/apps/hallo" >/dev/null || fail "the scaffolded Containerfile does not build"
podman push -q "$REG/hallo:1"
echo "image $REG/hallo:1 (on a box: ghcr.io/mdopp/hallo, published by apps.yml)"

say "agent: register the repo as the box's template registry (guide step 5)"
agent <<SH || fail "laib-app register"
set -euo pipefail
laib-app register "$WORK/github/laib-apps.git" "Vorlagen der Werkstatt-Programme"
SH
ID="$(open_id)"; [ -n "$ID" ] || fail "no open proposal for the registry"
L inbox | sed -n '1,12p'
L approve "$ID" >/dev/null || fail "the owner's approve of #$ID exit $?"
grep -q "laib-apps.git" "$LAIB_HOME/state/box.yml" || fail "box.yml has no registry after #$ID"

say "agent: sync, find the template, propose the service (guide step 6)"
agent <<SH || fail "sync or propose --add"
set -euo pipefail
laib template sync
laib template list | jq -e '.available.hallo == ["apps"]'
laib propose --add hallo IMAGE=$REG/hallo:1 "Hallo für die Familie: begrüßt, wer angemeldet ist"
SH
ID="$(open_id)"; [ -n "$ID" ] || fail "no open proposal for hallo"
L inbox | sed -n '1,14p'
L approve "$ID" > "$WORK/approve.txt" || { cat "$WORK/approve.txt"; fail "the owner's approve of #$ID exit $?"; }
systemctl --user is-active --quiet hallo.service || fail "hallo.service is not active after the approve"
[ "$(curl -fsS "http://127.0.0.1:$PORT/healthz")" = ok ] || fail "hallo's health check"

say "simulated Strato: the record does not exist yet"
cat > "$WORK/doh.py" <<'PY'
import json, os, sys, urllib.parse
from http.server import BaseHTTPRequestHandler, HTTPServer
FLAG = sys.argv[2]
class H(BaseHTTPRequestHandler):
    def log_message(self, *a): pass
    def do_GET(self):
        q = urllib.parse.parse_qs(urllib.parse.urlparse(self.path).query)
        name = q.get("name", [""])[0].rstrip(".")
        ans = []
        if name == "boot.test":
            ans = [{"name": "boot.test.", "type": 1, "TTL": 60, "data": "203.0.113.7"}]
        elif name == "hallo.boot.test" and os.path.exists(FLAG):
            ans = [{"name": "hallo.boot.test.", "type": 5, "TTL": 60, "data": "boot.test."}, {"name": "boot.test.", "type": 1, "TTL": 60, "data": "203.0.113.7"}]
        b = json.dumps({"Status": 0 if ans else 3, "Answer": ans}).encode()
        self.send_response(200); self.send_header("Content-Type", "application/dns-json"); self.send_header("Content-Length", str(len(b))); self.end_headers(); self.wfile.write(b)
HTTPServer(("0.0.0.0", int(sys.argv[1])), H).serve_forever()
PY
python3 "$WORK/doh.py" 18553 "$WORK/strato-record" & PIDS+=($!)
sleep 1
rc=0; agent <<'SH' > "$WORK/check1.txt" || rc=$?
laib-app check hallo.boot.test
SH
cat "$WORK/check1.txt"
[ "$rc" = 8 ] || fail "laib-app check before the record: exit $rc, want 8"
grep -qF "Bei Strato unter boot.test die Subdomain „hallo“ anlegen und als CNAME-Ziel „boot.test“ eintragen." "$WORK/check1.txt" || fail "the check does not tell the owner the Strato record"

say "simulated Strato: the owner created the record; the agent checks again (guide step 7)"
touch "$WORK/strato-record"
agent <<'SH' > "$WORK/check2.txt" || { cat "$WORK/check2.txt"; fail "laib-app check after the record: exit $?"; }
laib-app check hallo.boot.test
SH
cat "$WORK/check2.txt"
grep -q "^route done: https://hallo.boot.test/" "$WORK/check2.txt" || fail "the check did not say done"

say "the family's view: Authelia's login, then hallo greets by name"
LT="$(curl -fsS -X POST -H 'Content-Type: application/json' -d "{\"username\":\"admin\",\"password\":\"$LPW\"}" http://127.0.0.1:17170/auth/simple/login | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')"
gql() { curl -fsS -X POST -H "Authorization: Bearer $LT" -H 'Content-Type: application/json' -d "$1" http://127.0.0.1:17170/api/graphql; }
FAM="$(gql '{"query":"{ groups { id displayName } }"}' | python3 -c 'import sys,json; print([g["id"] for g in json.load(sys.stdin)["data"]["groups"] if g["displayName"]=="family"][0])')"
gql "{\"query\":\"mutation(\$u:String!,\$g:Int!){ addUserToGroup(userId:\$u, groupId:\$g){ ok } }\",\"variables\":{\"u\":\"core\",\"g\":$FAM}}" >/dev/null
R=(--resolve "hallo.$DOMAIN:443:127.0.0.1" --resolve "auth.$DOMAIN:443:127.0.0.1")
code="$(curlc "${R[@]}" -o /dev/null -w '%{http_code} %{redirect_url}' -H 'Remote-User: mallory' "https://hallo.$DOMAIN/")"
echo "without a login, with a forged Remote-User: $code"
case "$code" in 30[23]\ https://auth.$DOMAIN/*) ;; *) fail "hallo without a login: $code" ;; esac
code="$(curlc "${R[@]}" -c "$WORK/jar" -o "$WORK/login.json" -w '%{http_code}\n' -X POST -H 'Content-Type: application/json' \
  -d "{\"username\":\"core\",\"password\":\"$OPW\",\"keepMeLoggedIn\":false,\"targetURL\":\"https://hallo.$DOMAIN/\"}" "https://auth.$DOMAIN/api/firstfactor")"
grep -q '^200$' <<<"$code" || fail "Authelia login: $(cat "$WORK/login.json")"
body="$(curlc "${R[@]}" -b "$WORK/jar" "https://hallo.$DOMAIN/")"
echo "logged in as core (family): $body"
[ "$body" = "Hallo, core!" ] || fail "hallo behind the login answered: $body"
body="$(curl -fsS -H 'Remote-User: mallory' "http://127.0.0.1:$PORT/")"
[ "$body" = "Hallo!" ] || fail "hallo trusts Remote-User without the proxy secret: $body"
L journal --since 1h | grep -E 'propose|approve' | tail -6 || true
say "pi in the browser (#384): on the host's loopback, through Caddy only for admins with two factors"
for _ in $(seq 60); do curl -fsS -H "Host: pi.$DOMAIN" http://127.0.0.1:8791/api/health >/dev/null 2>&1 && break; sleep 2; done
curl -fsS -H "Host: pi.$DOMAIN" http://127.0.0.1:8791/api/health || fail "pi-web-ui does not answer on the host's loopback"
echo
LANIP="$(hostname -I | awk '{print $1}')"
if curl -s -m 5 -o /dev/null -H "Host: pi.$DOMAIN" "http://$LANIP:8791/api/health"; then fail "pi-web-ui answers on $LANIP, past Caddy and Authelia"; fi
RP=(--resolve "pi.$DOMAIN:443:127.0.0.1" --resolve "auth.$DOMAIN:443:127.0.0.1")
code="$(curlc "${RP[@]}" -o /dev/null -w '%{http_code} %{redirect_url}' "https://pi.$DOMAIN/")"
echo "pi without a login: $code"
case "$code" in 30[23]\ https://auth.$DOMAIN/*) ;; *) fail "pi without a login: $code" ;; esac
code="$(curlc "${RP[@]}" -b "$WORK/jar" -o /dev/null -w '%{http_code} %{redirect_url}' "https://pi.$DOMAIN/")"
echo "pi with one factor only: $code"
case "$code" in 30[23]\ https://auth.$DOMAIN/* | 403\ *) ;; *) fail "pi with one factor only: $code (the route wants two)" ;; esac
echo "werkstatt app test ok: scaffolded, tested, linted, proposed, approved, routed through Caddy behind Authelia, checked; pi behind two factors"
echo "simulated: GitHub (bare repo), the app's CI (podman build on the runner, local registry), Strato (fake DoH), Let's Encrypt (Caddy's internal CA)"
