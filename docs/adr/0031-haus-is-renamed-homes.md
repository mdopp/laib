# ADR 0031 — Haus is renamed Homes: program, app and assistant share one name

Applies when you meet „Haus“ or `haus` in code, templates, docs or on the
box and wonder whether it is the family's program (now Homes) or the
ordinary German word; when you touch the legacy route `haus.<domain>`,
a `was:` line, the device-token prefix `haus_device_` or archiv's
`X-Archiv-Haus` fallback; or when you want to know whether one of them
may go yet.

- **Status:** Accepted, 2026-11-03 (#1021). Amends ADR 0021 rules 1 and 3
  for this one service and ADR 0030 rule 1.
- **Deciders:** the owner (mdopp), 2026-11-03: the program „Haus“ becomes
  **Homes**, holistically: the pages, the app, the repos, the image, the
  template, the hand and every German text that names the product. The
  app's id changes too (`cloud.dopp.haus` → `cloud.dopp.homes`), accepted
  with its cost: every family phone installs the new app, pairs it again
  and places its widgets again.

## Context

ADR 0030 named the assistant „Homes“ (the plural of home, spoken like
Holmes). The program the family looks at was still „Haus“: one family
saw two names for one thing — „Gemeinsam mit Homes“ on a page of Haus,
Homes answering in the Haus app. „Haus“ is also an ordinary German word
(im ganzen Haus, außer Haus, die Kontakte des Hauses, the energy flow's
„Haus“ node), so a grep for the product found the house as often as the
program, and ADR 0021 rule 1 („one name, one thing“) held only by
reading every hit.

What moved, and where it was:

| What | Before | Now |
|---|---|---|
| repos | `mdopp/haus`, `mdopp/haus-android` | `mdopp/homes`, `mdopp/homes-android` |
| image, binary | `ghcr.io/mdopp/haus`, `/haus` | `ghcr.io/mdopp/homes`, `/homes` |
| app id | `cloud.dopp.haus` | `cloud.dopp.homes` |
| address | `haus.<domain>` | `homes.<domain>` (`@` and `www.` lead there) |
| template, service, units, pod | `haus`, `haus-qmd`, `haus-check`, … | `homes`, `homes-qmd`, `homes-check`, … |
| data | `${DATA_DIR}/haus{,-qmd,-dokumente}` | `${DATA_DIR}/homes{,-qmd,-dokumente}` |
| env, secrets | `HAUS_*`, `ARCHIV_HAUS_SECRET` | `HOMES_*`, `ARCHIV_HOMES_SECRET` |
| proxy header | `X-Haus-Proxy` | `X-Homes-Proxy` |
| hand, `provides` | `haus` | `homes` |
| hook builtin | `LAIB_HAUS_URL` | `LAIB_HOMES_URL` |

## Decision

1. **The program, the app and the assistant share one name, Homes.**
   For this one service ADR 0021 rule 3 („a service is one German noun“)
   gives way: Homes is the template, pod, data dir, secret prefix and
   hand. Rule 1 holds again without reading context: `homes` is the
   program, `Haus` is the house.
2. **The program's name is fixed.** ADR 0030 rule 1 keeps box.yml
   `assistant` as the one place for the name the family talks to; it
   names the assistant (the Bote, Stimme's pipeline, the greeting), not
   the program, its address or its app, which stay Homes when the
   assistant is renamed.
3. **The box moves in one proposal** (#1021 step 7): `services/haus`
   goes and `services/homes` comes, with the owner's values; policy.yml's
   hand `haus` becomes `homes`; every other service that declares a
   renamed secret takes its new template. Apply stops the old units,
   renames the data dirs and copies the secrets by `was:` (STATE.md „A
   renamed service takes its data and secrets along“), mints a new token
   for the hand `homes` (the old one belongs to the hand `haus` and is not
   carried), starts Homes and re-renders Caddy and Authelia.
4. **The old app keeps working until each phone switched.** The route
   `haus.<domain>` stays as a second route of the homes service: same
   port, `/napi/` bypassing Authelia, the same proxy secret. Not a
   `redirectFrom`: OkHttp drops `Authorization` on a cross-host redirect,
   so a Caddy redirect would log the old app out. The program itself
   308-redirects every other request on that host to `homes.<domain>`,
   accepts a paired phone's `haus_device_` token, and tells the old app
   (`GET /napi/app`) where to get Homes.
5. **What is the household's keeps its name.** The accounts named `haus`
   in Radicale/LLDAP (the household's calendar and address book,
   `/haus/haushalt/`), Audiobookshelf, Jellyfin, Paperless and Home
   Assistant's user „Haus“ are the household's logins the program uses,
   not the program; renaming them moves the household's calendar and makes
   every phone subscribe again. They stay until the owner decides
   otherwise (#1021, „Owner decision needed“). So do the labels a hook
   finds its key by (Audiobookshelf's „Haus (laib)“, Jellyfin's „Haus
   (Gesehen)“ and the device `laib-haus`): renamed, the hook would mint a
   second key next to the first.

## Legacy names and when they end (ADR 0021 rule 8)

| Name | Where | Ends when |
|---|---|---|
| `haus.<domain>` | the homes template's second route | every phone runs the Homes app (#1021 step 9); then it becomes a `redirectFrom` |
| `haus_device_` | the program accepts a paired phone's old tokens | the same |
| `was:` lines | templates/homes, bote, scan, auth, archiv, audiobookshelf, media, home-assistant | the box moved (#1021 step 7) |
| `X-Archiv-Haus`, `ARCHIV_HAUS_SECRET` | archiv reads them beside the new names | the box moved |
| the hand `haus`'s token record | the state's tokens | revoked at #1021 step 9 |
| `X-HAUS-*` iCal properties, PRODIDs | entries already stored in Radicale | kept: stored data, not a name in code |
| the accounts named `haus`, the hook labels above | Radicale/LLDAP, ABS, Jellyfin, Paperless, HA | the owner's decision in #1021 |

`Haus` does not go on ADR 0021's retired names: it is an ordinary German
word, and the docs check would flag the house.

## Consequences

- One name in front of the family: the pages, the app and the assistant
  all say Homes.
- Each phone installs Homes, pairs it and places its widgets again; the
  old app says so itself before it stops being needed.
- The Bote keys the Sprechen field's session as `homes:<login>`, so that
  conversation starts fresh once.
- Restic snapshots from before the move hold `${DATA_DIR}/haus*`;
  `laib restore … homes` reads the new paths, so an older snapshot is
  restored by its restic path by hand until those snapshots age out.
- `${DATA_DIR}/haus-check` is not carried (it is written again every 15
  minutes) and stays on disk until removed by hand.
- The usage counts keep the app id `haus` the program has always sent:
  the history in usage-metrics stays one series.
