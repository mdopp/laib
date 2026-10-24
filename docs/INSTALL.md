# Laib — install on the box

Fedora CoreOS, user `core`, rootless Podman. Nothing here touches a running
service; after the install `laib plan` only *shows* what the box and the
state differ in.

```
curl -fsSL https://raw.githubusercontent.com/mdopp/laib/main/scripts/install-box.sh \
  | bash -s -- --domain <domain> --lan-ip <ip> \
       --model-url http://<gpu-box>:11435/v1 --model-lease http://<gpu-box>:11435/api/model-lease
```

A release carries `laib`, `laib-lease`, `laib-disk`, `laib-haushalt`,
the scripts and `SHA256SUMS`; the script verifies the sums. `laib-operator`
and `laib-notify` are images (`ghcr.io/mdopp/laib-operator`,
`ghcr.io/mdopp/laib-notify`) whose digests the release builds into `laib`
(ADR 0027): its apply pulls them. The repo, its releases and
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

1. Downloads the release binaries to `/var/opt/laib/`: `laib`,
   `laib-lease` (the model role's sidecar) and, since #443, `laib-disk`
   (the data RAID and the disks' health, which `laib disk` and `laib diag`
   run), and since #598 `laib-haushalt` (the household verbs `laib scan` and `laib net
   geraete`, which laib serves through its verb door, ADR 0026). The `/var/opt` prefix is writable on CoreOS; `/opt` is not.
2. Creates `/mnt/data/laib` owned by `core`.
3. Installs `dist/laib.service` as a **user** unit and enables linger so it
   runs without a login session.
4. Runs `laib init --domain …` once: the `age` key, the state repo with
   `box.yml` and `policy.yml`, the first journal line. **Print the key**
   (`/mnt/data/laib/age.key`) and keep it outside the box (ADR 0006).
5. Starts the daemon and prints `/ready`.

For a fresh box the same script runs from Ignition (`systemd.units` entry
that calls it once on first boot).

## After the install

```
laib plan                       # a unit Laib did not write shows as 'nicht verwaltet'
laib ignore <unit>              # one that stays beside Laib on purpose
laib token mint claude --ttl 8h # for a Claude session
```

## One wildcard certificate (DNS-01, the challenge delegated to deSEC)

For a domain whose DNS has no API (`dopp.cloud` at Strato) and that should
need no public record per name (#378, ADR 0007 amendment 2026-10-19). The
domain and its records stay where they are; only the ACME challenge moves.
`<name>` below is the deSEC name you register, e.g. `dopp-acme`.

1. **deSEC account** (owner): sign up at <https://desec.io> with an email
   address, confirm it. Free, no payment details.
2. **The challenge zone** (owner): in the deSEC web UI "Domain Management" →
   "+" → a dynDNS name `<name>.dedyn.io`. (Alternative: a zone of your own
   such as `acme.dopp.cloud`, which needs NS records at Strato; the dedyn
   name needs none.) Nothing else goes into it; Caddy writes and removes
   one TXT record there per issuance.
3. **The token** (owner): "Token Management" → "+" → name `laib caddy`,
   no expiry. Copy it once, it is shown once. Restrict it (token policies,
   "Edit policies"): a default policy without write permission, plus one
   for domain `<name>.dedyn.io`, subname empty, type `TXT`, write
   permission. The account then holds only this zone, and the token
   writes only its TXT records.
4. **The CNAME at Strato** (owner): `_acme-challenge` of `dopp.cloud`
   (exists since 2026-10-19, pointing at `dopp.cloud`): change its target
   to `<name>.dedyn.io.` Check from anywhere:
   `dig +short CNAME _acme-challenge.dopp.cloud @9.9.9.9` answers
   `<name>.dedyn.io.`
5. **On the box** (core):

   ```
   laib secret set DNS_API_TOKEN          # paste the token, Enter, Ctrl-D: stdin, never argv
   ```

   then in `/mnt/data/laib/state/box.yml`:

   ```yaml
   dns:
     provider: desec
     secretRef: DNS_API_TOKEN
     challengeAlias: <name>.dedyn.io
   ```

   `laib diag` shows no `acme` finding (the CNAME is right), then
   `laib plan` (the Caddyfile: one `cert_issuer`, a `*.dopp.cloud` site)
   and `laib apply`. Caddy fetches `*.dopp.cloud` within a minute:
   `laib logs caddy` shows "certificate obtained successfully" for
   `*.dopp.cloud`. The per-name certificates from HTTP-01 stay in
   `${DATA_DIR}/caddy/data` until they expire; they are no longer renewed.
6. **Proof**: a new route with no record at Strato answers in the LAN with a
   valid Let's Encrypt certificate:
   `curl -sv https://<new>.dopp.cloud 2>&1 | grep -E 'subject|issuer'`
   shows `CN=*.dopp.cloud`. Names used from outside still need their
   public CNAME at Strato (ADR 0013, class 3).

## Updating

Laib updates itself (#547): `laib-images.service` runs
`/var/opt/laib/self-update.sh` before the nightly image run, in its window
(box.yml `updates`). It takes the newest release with binaries (or the
version `updates.laib` holds; `off` only looks) through that release's own
verified `install-box.sh`, only while no backup runs, the lock is free and
laib is green (never back to an older version), and puts the previous
binaries back, converged, when `/ready` is not green on the new version
within three minutes; that release is then skipped until a newer one. `laib updates` shows the
`laib` row, `laib diag` the `self-update` probe, the log is
`/mnt/data/laib/self-update.log`. A box installed before #547 needs the
manual run below once. To try it now: `systemctl --user start laib-images.service`.

By hand: re-run the install script with `--version vX.Y.Z` or without for latest. "Latest" is the newest release that has its binaries: they are built after the release's VM test, so for about an hour a fresh release is skipped, and the script says so.
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

### Rebasing a GPU box to uCore (once, ADR 0024)

uCore is Fedora CoreOS plus the prebuilt NVIDIA driver, but it also turns
on things plain CoreOS does not have. Handle them in the same session as
the rebase. The Kraftwerk ran into the first one (#445):

1. **Before the reboot: `sudo systemctl mask firewalld.service`.** uCore
   enables firewalld with the zone `FedoraServer` (ssh, cockpit,
   dhcpv6-client only). Next to Laib's own nftables table (`inet laib`) it
   blocks DNS, 80/443, Samba, MQTT and the rest from the LAN, and lets only
   SSH in. Laib has exactly one firewall mechanism, its table. A mask in
   `/etc` carries over into the new deployment and wins over the image's
   enablement. `install-box.sh` (and `--box-config`) masks a firewalld it
   finds, and `laib diag` is red (`firewall`) while one runs.
2. **After the boot, Zincati and `rpm-ostreed-automatic.timer`** (#486):
   Zincati asks Fedora's update graph, which does not know the uCore image
   (`booted deployment … not found in the update graph`): it never finds an
   update. uCore's `rpm-ostreed-automatic.timer` is a second update driver
   that ignores the pin and the pause files. On uCore the one update path
   is `laib-host-image` (below). `install-box.sh` (and `--box-config`)
   masks both on a box booted from a container image other than Fedora
   CoreOS's own, and `laib diag` is yellow (`os-update`) while Zincati
   runs there. Rebase by tag
   (`rpm-ostree rebase ostree-unverified-registry:ghcr.io/ublue-os/ucore-minimal:stable-nvidia`),
   then the next `install-box.sh` pins the booted digest itself.
3. **After the boot, the ZFS units** (`zfs-import-cache.service`,
   `zfs-import-scan.service`, `zfs-mount.service`, `zfs-zed.service`,
   `zfs.target`): Laib's data disk is an mdadm mirror with XFS and has no
   pool. `zfs-import-scan` would import any pool it finds on a disk that
   does not belong to Laib (the foreign-disk rule). Disable them:
   `sudo systemctl disable --now zfs-import-cache.service zfs-import-scan.service zfs-mount.service zfs-zed.service zfs.target`.
4. **The boot check** then proves the new deployment. Since #445 that
   includes the LAN's view: every TCP port the box listens on outside
   loopback is probed from a network namespace (as a LAN client's packets
   arrive). Ports laib closes itself (`lan: false`) are not probed. A port
   blocked now that was not blocked at the last proven boot means "not
   proven", and the box rolls back once; the record names the blocked ports
   (#481), so a port another firewall closes on purpose stays allowed.

### OS updates (laib's reboot lock, ADR 0024)

One update path per box, and both reboot through laib's lock. On plain
Fedora CoreOS, Zincati finds and stages OS updates. On a box booted from
another container image (uCore), `laib-host-image.timer` follows the image
pinned in the state, and Zincati is masked (#486).

On Fedora CoreOS: with `install-box.sh`'s
`/etc/zincati/config.d/60-laib-updates.toml` (strategy `fleet_lock`), it asks
laib before every reboot. laib says yes only in the window two to four hours
after the backup (05:00–07:00 for `backup.schedule: "03:00"`), only while the
backup is not running, only while laib is green, and only once the running
deployment is proven (`/var/lib/laib/boot-proven`). `laib diag` shows a staged
update (`os-update`), and the journal shows the grant and the arrival. After
the reboot the boot check proves the new deployment or rolls it back once.

On uCore: `host/image` in the state holds `# tag: <tag>` and the rebase
reference by digest. `install-box.sh` writes it from the booted deployment
when the state has none and the box was rebased by tag (the tag, and the
digest it runs), and commits it. Laib's weekly update run proposes a newer
digest of that tag; `laib updates` shows it on the `host` row ("neuer
Digest von …"). After a human approves it, `laib-host-image.timer` (every 15
minutes) stages it with `rpm-ostree rebase`, asks the same lock
(`/v1/pre-reboot`), and reboots only on its yes; the boot check proves the
new deployment or rolls it back once. A box rebased by digest does not say
which tag it follows: pin it once by hand, then check:
```
cd /mnt/data/laib/state && mkdir -p host
printf '# tag: %s\n%s\n' stable-nvidia "$(rpm-ostree status --json | jq -r '.deployments[] | select(.booted) | ."container-image-reference"')" > host/image
git add host/image && git commit -qm "host: pin the running image"
cat host/image && /var/opt/laib/laib updates | grep '^host'
```
`laib diag` is yellow (`os-update`) on a uCore box without the pin: it gets
no OS update at all.

**The pause is one mechanism for both paths**: a drop-in in
`/etc/zincati/config.d/` with `[updates]` `enabled = false`, read in name
order, the last file that sets `enabled` decides (as Zincati reads them;
`laib-host-image` reads them the same way, also where Zincati is masked).
The boot check writes `95-laib-boot-check.toml` after a rollback, so the
rolled-back update is not staged again. `laib diag` says which file pauses
(`os-update`, yellow for the boot check's), and so does `laib updates` on
the `host` row.

Lifting a pause is the owner's step. Do it once the running deployment is
proven with the LAN probe (#445):
```
cat /var/lib/laib/boot-proven                                # the booted deployment, red, blocked count, blocked ports: "0 0", or the ports another firewall closes on purpose
ls /etc/zincati/config.d/                                   # 60-laib-updates.toml, and the pause files
sudo rm /etc/zincati/config.d/55-servicebay-lock.toml       # ServiceBay's window, if it is still there
sudo rm /etc/zincati/config.d/90-paused-nvidia-kmod.toml    # the hand-made pause (the driver is prebuilt now)
/var/opt/laib/laib diag | grep os-update                     # no "pausiert" left
# Fedora CoreOS: Zincati picks it up
sudo systemctl restart zincati.service
journalctl -u zincati -n 20 --no-pager                      # "fleet_lock" and "reached steady state"
# uCore: the follower picks it up at its next run, or now
sudo systemctl start laib-host-image.service
journalctl -u laib-host-image -n 5 --no-pager               # nothing to do, "staging …", or "the reboot lock says not now"
```
`95-laib-boot-check.toml` appears only after an automatic rollback. Delete
it only once the cause is understood.

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
$laib operator enable                        # mints the ops token, renders and starts laib-operator's container
$laib journal --since 1h
```

Point the router (or the trial laptop's DNS) at the NUC for the trial
domain only when AdGuard answers; until then test with `curl --resolve`.
Gate for Phase 2: a week of `converge` and `diag` without a human touch.
