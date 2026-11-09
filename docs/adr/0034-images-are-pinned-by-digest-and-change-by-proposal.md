# ADR 0034 — Images are pinned by digest and change by proposal; our own are verified by their signature

Applies when you add an image to a template, when you touch the nightly
image run (`laib-images.timer`, `apply.ImageRun`), the nightly `versionen`
skill or the weekly `aktualisierung`, Laib's self-update, an image
workflow's signing step, or when you weigh what a taken-over GitHub or
registry account could put on the box.

- **Status:** Accepted, 2026-11-09 — the direction is the owner's (security
  plan, gap „Software kommt von selbst“: „Images per Prüfsumme festnageln,
  Updates als Vorschlag wie Template-Updates“, all recommendations taken
  2026-11-09), and so is the cadence: choice **(c) „Frage ruhig bei
  allen“**, with a summary of what changed on the card („Ich will eine
  grobe Zusammenfassung bei der Frage sehen, was sich geändert hat. Gerne
  durch das LLM aus den Changelogs erstellt, kurz und bündig. Inhaltlich
  das wichtigste nennen.“).
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

## The owner's choice: (c), every image by a tap

The choice was how our own images (Homes, Dokumente, Werkstatt, Caddy,
Frage, Bote's helpers) come in: (a) like the rest, a nightly proposal the
dial auto-approves; (b) human for third-party images, auto for our
verified own (the recommendation); (c) everything human. The owner chose
(c), 2026-11-09:

5. **Every newer digest waits for a tap.** An upstream's and our own: the
   content review makes every new image of an existing container class
   human („neue Software nur auf deinen Tipp“), with or without data, so
   no dial and no agent approves it. Nothing pulls by itself any more.
6. **One card per night, with what changed.** The Operator's `versionen`
   skill (daily 05:30, `modell: kurz`) reads `laib updates --notes` and
   files one grouped values proposal (`laib_propose` `sets`) with every
   new digest. Per image the card shows its version change and a German
   summary of one to three sentences, „Zusammenfassung (automatisch)“,
   security fixes and breaking changes first, written by the box's own
   model from the upstream's changelog: the GitHub releases between the
   two version labels, else the commits between the two
   `org.opencontainers.image.revision` labels, from the repository the
   `org.opencontainers.image.source` label names. laib-haushalt reads
   GitHub behind the door (the kernel holds no GitHub client, #797), with
   its token when it has one, else unauthenticated, and waits for GitHub's
   reset after a rate limit. No changelog, or a busy model: the card says
   so in one line („Keine Änderungsnotizen gefunden.“ / „Keine
   Zusammenfassung: das Modell war gerade beschäftigt.“) and the tap is
   offered anyway; `modell: kurz` runs the script while the graphics card
   is elsewhere and the ask answers why instead of waiting.
7. **The changelog is untrusted.** The model only summarizes it, in one
   ask without tools (#861); the prompt marks the text as foreign; the
   script keeps three sentences of plain text without links, the kernel
   one line of at most 400 characters (`--note`), and the card labels it
   as automatic. The decision stays the person's.
8. **Pinned once, then by digest.** The same skill files, once while open,
   „Images festhalten“: each floating value pinned to the digest the box
   runs (`laib updates` `pin`). The card says they run on as now; such a
   pin of what runs needs no signature check.

## Consequences

- `laib-images.timer` no longer pulls or restarts anything; it still
  removes the images nothing needs (#1071). The proposer is the Operator's
  nightly `versionen`; `podman auto-update` stays masked;
  `AutoUpdate=registry` keeps only its meaning "no data a new image
  migrates": such a service is not snapshotted before its new image.
- Every image change is a commit in the state repo with its digest: "what
  ran on the night of …" has an answer, and a rollback is a revert.
- Werkstatt fixes, Homes, Dokumente and every other image of ours now
  wait for the owner's tap: merge → image → the next night's card → tap.
  The fast path is gone by choice (c); a fix that must land at once is a
  proposal the owner taps at once.
- Our own image is signed keyless in its build workflow (`cosign sign`,
  the workflow's GitHub identity, `id-token: write`); the review and
  `laib updates --notes` verify a new digest with the pinned cosign image
  (`registry.Cosign`) against `registry.SignedBy` — a workflow of one of
  the owner's repositories on main — and refuse an unsigned one. Images
  built before the signing step, and those of other repositories
  (Homes, Dokumente) until their workflows sign, are refused as new
  digests: they stay on what runs until a signed build arrives.
- `laib template lint` warns of a floating image; the templates' defaults
  still float and are pinned on the box by the „Images festhalten“ card.
- Laib's own binary still follows its releases nightly (`updates.laib`,
  `scripts/self-update.sh`, `SHA256SUMS`); checking the release assets'
  attestations is not done yet.
- The kernel grew by the signature check, `--notes`, `--note`, the
  grouped `sets` and the card's lines — within its cap (21,994 of 22,000);
  laib-haushalt by the changelog read (1,318 of 1,320).
