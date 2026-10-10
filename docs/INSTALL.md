# Laib — install on the box

Fedora CoreOS, user `core`, rootless Podman. Nothing here touches a running
service; after the install `laib plan` only *shows* what the box and the
state differ in.

```
# while the repo is private: a fine-grained GitHub token with Contents: read on mdopp/laib
export LAIB_GH_TOKEN=github_pat_…
curl -fsSL -H "Authorization: Bearer $LAIB_GH_TOKEN" \
  https://api.github.com/repos/mdopp/laib/contents/scripts/install-box.sh -H "Accept: application/vnd.github.raw" \
  | bash -s -- --domain <domain> --lan-ip <ip> \
       --model-url http://<gpu-box>:11435/v1 --model-lease http://<gpu-box>:8787/api/model-lease
```

Release v0.1.0 carries `laib`, `laib-operator`, `laib-lease` and
`SHA256SUMS`; the script verifies the sums. Once the repo is public the
token and the API path go away and the plain raw URL works.

A box with its own GPU passes `--gpu` instead and later adds the `llama`
template as its model role. A box without one (the NUC) points
`--model-url` at a peer's llama proxy or at a cloud API (`https://…`,
then `laib secret set MODEL_API_KEY` before the first apply). Operator,
Bote and Frage read `${MODEL_URL}`, `${MODEL_KEY}` and `${MODEL_LEASE}`.

What it does, in order:

1. Downloads the three release binaries to `/var/opt/laib/`: `laib`,
   `laib-operator` (the admin agent) and `laib-lease` (the model role's
   sidecar). The `/var/opt` prefix is writable on CoreOS; `/opt` is not.
2. Creates `/mnt/data/laib` owned by `core`.
3. Installs `dist/laib.service` as a **user** unit and enables linger so it
   runs without a login session.
4. Runs `laib init --domain …` once: the `age` key, the state repo with
   `box.yml` and `policy.yml`, the first journal line. **Print the key**
   (`/mnt/data/laib/age.key`) and keep it outside the box (ADR 0006).
5. Starts the daemon and prints `/ready`.

For a fresh box the same script runs from Ignition (`systemd.units` entry
that calls it once on first boot); for the migration it runs beside
ServiceBay.

## After the install (migration step 1, docs/MIGRATION.md)

```
laib plan                       # every ServiceBay unit shows as 'nicht verwaltet'
for u in $(laib plan --json | jq -r '.unmanaged[]'); do laib ignore "$u"; done
laib token mint ops             # for Solaris; paste into its ops profile
laib token mint claude --ttl 8h # for a Claude session
```

Then Caddy beside NPM (step 2). Nothing before that changes a service.

## Updating

Re-run the install script with `--version vX.Y.Z` or without for latest.
The unit restarts; state and journal are untouched.


## The stick: Fedora CoreOS with Laib on first boot

ServiceBay's TUI built the install stick; Laib has `laib-stick`, one
binary for the laptop (Linux, macOS, Windows) with nothing to install
beside it: no podman, no butane, no coreos-installer. Download it from
the release, run it, answer the questions:

```
./laib-stick
Name der Box [nuc]:
Domain der Dienste: trial.example
Feste LAN-Adresse (leer = DHCP): 192.168.178.30
SSH Public Key (Datei) [~/.ssh/id_ed25519.pub]:
Konsolen-Passwort für core setzen? [j/N]: j
Hat die Box eine GPU? [j/N]:
URL eines entfernten Modells: http://192.168.178.100:11435/v1
Lease-URL der entfernten llama: http://192.168.178.100:8787/api/model-lease
GitHub-Token (solange mdopp/laib privat ist): github_pat_…

Lade Fedora CoreOS (stable) …  100%  (sha256 geprüft)
ISO fertig: laib-nuc.iso — Ignition eingebettet
Laufwerke: …
USB-Gerät beschreiben: /dev/sdb
ALLES auf /dev/sdb wird gelöscht. Wirklich schreiben? [j/N]: j
```

