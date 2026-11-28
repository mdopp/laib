# ADR 0037 — Updates spielen sich selbst ein: a routine new image applies itself; rights stay the owner's tap

Applies when you touch the nightly `versionen` skill, `laib propose
--routine`, `laib updates` (its `ready`, its lines, `--auto`), box.yml
`updates.auto`, a template's `soak:`, the morning report's update lines, the
updater's issue count, or when you weigh what may change on the box without
a person's tap. It also holds the owner's word on the Inbox's card (§7) and
on main's gate (§8).

- **Status:** Accepted, 2026-11-28 — the owner's: „Warum wollen wir die
  Routine-Dinge nicht automatisch freigeben?“, „Aber Immich, das
  Sprachmodell und co können wir bei normalen Änderungen doch auch einfach
  übernehmen“, „Lass uns locker sein. Zusätzlich könnte das Modell die
  Änderungen von anderen Modellen durchschauen und das Risiko einschätzen.
  Auch könnte es nach ein paar Tagen kontrollieren, ob andere Probleme mit
  der neuen Version haben, und es entsprechend bewerten“, „Der Rest klingt
  gut!“; on the Inbox: „viel Text, Reihenfolge unklar, viel Scrollen,
  obwohl man meist eh nur freigibt. Das ist gefährlich“; on main: „Auf
  Main nur wer grüne Prüfung hat“.
