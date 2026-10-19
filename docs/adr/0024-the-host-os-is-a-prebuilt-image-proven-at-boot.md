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
   more red services than the last proven boot had. Green: the deployment is
   recorded in `/etc/laib/boot-proven`. Not green: `rpm-ostree rollback`,
   reboot, Zincati paused with `/etc/zincati/config.d/95-laib-boot-check.toml`
   (otherwise it stages the same update again), and an incident note in
   laib's journal, which reaches the owner. Never twice from the same
   deployment, never on the proven one, never when the rollback deployment
   is not the proven one. The first run records a baseline and never rolls
   back. "No more red than before" rather than "no red", so an app that was
   already red does not block every OS update.
4. **`/boot` is measured.** `laib diag` is yellow when `/boot` has less free
   than one kernel set (~150 MiB).
5. **No layering on an existing box** (#367): `install-box.sh` never layers
   or reboots a box that is already in use.

## Consequences

- An OS update that breaks the GPU or laib costs one reboot and an incident,
  not an evening with SSH. The owner resumes updates by deleting the pause
  file once the cause is fixed.
- Until part 1 lands, the Kraftwerk still builds its module on the box; the
  boot check then catches a failed build (no `nvidia-smi`) and rolls back,
  which is what makes lifting the hand-made Zincati pause safe at all.
- Still open: the rebase itself (owner, on the box), the image pin and its
  update proposal in Laib, a VM run of a deliberately broken deployment
  (the VM has no GPU: the laib half only), and an update window that never
  shares the backup slot (Zincati `[updates.periodic]` drop-in).