Every answer is also a flag (`laib-stick --yes --hostname nuc --domain …
--device /dev/sdb` for a scripted run; `--print-ignition` shows the
Ignition the box will get, `--iso` reuses a downloaded image). What it
does: renders `usb/fedora-coreos.bu` with your answers and transpiles it
in-process (the Butane library), wraps it in a live Ignition that runs
`usb/pre-install.sh` (smallest non-removable disk), `coreos-installer
install`, `usb/post-install.sh` (EFI boot order) and reboots, downloads
the stream's ISO and verifies its sha256, and writes that live Ignition
into the ISO's embed area exactly as `coreos-installer iso customize`
would (a cpio in `IMAGES/IGNITION.IMG`). On Linux and macOS it then
writes the stick with `dd` under sudo; on Windows it leaves the ISO for
Rufus (DD mode) or balenaEtcher.

Boot the box from it: the live system installs CoreOS with the Ignition
(user `core` with your key, the hostname, `/mnt/data`, DNS stub off for
AdGuard, linger), promotes the SSD in the EFI boot order and reboots. On
the first boot `laib-firstboot.service` runs `install-box.sh` with the
flags you gave, then shreds the token from the disk. After that, continue
at step 2 below (`laib` is installed and `laib init` has run). The stick
carries the GitHub token in clear text while the repo is private: keep
the stick.

Not on the stick (yet): the NVIDIA driver layering and CDI setup
ServiceBay's TUI baked for the GPU box — Phase 4 brings them over.

## The first installation, step by step (the NUC, Phase 1)

What you need before: Fedora CoreOS on the NUC with user `core` and SSH
(the stick above does exactly that);
a domain for the trial (its DNS at a provider the Caddy image carries:
Cloudflare, Hetzner, netcup, IONOS, INWX, deSEC, DuckDNS, Porkbun, OVH,
Route53) with an API token; the GPU box's model proxy reachable on the
LAN; a GitHub token while this repo is private.

```
# 1. the binaries, the unit, the state repo
export LAIB_GH_TOKEN=github_pat_…
curl -fsSL -H "Authorization: Bearer $LAIB_GH_TOKEN" -H "Accept: application/vnd.github.raw" \
  https://api.github.com/repos/mdopp/laib/contents/scripts/install-box.sh \
  | bash -s -- --domain trial.example --lan-ip 192.168.178.30 \
       --model-url http://192.168.178.100:11435/v1 --model-lease http://192.168.178.100:8787/api/model-lease
laib=/var/opt/laib/laib

# 2. the DNS provider and its token
sed -i 's/^dns:.*//' /mnt/data/laib/state/box.yml
printf 'dns:\n  provider: cloudflare\n  secretRef: DNS_API_TOKEN\n' >> /mnt/data/laib/state/box.yml
echo '<the token>' | $laib secret set DNS_API_TOKEN

# 3. the roles: proxy, sso + identity, dns, then a first app. `laib add` is
#    propose + approve + apply as the human; a template that provides a
#    role takes it in box.yml, a host-network template joins the carve-outs.
$laib add caddy
$laib add auth OPERATOR_USER=mdopp OPERATOR_EMAIL=you@example   # the hook seeds LLDAP: admins, family, your account
$laib add adguard
$laib add whoami

# 4. look
$laib status; $laib diag; $laib plan
# https://laib.trial.example — the box page behind Authelia, login with your LLDAP account
# https://whoami.trial.example — the first service

# 5. the operator
$laib operator enable                        # mints the ops token, renders and starts laib-operator
$laib journal --since 1h
```

Point the router (or the trial laptop's DNS) at the NUC for the trial
domain only when AdGuard answers; until then test with `curl --resolve`.
Gate for Phase 2: a week of `converge` and `diag` without a human touch.
