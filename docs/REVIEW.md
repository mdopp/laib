# Review brief — what Laib is, what was built, what is proven (2026-10-11)

For a second agent cross-checking this work. Everything below is in Git;
nothing has run on a real box. Read `README.md` (Stand), then this.

## Repositories

| Repo | What | State |
|---|---|---|
| `mdopp/laib` (public since 2026-10-12) | the control plane: kernel, stick, operator, lease, Frage, Automat, 22 templates, docs, ADRs, CI | release v0.3.5 (built by hand, #47); ci/boot green, vm-install green end to end on the merged chain (run 38282916929) |
| `mdopp/haus` (public since 2026-10-12) | the family's surface: Box page, pairing, companion API for the Android widgets | 5 commits, ci green, image `ghcr.io/mdopp/haus` |
| `mdopp/servicebay` | the predecessor, untouched, running on the Kraftwerk | reference only: `docs/COMPANION_APP.md`, `tools/sb/internal/build/assets/fedora-coreos.bu` (setup-raid) |
| `mdopp/solarisbay`, `mdopp/solaris-android` | voice/portal today; to be deconstructed (#21) | untouched; the app's `data/SbApiClient.kt` pins the companion JSON shapes |

## Laib: the pieces

| Piece | Code | Does | Proof |
|---|---|---|---|
| Kernel | `cmd/laib`, `internal/{state,render,plan,apply,proposal,policy,secrets,token,journal,sysd,boxfs}` | Git state repo as truth; `plan`/`apply` (snapshot, write, daemon-reload, restart, verify); proposals + inbox; hands/tiers; MCP over HTTP; three pages; converge; `disk`, `inbox`, `remove`, `backup`/`restore` | unit + golden tests (`testdata/golden/{basic,nuc}`), `scripts/e2e.sh` (shims), `scripts/boot-test.sh` (real Quadlet on a runner), VM |
| Operator | `cmd/laib-operator`, `internal/operator` | admin agent: OpenAI-compatible tool loop over Laib's MCP as hand `ops`, budget, pre-repair snapshots, notes, morning report; heartbeat in readiness/diag | e2e with a scripted model only |
| Lease | `cmd/laib-lease`, `internal/lease` | llama policy proxy + GPU lease, host unit | unit tests; never on a GPU |
| Frage | `cmd/frage`, `internal/frage` | SearXNG + summary, OpenAI-compatible ("was ist …") | unit tests; image built |
| Automat | `cmd/automat`, `internal/automat`, `internal/mcpserver` | HA automations as MCP in the bote pod, writes behind the admin's confirmation | unit tests vs fake HA; image built |
| Stick | `cmd/laib-stick`, `internal/stick`, `usb/` | cross-platform wizard: Butane in-process, ISO download + sha256, Ignition embed (coreos-installer format), dd / raw Windows write; first boot: data disk RAID1, firstboot → `install-box.sh` | VM: install, reboot, first boot, RAID, reinstall keeps data, caddy, backup/restore, update, remove |
| Templates (22) | `templates/*` | laib.yml contract + Quadlet units + hooks: adguard, audiobookshelf, auth, beets, bote, caddy, esphome, file-share, frage, haus, home-assistant, immich, llama, media, mosquitto, music-assistant, paperless, radicale, stimme, vaultwarden, werkstatt, whoami (+ README) | lint + render; only caddy installed on the VM |
| Werkstatt | `werkstatt/` | dev image: pi + Claude Code, autoloop, laibctl | python unit tests; image built |
| Docs | `docs/*.md`, `docs/adr/0001–0018` | KONZEPT, ZIELBILD, DESIGN, INTERFACE, STATE, TESTING, MIGRATION, DEKONSTRUKTION, BACKUP, INSTALL, OUTSIDE, SERVICES, C4 | — |

Sizes: kernel + tools 9.2k lines Go production, 1.8k tests (`scripts/loc.sh` caps 15k / tests ≤ prod); Haus 825 lines.

## CI (`.github/workflows`)

`ci` (vet, tests, template lint, LOC, e2e), `boot` (real Quadlet), `vm-install` (KVM guest: stick → CoreOS → first boot → RAID + mirror via `laib disk add` → reinstall → `laib add caddy` with own CA → restic backup/restore → update path → `laib remove`; ~18 min), four image builds, `release` (release-please, blocked by a repo setting; releases cut by hand).

## Decisions a reviewer should challenge

1. No LLM in the kernel; agents are hands with tokens and ceilings (ADR 0001, 0006, 0013).
2. systemd/Quadlet runs everything; Laib is a host binary, not a container (ADR 0002, 0003).
3. One approval surface; humans decide destroy; pages trust only the proxy (ADR 0012).
4. Caddy replaces NPM; the trial box uses `tls: internal` (Strato has no DNS API; port 80 goes to the Kraftwerk).
5. The data disk is a one-member RAID1 by design; the mirror joins later (`laib disk add`).
6. Backups: a restic repo on the data disk from day one; diag says what it does not protect against.
7. Hermes as stock for the Bote, configured only (ADR 0016); Automat and HA's MCP as its hands.
8. Haus is a separate program; Solaris keeps `/napi/whoami` and the portal paths until Haus owns them (#28, #21).
9. Templates stay builtin; the registry split is postponed (#24).

## Known gaps and risks

- Never on hardware: UEFI boot order (`usb/post-install.sh`), the real NIC, phones trusting Caddy's root, Hermes on a box (`SIGNAL_HOME_CHANNEL` unverified), the Operator against a real model, GPU/CDI for llama.
- Private ghcr images need `podman login` with a packages:read token (#30).
- The GitHub token rides the stick in clear text and stays in `~/.config/containers/auth.json` (#37: deliberately, so private images pull after a reboot; revoke it after the trial).
- `laib remove` scans only folders of services in the state; an unknown Quadlet folder is "unmanaged", never deleted.
- Haus trusts `Remote-User` because only Caddy reaches its loopback port; no second proof.
- Restore stops the service's units, restores per path, re-applies; the state repo restore (`--box`) was not exercised in the VM.

## How to verify yourself

```
go test ./... && scripts/e2e.sh                  # needs Go 1.27, python3
for t in templates/*/; do ./laib template lint "$t"; done
gh run list --repo mdopp/laib --workflow vm-install   # the end-to-end proof
gh run view <id> --log | grep -E "ready after|HTTP 200|\[UU\]|Daten bleiben|snapshot .* saved"
```
