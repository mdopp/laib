# ADR 0031 — Haus is renamed Homes: program, app and assistant share one name

Applies when you meet „Haus“ or `haus` in code, templates, docs or on the
box and wonder whether it is the family's program (now Homes) or the
ordinary German word; when you move Homes' address (`www.<domain>` since
the amendment at the end) or meet the gone names `homes.<domain>` and
`haus.<domain>`; when you touch a `was:` line, the device-token prefix
`haus_device_` or archiv's `X-Archiv-Haus` fallback; or when you want to know whether one of them
may go yet.

- **Status:** Accepted, 2026-11-03 (#1021). Amends ADR 0021 rules 1 and 3
  for this one service and ADR 0030 rule 1.
- **Deciders:** the owner (mdopp), 2026-11-03: the program „Haus“ becomes
  **Homes**, holistically: the pages, the app, the repos, the image, the
  template, the hand and every German text that names the product. The
  app's id changes too (`cloud.dopp.haus` → `cloud.dopp.homes`), accepted
  with its cost: every family phone installs the new app, pairs it again
  and places its widgets again. The same day, option (b) in #1021: the
  program's own logins in the other apps are renamed `homes` too.

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
5. **The program's own logins are renamed too** (the owner, 2026-11-03,
   #1021 option b), each the way that keeps the most:
   - LLDAP/Radicale: the account `homes` (the same password, by `was:`)
     owns the household's calendars; Radicale's pre-start moves the
     collections `haus/` to `homes/` once (entries, UIDs and props along;
     both holding something is a red apply, never a merge), the rights name
     `homes`, and auth's post-deploy deletes the LDAP user `haus`. A stored
     calendar subscription for person `haus` is read as `homes`.
   - Audiobookshelf and Jellyfin: the user is renamed in place through
     their APIs; the id, what it played and its key stay. A key named
     „Haus (laib)“ / „Haus (Gesehen)“ counts as Homes's when one is
     replaced: neither app can rename a key.
   - Home Assistant: a new user `homes` („Homes“) with a new password and
     a new `HOMES_HA_TOKEN` (no `was:`); once it works, post-deploy deletes
     the user `haus` and its tokens with it. HA cannot rename a login.
   - Kept: Home Assistant's copies of the household's calendars stay at
     `homeassistant/haus-<collection>`, because HA's calendar entities are
     bound to the copy's path; a Paperless login is the owner's.

## Legacy names and when they end (ADR 0021 rule 8)

| Name | Where | Ends when |
|---|---|---|
| `haus.<domain>` | gone 2026-11-07 with `homes.<domain>` (amendment below) | ended |
| `haus_device_` | the program accepts a paired phone's old tokens | the same |
| `was:` lines | templates/homes, bote, scan, auth, archiv, audiobookshelf, media, home-assistant | the box moved (#1021 step 7) |
| `X-Archiv-Haus`, `ARCHIV_HAUS_SECRET` | archiv reads them beside the new names | the box moved |
| the hand `haus`'s token record | the state's tokens | revoked at #1021 step 9 |
| `X-HAUS-*` iCal properties, PRODIDs | entries already stored in Radicale | kept: stored data, not a name in code |
| the LDAP user `haus` deleted, `haus` kept reserved | templates/auth post-deploy | #1021 step 9 |
| the HA user `haus` deleted once `homes` works | templates/home-assistant post-deploy | #1021 step 9 |
| `haus/` moved to `homes/` | templates/radicale pre-start | #1021 step 9 |
| a subscription for person `haus` read as `homes` | `laib calendar`, kalender.py | #1021 step 9 |
| the user `haus` renamed `homes`, the keys „Haus (laib)“, „Haus (Gesehen)“ | audiobookshelf, media post-deploy | #1021 step 9 |
| `homeassistant/haus-<collection>` | HA's copies of the household's calendars (kalender.py) | kept: HA's entities hang on the path |

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
- A phone subscribed in DAVx⁵ to `/haus/haushalt/` (or another collection
  of the household's account) subscribes again under `/homes/…`; the
  entries are the same, moved, not copied.
- Home Assistant's Homes user is new: its logbook says „Homes“ from now on,
  and the old user „Haus“ with its entries' attribution is gone.
- The usage counts keep the app id `haus` the program has always sent:
  the history in usage-metrics stays one series.

## Amendment 2026-11-07: Homes moves to `www.<domain>`

- **Deciders:** the owner, 2026-11-07: Homes answers at `www.<domain>`, the
  domain itself redirects there, and `homes.<domain>` goes at once, with no
  redirect („Sofort ganz weg“).
- **Decision.** The homes template's first route is `www` with
  `redirectFrom: ["@"]`; it keeps everything `homes.` had: one factor for
  `family` and `admins`, `/napi/` past Authelia, the proxy secret.
  `HOMES_PUBLIC_URL` names the same host (`TestHomesPublicURLIsItsRoute`).
  Laib's own links to Homes (a login without a target, the welcome mail)
  follow the route by themselves (`render.HomesURL`), and Authelia's cookie
  is the domain's, so nobody signs in again. `haus.<domain>` goes in the
  same change: the old app's `/napi/` calls there stopped on 2026-11-03,
  and since then every paired phone calls `homes.`; no `redirectFrom` is
  left for it either.
- **Consequences.** A phone paired to `homes.<domain>` cannot reach Homes
  once the change is applied: the app keeps its address and does not
  follow a move, so it is unpaired and paired again with `www.<domain>`.
  The contacts' links in the Homes account follow by themselves on the
  next sync (Homes makes them from `HOMES_PUBLIC_URL` when it serves them).
  Caddy has no site for `homes.` or `haus.` any more and renews no
  certificate for them; their Strato records point nowhere useful and
  can be deleted.
