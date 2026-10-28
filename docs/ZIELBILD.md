# Laib — what we have

Rewritten 2026-10-19 as "what we have" (#160, Phase 5's last line). Until
then this file was the target picture and the phased way there; that plan
is done or decided, and the history is in Git and in #23 and #160. Every
statement below comes from this repo or its issues. What only the box can
answer says **auf der Box zu prüfen**. The concept is still
`docs/KONZEPT.md`. What each piece is and why is in `docs/DESIGN.md`.

## The box

**One box: the Kraftwerk**, the GPU box that ran ServiceBay. It runs Laib
and nothing else manages it. The Intel NUC was the proving ground (#23).
ServiceBay moved to Laib on migration day, 2026-10-18/19 (ADR 0010,
#160); its runbook and scripts were removed on 2026-10-22 and are in git
history.

- **Host OS:** Universal Blue's uCore, `ucore-minimal:stable-nvidia`,
  pinned by digest (`@sha256:a287759b…`). It is Fedora CoreOS 44, kernel
  7.2.7, with the open NVIDIA module 615.78 prebuilt (ADR 0024). The box
  was rebased on 2026-10-19 and the boot check proved that deployment
  (#368). The previous proven deployment stays as the rollback.
  firewalld, which uCore turns on, is masked: Laib's nftables table is the
  one firewall (#445).
- **Laib:** release v0.50.0 <!-- x-release-please-version --> in this repo
  (`version.txt`, bumped by release-please). Which binary runs on the box
  (`laib version`): auf der Box zu prüfen.
- **State and journal:** `/mnt/data/laib/state`, `/mnt/data/laib/journal`
  (`docs/STATE.md`). The data disk is an mdadm RAID1 with XFS. It runs with
  one member, which `laib diag` reports (`docs/BACKUP.md`).

## The services

The built-ins in `templates/` replace every unit ServiceBay ran
(`docs/SERVICES.md`). On the box, migration day moved them in this order:

- the platform roles: `auth` (Authelia + LLDAP), `adguard`, `llama` (the
  model role, with `laib-lease`), `caddy` (in place of NPM);
- the household: `radicale`, `mosquitto`, `file-share`, `beets`,
  `esphome`, `usage-metrics`, `immich`, `paperless`, `media`,
  `home-assistant`, `vaultwarden`.

The boot check after the rebase saw 17 of 17 services green (#368, 2026-10-19).
Which services the state holds today, `laib status` says: auf der Box zu
prüfen. The retired Solaris' units ran on as ServiceBay units, ignored
(`laib ignore`), until Phase 5: `scripts/solaris-off.py --do` stops and parks
them, takes their ignore.yml lines out, adds Stimme and lets Home
Assistant's post-deploy wire Stimme's Wyoming entries (#540, the
script's docstring is the runbook; in git history since 2026-10-24); their data stays 30 days.
Whether it ran: auf der Box zu prüfen. Its links into ServiceBay were
switched off explicitly before (#396). ServiceBay itself is off; its data dir
stays 30 days after migration day.

## What the family and the admin use

- **Haus** (`mdopp/haus`) is the family's surface: the pages at
  `haus.<domain>` and the Haus app. `dopp.cloud` and `www.` redirect to it
  (#360). It was added by hand after migration day (#379).
- **The Bote** (Hermes, ADR 0016) is the text agent on Signal and behind
  Haus's Sprechen field. It is installed on the Kraftwerk with Signal
  registered on a spare landline number, with hand fixes in the state
  (#469).
- **The Werkstatt** (pi, Claude Code, the autoloop) replaces `claude-dev`
  and `pi-web` (#402, #405, #407; the rollout check #436 is closed). Whether
  the bridges on ServiceBay units are gone: auf der Box zu prüfen.
- **The chat is the interface** (owner, 2026-10-19, #441): "Das Interface
  ist jetzt der Chat, das heißt aber nicht, dass die Daten deshalb falsch
  sein dürfen." What ServiceBay showed on web pages comes back as Laib
  verbs and MCP tools that pi, Claude and the Bote call. Each answer comes
  from the rendered or running state (`laib status`, `logs`, `inspect`,
  `look`, `net`, `diag`), never from a guess. A destructive action is
  approved only in Laib's Inbox with two factors; the chat tells about it
  and sends the link. The decisions per feature are in `docs/DESIGN.md`,
  "ServiceBay's features, decided".
- **Laib's own pages** (`laib.<domain>`, the default of `box.yml
  laib.host`: Status, Inbox, Journal, Netz) are the admins' and the one
  place where approvals happen (ADR 0012, #778). Which host the Kraftwerk's
  box.yml sets: auf der Box zu prüfen.
- **Messages to the owner** for a service red for ten minutes, a failed or
  stale backup, the weekly image updates and each new proposal go through
  the Bote first, by mail with Authelia's SMTP settings when no channel
  answers (#393, #457).

## Backup

- **On the box:** restic in `/mnt/data/backup/restic`, every night, plus a
  monthly restore test (`docs/BACKUP.md`). Until v0.9.0 the nightly run
  was refused on every box, so it never ran on the Kraftwerk (#444).
  Whether a snapshot exists now: auf der Box zu prüfen (#436).
- **Off the box:** a second restic repo on the Fritz!Box NAS, filled by
  `restic copy` after each backup (`backup.offsite`, #391). It needs the
  owner's step: a NAS user, FTP on, the password as a Laib secret, the
  block in `box.yml`. Done or not: auf der Box zu prüfen (#436).
- **The recovery sheet** (`laib recovery-sheet`) holds the age key and the
  restic password. Without it, an off-box repo is unreadable.

## OS updates

ADR 0024. The driver is built in CI (uCore), not on the box. `laib-cdi`
regenerates the CDI spec at every boot. `laib-boot-check` proves every new
deployment within 15 minutes (GPU, CDI, `/ready`, the LAN's view of the
ports) or rolls back once and pauses Zincati.

Zincati stages updates and asks Laib before each reboot (`fleet_lock`
against laib on loopback). Laib grants the reboot only in the window two to
four hours after the backup, never during a backup, only while green and
only from a proven deployment (`docs/INSTALL.md`, "OS updates").

The hand-made Zincati pauses (`55-servicebay-lock.toml`,
`90-paused-nvidia-kmod.toml`) stay until the owner lifts them after #445.
Whether they are gone: auf der Box zu prüfen.

## What moved out of ServiceBay and Solaris

- **ServiceBay's control plane** became Laib: the state repo instead of 28
  JSON stores, plan/apply with snapshot and rollback, Caddy instead of NPM,
  one secrets file, restic, the journal (`docs/DESIGN.md`).
- **Its migration** ran once, on 2026-10-19, by script and runbook: the
  secrets the data was made with were carried over, the data moved into
  Laib's layout (ADR 0010). The scripts, the runbook and their rehearsal
  were removed on 2026-10-22 and are in git history.
- **Its assist catalog:** the general checklists became Werkstatt skills
  (`werkstatt/guide/skills/`, #394). The rest stays behind. There is no
  `laib migrate assists` (#160).
- **Its disk import** is a Werkstatt skill with `scripts/laib-import.py`
  (#456). Its Google import is `scripts/import-google.py` (#372).
- **Solaris the agent retires** (ADR 0015). Voice is Home Assistant Assist
  with Stimme and Frage. Its pages, cards and companion API went to Haus.
  The household chat is the Bote.

## What is dropped on purpose

`docs/DESIGN.md`, "Features that are dropped" and, decided by the owner on
2026-10-19 (#441), "ServiceBay's features, decided". Dropped there:

- web pages in place of chat verbs
- the web terminal
- health graphs
- the settings, registry and setup pages
- learning proposals
- the Fritz!Box TR-064 integration
- the `:dev` canary
- multi-node
- the factory-reset assistant

## What is still open

The issue tracker (`gh issue list -R mdopp/laib`); the review of
2026-10-28 orders the work in #851. What this list named until then is
closed: #30, #128, #368 (the host image), #436 (the rollout check), #457,
#469, #470, #471 and #540 (the retired Solaris off on the Kraftwerk).
Whether each holds on the box: auf der Box zu prüfen. Still waiting on a
date: ServiceBay's data dir and the retired Solaris' data, deleted 30
days after they were switched off.
