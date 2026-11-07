# homes — the surface (ADR 0014)

`ghcr.io/mdopp/homes`, source `mdopp/homes`. The family's pages and `/napi`,
the API of the Homes app (`mdopp/homes-android`, a widget client, ADR 0022):
the Box keys under `/napi/box/*` come from Laib. Nothing of the Solaris app
is carried. Port 8650 is published beyond loopback for the Bote's pod
(below); the LAN reaches it only through Caddy.

Kontakt prüfen lassen (owner 2026-11-07, mdopp/homes `kontaktantwort`) is
the one page of Homes without a login, and it has a host and a port of its
own: the route `kontakt.<domain>` (`auth: none`, public) proxies to port
8652, where Homes (`HOMES_KONTAKT_LISTEN`) registers nothing but that page,
`/<token>`. Homes' own host (`www.<domain>`) keeps its one bypass, `/napi/`,
and nothing else. Caddy limits the new host to 30 requests per address in 10 minutes
(caddy-ratelimit, pinned in `caddy/Containerfile`) and a 32 KB body; Homes
limits both again, keeps only a hash of each link and answers every link
that is none with the same page. The name needs its own Strato record, as
every name on this box.

Homes answers at `www.<domain>`, and the domain itself redirects there
(owner 2026-11-07, ADR 0031 amendment): the family's entry is the domain's
own name. `HOMES_PUBLIC_URL` in `homes.container` names the same host as
the first route; `TestHomesPublicURLIsItsRoute` holds the two together.
Its first route is also where Laib sends a login without a target and the
welcome mail (`render.HomesURL`).

Homes was called Haus until 2026-11-03 (ADR 0031, #1021). Its addresses
`haus.<domain>` and `homes.<domain>` are gone since 2026-11-07, no
redirect: a phone paired to either pairs again with `www.<domain>`. What
still carries the old name: the `was:` lines in `laib.yml` that carried the
data and secrets along. The program's own logins were renamed with it (the
owner, 2026-11-03): `homes` in LLDAP/Radicale, Audiobookshelf, Jellyfin and
Home Assistant (each hook renames or replaces the old `haus` one, ADR 0031).

With `HOMES_HA_TOKEN`, an administrator's long-lived token that
home-assistant's post-deploy mints and hands back (#349; nothing to click
in HA), Homes serves the house paths itself from Home Assistant: `ha/call` with the
companion contract's safety rules, `portal/{state,energy,entity-history,active}`
and the live stream that keeps the app's widgets current while the screen
is on (ADR 0022). `ENERGY_FLOW`/`ENERGY_TOTALS` name the energy sensors by
entity id.

