# Laib — install on the box

Fedora CoreOS, user `core`, rootless Podman. Nothing here touches a running
service; after the install `laib plan` only *shows* what the box and the
state differ in.

```
curl -fsSL https://raw.githubusercontent.com/mdopp/laib/main/scripts/install-box.sh \
  | bash -s -- --domain <domain> --lan-ip <ip> \
       --model-url http://<gpu-box>:11435/v1 --model-lease http://<gpu-box>:11435/api/model-lease
```

Release v0.1.0 carries `laib`, `laib-operator`, `laib-lease` and
`SHA256SUMS`; the script verifies the sums. The repo, its releases and
the ghcr images are public: no GitHub token is needed (a private fork or
mirror exports `LAIB_GH_TOKEN`; never a flag, ps shows every argv, #217).

A box with its own GPU passes `--gpu` instead and later adds the `llama`
template as its model role. A box without one (the NUC) points
`--model-url` at a peer's llama proxy or at a cloud API, then
`laib secret set MODEL_API_KEY` before the first apply: the cloud API's
key, or the peer box's own `MODEL_API_KEY` (its lease API refuses without
it, #42). Operator, Bote and Frage read `${MODEL_URL}`, `${MODEL_KEY}` and
`${MODEL_LEASE}`.

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

Nothing before step 3 changes a service; NPM stays the proxy until the services have moved, then Caddy replaces it (step 4d).

## Updating

Re-run the install script with `--version vX.Y.Z` or without for latest. "Latest" is the newest release that has its binaries: they are built after the release's VM test, so for about an hour a fresh release is skipped, and the script says so.
The running services are restarted (`try-restart` of laib, operator, lease) and the installer checks that `/ready` reports the new version; the previous binaries stay as `*.prev` for `laib self rollback`. State and journal are untouched. `SHA256SUMS` is required: an unverified build is refused.


## The stick: Fedora CoreOS with Laib on first boot

ServiceBay's TUI built the install stick; Laib has `laib-stick`, one
binary for the laptop (Linux, macOS, Windows) with nothing to install
beside it: no podman, no butane, no coreos-installer. Download it from
the release, run it, answer the questions:

```
./laib-stick
Name der Box [nuc]:
Domain der Dienste: nuc.dopp.cloud
Feste LAN-Adresse (leer = DHCP): 192.168.178.30
SSH Public Key (Datei) [~/.ssh/id_ed25519.pub]:
Konsolen-Passwort für core setzen? [j/N]: j
Hat die Box eine GPU? [j/N]:
URL eines entfernten Modells: http://192.168.178.100:11435/v1
Lease-URL der entfernten llama: http://192.168.178.100:11435/api/model-lease
Zertifikate für die Dienste:
  1 = Let's Encrypt über Port 80 (Port 80 muss die Box erreichen, direkt oder weitergeleitet)
  2 = Let's Encrypt über einen DNS-Anbieter mit API (kein Port 80 nötig)
  3 = Caddys eigene Zertifikate (nur für einen Laptop-Versuch: Handys und ihre Widgets vertrauen ihnen nicht)
Auswahl [1]:
Soll dieser Stick eine schon installierte Box neu aufsetzen? [j/N]:

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
would (a cpio in `IMAGES/IGNITION.IMG`). Then it writes the stick: on
Linux and macOS with `dd` under sudo, on Windows itself (as Rufus' DD
mode does: lock and dismount the stick's volumes, raw write to
`\\.\PhysicalDriveN`), which needs it started as Administrator; the
device there is the disk number from the list it shows.

Boot the box from it: the live system installs CoreOS with the Ignition
(user `core` with your key, the hostname, `/mnt/data`, DNS stub off for
AdGuard, linger), promotes the SSD in the EFI boot order and reboots. On
the first boot `laib-firstboot.service` runs `install-box.sh` with the
flags you gave, then shreds its env file from the disk. After that, continue
at step 2 below (`laib` is installed and `laib init` has run). The stick
carries the DNS token in clear text when you gave one: keep the stick.

Not on the stick (yet): the NVIDIA driver for the GPU box. The plan is a
prebuilt image instead of ServiceBay's on-box layering (ADR 0024). What every
box gets from `install-box.sh` already (the install and every update, not
`--box-config`): `laib-cdi.service` (the CDI spec at every boot, skipped
without `nvidia-ctk`) and `laib-boot-check.service`, which runs
`scripts/boot-check.sh` (a release asset, checked against `SHA256SUMS` like
the binaries) to prove a new OS deployment or roll it back once, reboot and
pause Zincati (ADR 0024). That reboot is the boot check's; installing Laib
never reboots (#367).

## Before the first attempt (pre-flight, the NUC)

What makes or breaks the first boot is settled before the stick is built:

- **The NUC**: UEFI boot, Secure Boot off, one internal disk (the stick picks
  the smallest non-removable one and wipes it), LAN cable. Nothing on it you
  want to keep.
- **A LAN address**: reserve one in the Fritz!Box for the NUC's MAC (or take
  DHCP and look it up afterwards). It goes into `laib-stick` as the LAN
  address and into AdGuard's `*.<domain>` rewrite.
- **The domain and its certificates** (ADR 0007 amendments, #45):
  `dopp.cloud` is at Strato (no DNS API, no wildcards) and the house's port
  80 reaches the Kraftwerk. So the NUC uses HTTP-01 through the Kraftwerk:
  at Strato, one subdomain per NUC host as a CNAME to `dopp.cloud`
  (`laib.nuc`, `auth.nuc`, `ldap.nuc`, `haus.nuc`, `dns.nuc`, `whoami.nuc`);
  on the Kraftwerk's NPM, one proxy host per name, **HTTP only, no SSL**,
  forwarding to the NUC's LAN address on port 80. `laib-stick --domain
  nuc.dopp.cloud` without `--dns-provider` and without `--tls internal`;
  Caddy then gets Let's Encrypt certificates and the phones trust them.
  The NUC's AdGuard (or `/etc/hosts` lines) answers `*.nuc.dopp.cloud` with
  the NUC's LAN address. A DNS provider with an API (`--dns-provider desec`
  and the deSEC delegation, or INWX) remains the fallback. `--tls internal`
  (Caddy's own CA) exists for a laptop-only trial: Android apps and their
  widgets do not trust it.
- **The model**: the Kraftwerk's model proxy answers on
  `http://192.168.178.100:11435/v1` and the lease on
  `http://192.168.178.100:11435/api/model-lease`; `laib-stick` probes both
  from the laptop and warns when they do not answer.
- **No GitHub token**: the repo, its releases and the images
  (`ghcr.io/mdopp/laib-*`, `ghcr.io/mdopp/haus`) are public since
  2026-10-12, so the stick carries no token and the box keeps no registry
  login. (`laib-stick --token` still exists for a private fork or mirror;
  then the token is shredded after the first boot and kept for pulls.)
- **The SSH key**: the laptop's public key; the stick's console password is
  the fallback at the screen.
- **A way back**: the NUC is wiped and reinstalled by the same stick at any
  time; the Kraftwerk is not touched by anything in this trial.

Laib shells out to `git`, `diff` and `restic`. The first two are in the
CoreOS image (`git-core`, `diffutils`); `install-box.sh` puts restic's own
static release in `/usr/local/bin` (version and SHA256 pinned in the script,
like Laib's binaries). It layers nothing and never reboots (#367): an
rpm-ostree layering re-resolves every layered package, and on a box with a
staged deployment it needs a reboot. Installing Laib is not an OS update.

It also writes the box's own config, on every box and on every run (#281):
journald persistent with at most 4G and four weeks
(`/etc/systemd/journald.conf.d/50-laib.conf`) and podman's log driver pinned
to journald (`~/.config/containers/containers.conf.d/50-laib.conf`), so a
container's output is in `laib logs`. The stick's Ignition does not carry
them: the first boot runs this script, so there is one source. `laib diag`
is red while either is missing; `install-box.sh --box-config` writes only them.

What CI already proves (workflow `vm-install`): the stick's ISO boots in a
KVM guest, installs CoreOS, reboots, the first boot fetches and runs
`install-box.sh`, `laib` becomes ready and a real `laib add` applies on
CoreOS. What only the NUC proves: UEFI boot order (`post-install.sh`),
the real NIC, the LAN, the phones.

## The first installation, step by step (the NUC, Phase 1)

What you need before: Fedora CoreOS on the NUC with user `core` and SSH
(the stick above does exactly that);
a domain for the trial (its DNS at a provider the Caddy image carries:
Cloudflare, Hetzner, netcup, IONOS, INWX, deSEC, DuckDNS, Porkbun, OVH,
Route53) with an API token; the GPU box's model proxy reachable on the
LAN.

```
# 1. the binaries, the unit, the state repo
curl -fsSL https://raw.githubusercontent.com/mdopp/laib/main/scripts/install-box.sh \
  | bash -s -- --domain nuc.dopp.cloud --lan-ip 192.168.178.30 \
       --model-url http://192.168.178.100:11435/v1 --model-lease http://192.168.178.100:11435/api/model-lease
#    (the stick does all of this on first boot)
laib=/var/opt/laib/laib

# 2. certificates (ADR 0007 amendment 2026-10-13, #45): HTTP-01 through the
#    Kraftwerk. Before `laib add caddy`: each NUC host is a Strato CNAME to
#    dopp.cloud, and the Kraftwerk's NPM has an HTTP-only proxy host (no SSL)
#    per name forwarding to 192.168.178.30:80. Caddy then gets its certificates
#    on the first `laib add caddy`. With a DNS provider instead:
#    `--dns-provider desec` (+ LAIB_DNS_TOKEN); a laptop-only trial:
#    `laib init --tls internal`.

# 3. the roles: proxy, sso + identity, dns, then a first app. `laib add` is
#    propose + approve + apply as the human; a template that provides a
#    role takes it in box.yml, a host-network template joins the carve-outs.
$laib add caddy
$laib add auth OPERATOR_USER=mdopp OPERATOR_EMAIL=you@example   # the hook seeds LLDAP: admins, family, your account
$laib add adguard
$laib add whoami
#    the family's surface and the phone's API (Haus, mdopp/haus); its Laib token is minted on add
$laib add haus

# 4. look
$laib status; $laib diag; $laib plan
# https://laib.trial.example — the box page behind Authelia, login with your LLDAP account
# https://whoami.trial.example — the first service

# 5. the operator: on the trial it diagnoses and proposes, it does not repair
#    (owner decision 2026-10-13, ADR 0019): every restart or change waits for a
#    tap in the inbox. Raised to apply per action type once the evals (#82) are clean.
sed -i -E 's/^( *ops: *\{ *ceiling: *)apply/\1propose/' /mnt/data/laib/state/policy.yml
grep -n "ops:" /mnt/data/laib/state/policy.yml   # ops: { ceiling: propose }
$laib operator enable                        # mints the ops token, renders and starts laib-operator
$laib journal --since 1h
```

Point the router (or the trial laptop's DNS) at the NUC for the trial
domain only when AdGuard answers; until then test with `curl --resolve`.
Gate for Phase 2: a week of `converge` and `diag` without a human touch.
