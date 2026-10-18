#!/usr/bin/env bash
# One-shot carry-over of the secrets the box's data was made with (#228,
# ADR 0010: a throw-away script, never the kernel). For each service it reads
# the value from ServiceBay's store and pipes it into `laib secret set` under
# Laib's name, before that service's first apply: a fresh value locks the app
# out of its own database or config.
#
# Runs on the host as core while ServiceBay's container still runs. The store
# is config.json (installedSecrets, sealed enc:v1:iv:tag:ct with AES-256-GCM)
# and secret.key in ServiceBay's data dir; the host has no Python and openssl
# has no GCM, so the value is unsealed inside ServiceBay's own container by
# its node. It travels on a pipe straight into laib: never argv, a shell
# variable, a file or the terminal.
#
# Usage: scripts/carry-servicebay-secrets.sh [service ...]   (default: all)
#   SB_CONTAINER (servicebay), LAIB (laib) override the names.
set -uo pipefail
SB=${SB_CONTAINER:-servicebay}
LAIB=${LAIB:-laib}

# service     ServiceBay name                  Laib name
TABLE='
auth          LLDAP_ADMIN_PASSWORD             LLDAP_ADMIN_PASSWORD
auth          AUTHELIA_STORAGE_ENCRYPTION_KEY  AUTHELIA_STORAGE_KEY
immich        DB_PASSWORD                      IMMICH_DB_PASSWORD
immich        IMMICH_ADMIN_PASSWORD            IMMICH_ADMIN_PASSWORD
paperless     PAPERLESS_DB_PASSWORD            PAPERLESS_DB_PASSWORD
mosquitto     MQTT_PASSWORD                    MQTT_PASSWORD
media         JELLYFIN_ADMIN_PASSWORD          JELLYFIN_ADMIN_PASSWORD
adguard       ADGUARD_ADMIN_PASSWORD           ADGUARD_ADMIN_PASSWORD
file-share    SHARE_PASSWORD                   SAMBA_PASSWORD
'

# ServiceBay's own lookup (install/savedSecrets.ts loadSavedSecrets):
# installedSecrets wins over the legacy lldap/adguard fields; secrets.ts
# decrypt; DATA_DIR as dirs.ts reads it.
# Exit 3: no value; 4: unusable (a line break, a bad seal, a redaction mask);
# 5: the store cannot be read. It prints nothing but the value: a JSON.parse
# error would quote config.json, which holds plain values too.
UNSEAL='
try {
  const fs = require("fs"), c = require("crypto"), d = process.env.DATA_DIR || "/app/data", n = process.argv[1];
  const cfg = JSON.parse(fs.readFileSync(d + "/config.json", "utf8"));
  const legacy = {LLDAP_ADMIN_PASSWORD: (cfg.lldap || {}).password, ADGUARD_ADMIN_PASSWORD: (cfg.adguard || {}).password};
  let v = ((cfg.installedSecrets || []).find(e => e && e.varName === n && e.password) || {}).password || legacy[n] || "";
  if (typeof v !== "string") process.exit(4);
  if (v.startsWith("enc:")) {
    const p = v.split(":");
    if (p.length !== 5) process.exit(4);
    try {
      const x = c.createDecipheriv("aes-256-gcm", fs.readFileSync(d + "/secret.key"), Buffer.from(p[2], "hex"));
      x.setAuthTag(Buffer.from(p[3], "hex"));
      v = x.update(p[4], "hex", "utf8") + x.final("utf8");
    } catch (e) { process.exit(4); }
  }
  if (!v) process.exit(3);
  if (/[\r\n]/.test(v) || v === "<redacted>" || v === "__REDACTED__") process.exit(4);
  process.stdout.write(v + "\n");
} catch (e) { process.exit(5); }
'

for want in "$@"; do
  if ! grep -qxF -- "$want" <<<"$(printf '%s\n' "$TABLE" | awk '{print $1}')"; then
    echo "$want: no secret of it carries over (the table has: $(printf '%s\n' "$TABLE" | awk 'NF{print $1}' | sort -u | tr '\n' ' '))" >&2
    exit 2
  fi
done

fail=0
while read -r svc from to; do
  [ -n "${svc:-}" ] || continue
  if [ $# -gt 0 ] && ! grep -qxF "$svc" <<<"$(printf '%s\n' "$@")"; then
    continue
  fi
  podman exec "$SB" node -e "$UNSEAL" "$from" </dev/null | "$LAIB" secret set "$to" >/dev/null 2>&1
  rc=("${PIPESTATUS[@]}")
  case "${rc[0]}" in
    0) if [ "${rc[1]}" -eq 0 ]; then echo "$svc: $from -> $to"; else echo "$svc: laib secret set $to failed (rc ${rc[1]})" >&2; fail=1; fi ;;
    3) echo "$svc: $from is not in ServiceBay's store; $to NOT set" >&2; fail=1 ;;
    4) echo "$svc: $from in ServiceBay's store is no usable value (wrong secret.key, a line break, a redaction mask); $to NOT set" >&2; fail=1 ;;
    *) echo "$svc: $from could not be read from ServiceBay's store (rc ${rc[0]}); $to NOT set" >&2; fail=1 ;;
  esac
done <<<"$TABLE"
exit $fail