The Haushalt (haus#11): Homes serves `/mcp/haushalt` to the Bote, with the
generated `HOMES_HAUSHALT_TOKEN` as its bearer (ADR 0017). The Bote reaches it
on the host's port 8650: Homes publishes 8650 beyond loopback, `lan: false`
keeps the LAN out, and pages, `/napi` and `/mcp` each check their own secret
(#170). Notes are Markdown files in the file share's `notes` folder.
Music uploads go into the share's `musik-eingang` folder, mounted at
`/musik-eingang` and nothing else of the share (#639): Homes writes
`._<name>.part` and renames it when whole, beets imports it within about a
minute, and `laib status beets` (its status file's `letzte`) says per
upload what went in and what was kept back; the contract is in
`templates/beets/README.md`, „Musik-Eingang“.
Calendar and reminders use the household's Radicale account `homes` (#418). The auth
template seeds it in LLDAP with the generated `HOMES_RADICALE_PASSWORD`, and
`RADICALE_USER: homes` is the default. Homes makes the household's calendar
`Haushalt` (`/homes/haushalt/`) and address book (`/homes/haushalt-kontakte/`)
in that account. Radicale's rights let every family account read and write
their entries, but not delete the calendar. On a phone, add it in DAVx⁵ with
your own login: Account → CalDAV → the collection URL
`https://caldav.<domain>/homes/haushalt/` (until ADR 0031 `/haus/haushalt/`:
a phone subscribed there subscribes again). Without the account the Kalender
page says so in one sentence (haus, laib#418) instead of landing on Start.

Podcasts (haus#123, #676): Homes subscribes a podcast by its link in
Audiobookshelf at `HOMES_ABS_URL` with `HOMES_ABS_TOKEN`, the API key of ABS's
user `homes` (an administrator) that audiobookshelf's post-deploy mints and
hands back; without ABS both are empty and Homes shows no podcast page. ABS
keeps each podcast's last three episodes (audiobookshelf's care). Homes asks
ABS for new episodes every 15 minutes and tells each subscriber with
`laib_tell {person, text}` (the homes hand's `tell`, served by laib-haushalt
through bote-notify); it offers the message once `laib_tell` is in its
tools/list.

Discogs (#696, haus#138): one Discogs account per person. The household
registers one Discogs app (discogs.com, Settings, Developers) and the owner
sets its `HOMES_DISCOGS_KEY` (Consumer-Key) and `HOMES_DISCOGS_SECRET`
(Consumer-Secret) on the box page, „Schlüssel hinterlegen“. Each person then
connects their own account in Homes (Mehr → Discogs, OAuth; the way back is
`https://www.<domain>/discogs/zurueck`, behind Authelia like every family
page); Homes keeps those tokens in its data dir for that person alone. The
wish list then reads that person's collection ("habt ihr schon auf Vinyl"),
puts record wishes on their wantlist and marks a wish arrived once the record
is in it. Without key and secret the wish list has no Discogs, all else
stays.

Music wishes (#695): Homes asks Jellyfin at `HOMES_JELLYFIN_URL` with
`HOMES_JELLYFIN_TOKEN` whether a shared song is there already, and every 15
minutes whether an open wish arrived. The token is a user token, not an API
key (Jellyfin's API keys have no levels, each is an administrator): the
Jellyfin user `homes` that media's post-deploy keeps hidden, without
administrator, playback, download or deletion, and with the music libraries
only. Send it as `Authorization: MediaBrowser Token="…"`. Without media the
token is empty: the wish list stays, without „hast du schon“ and without
ticking a wish off by itself.

Gesehen & Gehört (haus#152): the page shows each person the films and series
they played in Jellyfin and the audiobooks they listened to in Audiobookshelf,
and puts a wished title on the Radicale list `liste-filme-buecher`. Reading
another user's played state needs a Jellyfin administrator, so here — and
only here — Homes holds an API key, `HOMES_JELLYFIN_KEY` ("Homes (Gesehen)" in
the dashboard), minted by media's post-deploy; Homes only reads with it. The
books come with `HOMES_ABS_TOKEN` (ABS's user `homes` is an administrator).
Without the key the page shows the books only; without either, no page.

The memory library (haus#13, #195): qmd (github.com/tobi/qmd, MIT) as
released, in the homes pod beside Homes (`homes-qmd`), searching the notes
by words and by meaning. Image `ghcr.io/mdopp/laib-qmd:<qmd version>`
(`qmd/`), CPU only. It serves the Vermittler (#198, `qmd/vermittler.mjs`)
instead of qmd's own server: qmd's 2.8.3 `/query` scores only by rank,
so a nonsense query's top hit scores 1, and Homes could not cut weak
hits. The Vermittler runs the same search on qmd's SDK:
- `POST /query` takes qmd's body (`searches` of type `lex`/`vec`,
  `limit`, `collections`), always without rerank; a plain or expanded
  query, `rerank: true` or any other field is refused with 400. It
  answers qmd's shape plus `vec`, the hit's cosine similarity (0 when
  the hit is outside the vector top list).
- `GET /status` lists each collection's documents and how many are
  embedded; `GET /health` answers while the index is open.
- Only the embed model loads, and it stays loaded: loading takes about
  3 s on the CPU, more than Homes waits. `/health`, and so the HealthCmd
  and `Notify=healthy`, answer only once it is loaded; before the first
  embed there is nothing to load.
- It goes when qmd returns the cosine itself (tobi/qmd#697).

It listens on the pod's loopback only — it has no auth — and Homes
reaches it at `HOMES_QMD_URL=http://127.0.0.1:8181` (like qmd it answers
only a loopback Host, 403 otherwise, so `host.containers.internal` would
not do), collection `HOMES_QMD_COLLECTION=notizen`. `qmd.json` is its
`index.yml`: the collection `notizen` over the notes folder, and
`models.embed` pinned to embeddinggemma-300M (it overrides
`QMD_EMBED_MODEL`). qmd watches no files: `homes-qmd-refresh.timer` runs
`qmd update` and `qmd embed` every quarter hour; a failed step fails
`homes-qmd-refresh.service` and `laib status` shows homes red until a run
succeeds. The first run downloads the model (~330 MB) into
`${DATA_DIR}/homes-qmd`; nothing ever runs `qmd pull`, which deletes the
cached models when offline. That folder is a cache, not backed up, and
lies beside `${DATA_DIR}/homes`, not in it: Homes relabels its own folder
(`:Z`) on every start. `homes-qmd` is active only once it answers a word search, and
is restarted when it stops answering. Serving takes about 700 MB; a
refresh with new notes about 1 GB more for a moment. embeddinggemma comes
under the Gemma Terms of Use, which fit a family's own use.

Documents (#219): the library indexes Paperless's documents too. Homes
mirrors what its Paperless user sees (the OCR text Paperless already
has) into `${DATA_DIR}/homes-dokumente`, one `<id>.md` per document
(title, date, sender, tags, then the text): `homes export-documents` in
Homes's container, as its own oneshot (`homes-documents-export.service`)
that the refresh wants and runs after. A document changed in Paperless
is written again, one deleted there is deleted here; Paperless not
answering leaves the folder as it was. The export failing (a folder Homes
cannot write, or a Homes image older than the command, which stops at
once without `LAIB_TOKEN`) shows homes red but never stops the notes'
refresh.
qmd indexes the folder read-only as the collection `dokumente`
(`HOMES_QMD_DOCUMENTS`); Homes checks each hit against Paperless and links
it to `www.<domain>/dokumente/<id>`. Without `HOMES_PAPERLESS_TOKEN` the
export does nothing and the library is the notes. The folder is a cache
(Paperless is the documents' home), not backed up, and beside
`${DATA_DIR}/homes` and `homes-qmd`, mounted `:z` since both containers use it.

The Dokumente page (haus#68, #521): `www.<domain>/dokumente` shows the
ways into Paperless. *Per E-Mail ablegen* is a mailto to `DOCUMENTS_MAIL`,
the address paperless fetches (its `PAPERLESS_MAIL_FILTER_TO`; empty: no
button). *Dokument ablegen* stores into the consume folder as the app's
upload does (`HOMES_UPLOAD_DIR`). *In Paperless öffnen* goes to
`PAPERLESS_PUBLIC_URL`; `PAPERLESS_LAN_ONLY: "true"` (paperless' route is
internal) adds "nur zu Hause im WLAN" and, from outside the house, a
sentence in place of the link.

Immich for the contacts (homes 2026-11-05): `IMMICH_URL` (Immich as the
pod reaches it, `http://host.containers.internal:2283`), `IMMICH_PUBLIC_URL`
(`https://photos.<domain>`) and `IMMICH_KEY: !secret HOMES_IMMICH_KEY`, an
API key of the owner's Immich account with `person.read`, `person.create`,
`person.update`, `person.merge`, `user.read`, `asset.upload`, `face.read`
and `asset.delete` (Immich keeps its people per user; `person.statistics`
adds photo counts).
The owner sets it on the box page („Schlüssel hinterlegen“). With it a
contact's „Fotos“ opens its Immich person, a card without a picture
reaches the phones with that face, and every Haushalts-Kontakt and each of
the key owner's own Handy-Kontakte gets an Immich person of its name
(homes 2026-11-06): one Immich has of that name, else one Homes makes
without a face (`person.create`) and keeps in step with the contact
(`person.update`). The owner merges a named face into it in Immich;
persons of one name Homes merges itself in its hourly run, and faces
Immich holds close it offers the key's owner on „Dieselbe Person?“
(`person.merge`, homes 2026-11-06). The pictures of the key owner's own
Handy-Kontakte go to Immich as archived photos (`asset.upload`), and the
person Immich gives such a face (`face.read`) is only a question, „Ist
das …?“, merged on the owner's Ja; on a Nein or „Kein Foto“ Homes deletes
its own upload (`asset.delete`, homes#298). The
key's owner (the login whose e-mail is the Immich account's, `user.read`)
opens their own Handy-Kontakte's photos from „Meine Kontakte“. Immich's
people are not offered as contacts. Homes keeps the mapping and the
small faces in `${DATA_DIR}/homes`. Without it „Fotos“ searches Immich by name. All of it
only while Laib runs immich (Homes' module `fotos`).

The house model (#198): with a model on the box (the `model` role, or
`box.yml model.url`) Homes gets `HOMES_LLM_URL` (the proxy's OpenAI base
as the pod reaches it, `${MODEL_URL}`), `HOMES_LLM_MODEL=qwen3.6-35b-a3b` and
the model key as `HOMES_LLM_KEY`, and lets the model tidy the library's
hits, never blocking. Without a model URL and key are empty and the
judge is off.

The Sprechen field (haus#12, #215): Homes gets the Bote's OpenAI-compatible
API as `HOMES_BOTE_URL` (`BOTE_URL`, default
`http://host.containers.internal:8642/v1`) and its key as `HOMES_BOTE_KEY`
(`BOTE_KEY: !secret BOTE_API_KEY`, generated by `laib add bote`). Without
the Bote the key renders empty and Homes shows no field. A Homes service
added before #215 gets both lines from the template update, which adds
every new key of `values.example.yml` to `values.yml`; no hand edit.

1. `laib add homes` mints `HOMES_LAIB_TOKEN` (hand `homes`: status, inbox,
   journal, restart) and generates `HOMES_PROXY_SECRET`, which Caddy sends
   as `X-Homes-Proxy` to the pages — nothing else reaches them (haus#1).
2. Homes decides nothing: the Box page links to `laib.<domain>/inbox`, the
   one approval surface; the phone's approve buttons answer 403.
3. `laib add homes`. To pair a phone, enter `https://www.<domain>` in the
   app: it opens `/pair-device`, the person logs in and confirms, and the
   app is paired (haus#6). `/box/geraete` lists and removes devices.

What the family uses (#266, haus#…): with usage-metrics installed, Homes
gets `HOMES_USAGE_URL` (`host.containers.internal:8094`) and
`HOMES_USAGE_TOKEN` (the `USAGE_METRICS_TOKEN` secret, a podman secret per
#224) and counts server-side, per feature and UTC day, never who: page
views, actions (`/schalten`, ☆, search, Sprechen), the plain "antwortet
gerade nicht" lines it showed, `/napi` refreshes per widget, and taps
tagged `?von=widget.<kind>` by the Homes app. It batches in memory and
flushes every few minutes; usage-metrics down drops the counts and logs
once. Homes also counts voice: Home Assistant's Assist runs, recognized or
not, and the sentence it did not understand (see `templates/home-assistant`).
The Box page's admin card "Was wird genutzt" reads it back: the last 30
days, most and least used, the plain lines, and the sentences the Bote and
voice could not answer. Without usage-metrics the token renders empty and
none of it happens.

