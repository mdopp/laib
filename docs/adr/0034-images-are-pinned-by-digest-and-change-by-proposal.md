# ADR 0034 — Images are pinned by digest and change by proposal; our own are verified by their signature

Applies when you add an image to a template, when you touch the nightly
image run (`laib-images.timer`, `apply.ImageRun`), the weekly updates skill
(`skills/aktualisierung`) or Laib's self-update, or when you weigh what a
taken-over GitHub or registry account could put on the box.

- **Status:** Proposed, 2026-11-09 — the direction is the owner's (security
  plan, gap „Software kommt von selbst“: „Images per Prüfsumme festnageln,
  Updates als Vorschlag wie Template-Updates“, all recommendations taken
  2026-11-09); the cadence for our own images below is the open choice.
- **Amends:** ADR 0002 (#528: images without data update nightly by
  themselves).

## Context

Since #528 an image whose container carries `AutoUpdate=registry` and a
floating tag (`:latest`, `:2`, no tag) is pulled every update night,
restarted and verified, and goes back to the previous image on red. No
human sees it, no digest is recorded in the state repo, and whoever can
push to the tag decides what runs. The template defaults on main
(2026-11-09) that float:

| | Images |
|---|---|
| **nightly by themselves** (`AutoUpdate=registry`, floating), third party | adguard `adguardhome:latest`; bote `signal-cli-rest-api:latest`; esphome `esphome:latest`; file-share `filebrowser:latest`, `samba:latest`, `syncthing:latest`, `python:3.13-alpine`; frage `searxng:latest`; mosquitto `eclipse-mosquitto:2`; music-assistant `server:latest`; stimme `wyoming-openwakeword:latest`, `wyoming-piper:latest`, `faster-whisper:gpu`, `python:3.13-alpine`; whoami `whoami:v1.10` |
| **nightly by themselves**, our own (`ghcr.io/mdopp/…`) | `laib-automat`, `laib-directory` (bote), `laib-frage`, `homes`, `dokumente` (scan), `laib-werkstatt`, `laib-caddy:2` |
| floating, but by proposal (data inside, no `AutoUpdate`) | auth `authelia:4`, `lldap:stable`, `valkey:8-alpine`; audiobookshelf, beets, jellyfin, paperless-ngx, radicale, vaultwarden `:latest`; paperless `postgres:16`, `redis:7`; immich `immich-server:release`, `immich-machine-learning:release-cuda`, `redis:6.2-alpine`; home-assistant `:stable`, `python-matter-server:stable`, `zwave-js-ui:latest`; llama `laib-lease:latest` (pinned by the release) |

What the box runs may differ (its own `values.yml`); `laib updates` lists
it. Laib's own binary already comes by release with `SHA256SUMS`
(`scripts/self-update.sh`), but the release itself merges after green CI
(`release-merge.yml`): the same GitHub account signs off everything.

The pieces for "pinned and proposed" exist: `laib updates` computes a
pinned image's newer digest (`registry.newDigest`), the weekly
`aktualisierung` skill proposes it for a service with data (`image:tag@
sha256:…`), the dial decides (`values` is auto by default), apply verifies
and reverts a red batch (#609).

## Decision

1. **Every image is pinned.** A template's default and the box's value are
   `repo:tag@sha256:<digest>`. A floating value is a lint warning for a new
   template and a proposal on the box that pins it to what it runs now.
2. **A newer digest is a proposal, never a pull.** The nightly run no
   longer pulls; it files, per night, one grouped values proposal with the
   new digests and their version labels (`inbox.ImageVersion`), as a
   template update is filed. The dial decides as for every values change;
   the review keeps a different repository human. Restart, verify and the
   way back are apply's (#609), so a red night still ends on the old image.
3. **Our own images are verified, not trusted by name.** `ghcr.io/mdopp/…`
   images are built and signed in this repo's CI (keyless, the workflow's
   identity); Laib checks the signature before it proposes the digest and
   refuses one it cannot verify. The same for the release assets
   self-update fetches (GitHub attestations).
4. **The account is part of the chain.** The owner's GitHub account uses a
   hardware key or a passkey as its second factor (an owner step, about ten
   minutes).

## Open choice for the owner

How our own images (Homes, Dokumente, Werkstatt, Caddy, Frage, Bote's
helpers) come in, because a Homes fix today reaches the box the next night
without a tap:

- **(a) Same as the rest:** a nightly proposal the dial auto-approves. One
  trail and a pin; a taken-over tag still lands, but visibly, and the
  signature check (3) stops an image not built by this repo's CI.
- **(b) Human for third-party images, auto for our verified own:** an
  upstream's new `:latest` waits for a tap in the Inbox (grouped, once per
  night); our own run as today once the signature holds.
- **(c) Everything human.**

Recommendation: (b). The signature is what tells our image from a
stranger's; a tap per night for upstreams is the price, and it is one card.

## Consequences

- `laib-images.timer` becomes the proposer, not the puller; `podman
  auto-update` stays masked; `AutoUpdate=registry` keeps only its meaning
  "no data a new image migrates" (the review's `auto`).
- Every image change is a commit in the state repo with its digest: "what
  ran on the night of …" has an answer, and a rollback is a revert.
- The kernel grows: the grouped proposal, the signature check (cosign or
  `gh attestation` as a pinned tool), a lint. Not in the 2026-11-09 train:
  the kernel stands near its cap and the cadence (a/b/c) is the owner's.
- Werkstatt's fast path (merge → image → box the next night) keeps its
  speed only under (a) or (b).