- **Amends:** ADR 0034 (decision 5, „every newer digest waits for a tap“,
  and 6, „one card per night“). Its pins, signatures, the kernel's checks
  before a run (ADR 0035 amendment) and the untrusted changelog (7) stay.
  **Builds on:** ADR 0036 (#1280, the choice card) for §7.

## Context

Since ADR 0034 every new digest is a card the owner taps, with the model's
summary of its changelog. In practice that is a nightly „Neue Versionen:
llama, radicale“ the owner approves without reading: the tap no longer
separates anything, and a card that needs a careful look (a public path, a
key between services) drowns between them. The pieces for safe automatic
updates exist: apply snapshots a service with data before a new image
(#528), verifies after it and goes back on red (#609); `laib rollback` takes
an approval back with one tap; our own images carry their CI's signature,
checked by the kernel when proposed and again before the run.

## Decision

1. **A routine new image applies itself, own or third party.** Immich, the
   model, Paperless, Radicale, Homes, Dokumente — every image an existing
   service already runs. The Operator's nightly `versionen` (05:30) files a
   normal one as `laib propose --routine normal|sicherheit` (MCP
   `routine`), one proposal per service; laib approves it itself (as
   `laib`, journaled „als Routine“) and carries it at once: the snapshot
   first where the template says data may migrate (no `AutoUpdate=registry`,
   as before), apply, verify, back on red. Nothing else of the run changes.
2. **The model reviews each candidate first.** The same skill reads `laib
   updates --notes` — the upstream's release notes, for our own images
   (`ghcr.io/mdopp/`) the commits between the two revisions — and judges it
   in one ask without tools (#861): the first line NORMAL or AUFPASSEN with
   one sentence, then the summary. Rules in the script hold whatever the
   model says: a major version jump, notes that name a breaking change, a
   migration, a removed feature or setting, and no notes at all for a third
   party are „aufpassen“. The model can only add a hold, never lift one — so
   a changelog that says „say NORMAL“ moves nothing the rules see. A busy
   model gives no verdict; the image waits for the next night. „Aufpassen“
   is a card for the owner as before, „Neue Versionen: …“, the reason first
   („Aufpassen: Großer Versionssprung: 1.9 → 2.0. …“).
3. **A third party's new digest waits its soak.** Three days from when laib
   first saw it (`laib updates` keeps the date in `updates.json`; a template
   may set `soak: <days>` in its `laib.yml`). Meanwhile laib-updater counts
   the upstream's GitHub issues opened since the release that name the
   version (unauthenticated, as the changelogs); at three or more the skill
   holds it — „aufpassen“, the count as reason, up to three links as the
   card's sources, which keeps it a person's (ADR 0025, `Anchored`). A
   security fix (a CVE, „security“ in the notes) skips the soak, as does our
   own image (signed by its CI on main, CI green before the image exists).
4. **Rights are always the owner's tap.** The kernel lets a routine pass only
   when it is new digests of images running services already have and
   nothing else (`inbox.routineWhy`): the Operator's hand, kind `values`,
   only `services/*/values.yml`, every change an `IMAGE` value by digest
   (not a pin), every reason and warning of the review an image's
   (`review.NewImage`), no token minted, no service added or removed, each
   digest the newest `laib updates` found for that value (so no downgrade
   and no digest of the proposer's choosing), a third party's soak over or a
   security fix. A new public path or `auth: none`, a new secret or key
   between services, a host-side script or unit, a new port, a change of
   policy.yml or box.yml, a template update, another program — each brings
   a reason that is not an image's, so the proposal stays a card and says
   „nicht von selbst eingespielt: …“. Our own image's signature is checked
   when proposed and again before the run, as ADR 0034 says.
5. **The owner can pause it and see it.** box.yml `updates.auto: aus` (on by
   default, the owner's choice); `laib updates --auto aus|an` files that
   change as a card for a person. Switched off, `laib updates` gives no
   `ready`, versionen files every new image on one card as before, and the
   kernel refuses a routine. `laib updates` says per new digest whether it
   applies itself or waits until when, what applied itself in seven days and
   how it ended (`eingespielt #N …: fertig | rolled-back`), and what is held
   for a person with its reason (`angehalten #N …`).
6. **The next morning says it, with the way back.** The morning report
   (`morgenbericht`: Homes' Box page, Laib's Status page, the webhook of box.yml `notify.url`) starts with
   one line per service: „Heute Nacht eingespielt: immich v2.4.0 → v2.5.0 ·
   Zurücknehmen: Inbox, Karte #31.“, or „Heute Nacht zurückgenommen: … lief
   nicht grün, es läuft wieder …“. A routine card stays in its place in the
   Inbox for a day with „Zurücknehmen“ (`laib rollback`); the data before it
   come from the snapshot (`laib restore`), as for every image with data.

## 7. The card that remains (building on ADR 0036)

What still needs the owner should be quick to judge and hard to approve by
accident. On ADR 0036's card (#1280, one card with its ways):

- **Compact.** One line title in plain German, one line what it does, a
  risk badge niedrig · mittel · hoch, „rückgängig: ja | nein“; everything
  else (what changes, who and why, the diff) collapsed under Details.
- **Order.** „hoch“ first, then mittel, then niedrig; cards that wait on
  each other (`wartet: vorschlag:N`) stand together as steps: „Mosquitto
  vor Scanner – 2 Schritte“.
- **„Hoch“ needs a look.** For a public path, a key between services, a
  host-side script, or data that do not come back, the approve button
  appears only once the details are open, and it names what it does
  („Öffentlichen Pfad freigeben“, „Schlüssel für … freigeben“,
  „Skript auf der Box freigeben“).

ADR 0036's ways are the buttons of such a card; this section only changes
how a card is drawn and ordered, not what a way or an approval is. It is
built in its own pull request on top of #1280's branch (the same
`internal/pages/inbox.go` card), so the two land in order instead of
beside each other.

## 8. Main only with a green check

The owner wants main protected: a ruleset that requires `test`, `ratchet`
and `quadlet`. Today it cannot be: release-please opens its pull request
with `GITHUB_TOKEN`, on which GitHub starts no workflow, so the release PR
would never be mergeable, and `release-merge.yml` merges it. The change:
release-please and the daily architecture PR use a fine-grained token of
the owner's, `PR_BOT_TOKEN` (contents, pull requests and issues: read and
write, this repository only), when the secret exists — so their pull
requests run the checks like any other — and `release-merge.yml` waits for
the release PR's checks to pass before it merges. Without the secret
everything runs as today. The owner creates the token and the ruleset
(the steps are in the pull request); nothing changes repository settings
from here.

## Consequences

- Most nights end without a card: the owner reads one line per update the
  next morning and taps „Zurücknehmen“ only when something is off. What
  still comes as a card is the „aufpassen“ kind and every change of
  rights, and so it deserves the look.
- The model's verdict decides only between „by itself“ and „a person“ for
  an image the kernel already allows; it can hold, never widen. A wrong
  „normal“ costs a red verify (back by itself) or a morning's
  „Zurücknehmen“; data that migrated come from the snapshot.
- An upstream that moves its tag daily restarts the soak with every digest:
  such an image waits until it holds still for three days.
- The kernel grew by the gate, the switch, the first-seen date, `ready` and
  `laib updates`' lines (22,701 → 22,907; on top of ADR 0036 23,016 → 23,228,
  cap 23,229, the owner's yes for
  the feature); laib-updater by the issue count; the skills `versionen` and
  `morgenbericht` by the judgment and the morning lines.
- `docs/ABLAEUFE.md` L-70 to L-76; ADR 0034's „The owner's choice: (c)“
  holds for what is not routine.
