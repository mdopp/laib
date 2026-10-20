# ADR 0024 — The host OS is a prebuilt image, proven at every boot

Applies when you are about to touch the host OS of a box: layer a package
with rpm-ostree, change how the NVIDIA driver or its CDI spec gets onto the
box, pause or resume Zincati, rebase a box, or wonder why an OS update
rolled itself back.

- **Status:** Proposed, 2026-10-19 (#368). Parts 2 and 3 are implemented
  (`install-box.sh` writes the units, `scripts/boot-check.sh` is the check, a verified release asset); part 1 waits for the owner.
- **Deciders:** the owner (mdopp); proposed by the #368 session.

## Context

The GPU box (Kraftwerk, Fedora CoreOS) has not taken an OS update since May.
The NVIDIA kernel module is compiled on the box at every update
(`akmod-nvidia-open` layered from RPM Fusion); against kernel 7.1 it failed
457 times in 84 hours and shrank `/boot`. The CDI spec was generated once,
behind a done-marker, and goes stale with every driver change. Nothing checks
a boot; going back is a human with SSH. So Zincati is paused by hand
(`/etc/zincati/config.d/90-paused-nvidia-kmod.toml`).

What ServiceBay did (`tools/sb/internal/build/assets/fedora-coreos.bu`,
`assists/recipe-retrofit-nvidia-gpu-on-a-running-box.md`): three rpm-ostree
stages with two reboots (repos, driver, CDI), done-markers per stage, a CDI
timer that had to be disabled by hand, `akmods --force` because
`akmods.service` is condition-skipped on CoreOS. It worked once, on install
night; every later update was a risk. The only stated reason for the open
modules was NVIDIA's recommendation for Turing and newer, which stays true.

## Decision

1. **The driver is built in CI, not on the box.** The GPU box runs an image
   in which kernel and NVIDIA module are already built together and booted
   in CI. *Entscheidung (vorgeschlagen):* Universal Blue's uCore NVIDIA
   variant (FCOS-based, open modules, signed, rebuilt on every FCOS stable
   release), pinned by digest in the state and moved by a Laib proposal like
   a service image (ADR 0019). Alternative in one line: an own
   `ghcr.io/mdopp/laib-host-nvidia` image `FROM` FCOS stable with the kmod
   built in our CI — full control, but Laib then owns a kernel-module build
   that breaks with every upstream kernel change, the exact work uCore
   already does for many boxes. The box moves once with `rpm-ostree rebase`
   (the old deployment stays as rollback); the RPM Fusion layering and both
   akmods go away with it. A box without a GPU stays on plain FCOS.
2. **CDI follows the driver.** `laib-cdi.service` runs
   `nvidia-ctk cdi generate --output=/etc/cdi/nvidia.yaml` at every boot,
   before the core user's manager starts the containers. No marker. On a
   box without `nvidia-ctk` it is condition-skipped. (The toolkit's own
   `nvidia-cdi-refresh` units are not used: one mechanism, independent of the
   toolkit's version.)
3. **Every new deployment is proven or rolled back once.**
   `laib-boot-check.service` (`/etc/laib/boot-check.sh`) waits up to 15
   minutes for: the GPU answering (`nvidia-smi`) and the CDI spec naming that
   driver, on a box with an NVIDIA card; laib's `/ready` answering with no
   more red services than the last proven boot had; and the LAN reaching
   the box (#445): every TCP port it listens on outside loopback is probed on
   the LAN address from a network namespace over a veth pair (loopback
   passes any firewall), with no port blocked that was open at the last
   proven boot (the record names the blocked ports, #481; laib's own
   `lan: false` ports are not probed; only a timeout or a refusal is a
   blocked port, any other failure of the probe measures nothing rather
   than "everything blocked").
   Green: the deployment is
   recorded in `/var/lib/laib/boot-proven`. Not green: `rpm-ostree rollback`,
   reboot, Zincati paused with `/etc/zincati/config.d/95-laib-boot-check.toml`
   (otherwise it stages the same update again), and an incident note in
   laib's journal, which reaches the owner. Never twice from the same
   deployment, never on the proven one, never when the rollback deployment
   is not the proven one. The first run records a baseline and never rolls
   back. "No more red than before" rather than "no red", so an app that was
   already red does not block every OS update. `/etc` belongs to one deployment, so
   what the check writes while booted into the new one would be gone after
   the rollback (found by vm-install, train 22). The records
   (`boot-proven`, `boot-rolled-back`) therefore live in `/var/lib/laib`,
   which every deployment shares, and the pause is written into the `/etc`
   of the deployment the box rolls back to
   (`/ostree/deploy/<os>/deploy/<id>/etc`). Zincati reads it there from the
   first second of that boot, so there is no window in which it could stage
   the broken update again. Writing the pause later, from the check on the
   rolled-back boot, would race with Zincati's start.
4. **`/boot` is measured.** `laib diag` is yellow when `/boot` has less free
   than one kernel set (~150 MiB).
5. **No layering on an existing box** (#367): `install-box.sh` never layers
   or reboots a box that is already in use.
6. **One firewall mechanism** (#445): Laib's nftables table. An image that
   brings its own firewall has it masked: uCore's firewalld (zone
   `FedoraServer`, ssh only) blocked the household for 2h20 after the
   Kraftwerk's rebase. `install-box.sh` masks it and `laib diag` is red
   while it runs. Two more things uCore turns on are handled with the
   rebase: `rpm-ostreed-automatic.timer` is disabled, because Zincati is the
   one update driver and the boot check's pause is a Zincati file; the ZFS
   units are disabled, because the data disk is mdadm with XFS and an
   import scan must not take a foreign disk. The checklist is in
   `docs/INSTALL.md` ("Rebasing a GPU box to uCore").
7. **OS updates are the normal path, through laib's reboot lock** (#368).
   Zincati keeps finding and staging updates; `install-box.sh` sets its
   strategy to `fleet_lock` against laib on loopback
   (`/etc/zincati/config.d/60-laib-updates.toml`, `/v1/pre-reboot`). laib
   grants the reboot only in the update window, two to four hours after
   `backup.schedule` (05:00–07:00 for the default 03:00), never while
   `laib-backup.service` runs, never while laib is not green, and never
   before the running deployment is proven by the boot check. A held reboot
   is a 409 for Zincati, which asks again in a few minutes. The grant and
   the arrival (`/v1/steady-state` after the boot) are journal lines
   (`os-update`); `laib diag` says when an update is staged and when its
   window is. The boot check stays the guard after the reboot. The pause
   files (`90-…`, `95-laib-boot-check.toml`: `updates.enabled = false`)
   still switch updates off entirely. Alternative in one line: Zincati's own
   `periodic` strategy, a fixed weekly window that knows nothing of a red
   laib or a running backup.

8. **The host image is pinned in the state and moves by proposal** (#368).
   `host/image` in the state repo holds two lines: `# tag: <tag>` (the tag
   it follows, e.g. `stable-nvidia`) and the rebase reference by digest
   (`ostree-unverified-registry:ghcr.io/ublue-os/ucore-minimal@sha256:…`).
   `laib updates` has a `host` row when the tag points to a newer digest.
   The Operator's weekly `aktualisierung` runner proposes it itself, as a
   patch to `host/image` (the model never sees it as an offer); the review
   makes it a human's call. Once approved, `laib-host-image.timer` (root,
   every 15 minutes, `scripts/host-image.sh`, a release asset checked like
   the boot check) stages the pinned image with `rpm-ostree rebase
   --bypass-driver` (Zincati is the update driver) and reboots into it only
   when laib's reboot lock (7.) says yes. The boot check proves the new
   deployment after that. A pause file holds the follower as well. The boot
   check names a deployment by its commit and serial, so a deployment that
   changes only kernel arguments is checked too.

## Consequences

- An OS update that breaks the GPU or laib costs one reboot and an incident,
  not an evening with SSH. The owner resumes updates by deleting the pause
  file once the cause is fixed.
- Until part 1 lands, the Kraftwerk still builds its module on the box; the
  boot check then catches a failed build (no `nvidia-smi`) and rolls back,
  which is what makes lifting the hand-made Zincati pause safe at all.
- Lifting the hand-made Zincati pause is the owner's step, once the boot
  check with the LAN probe (#445) has proven the running deployment
  (`docs/INSTALL.md`, "OS updates").
- vm-install (part `disks`) boots a deliberately broken deployment: its
  kernel line masks the core user manager, so laib never answers. It proves
  the rollback, the record of it and the Zincati pause (the laib half; the
  VM has no GPU).
