# home-assistant

Home Assistant, Z-Wave JS UI and the Matter server on the host network.
What belongs here from ADR 0015, configured in HA after install: MQTT
for mosquitto. The rest is not clicked together. The Wyoming integrations
for Stimme (Whisper 10300, Piper 10200, openWakeWord 10400 on
`STIMME_HOST`, default `127.0.0.1`): post-deploy adds an entry for each
server that answers, once, found by host and port in HA's
`.storage/core.config_entries` (the REST API does not show an entry's data,
and HA's Wyoming flow would take a server twice); an entry from before on
one of those ports is kept and reloaded when it had given up (#540). An
Assist pipeline part (speech to text, text to speech, wake word,
conversation agent) whose entity is gone or unavailable is pointed at
Stimme's or Frage's when that one works and speaks the pipeline's
language; a part that works, or a legacy provider, stays the owner's
choice. Nothing is deleted: an entry whose server is gone is the owner's to
delete, and the check names it („… antwortet nicht“). Home Assistant
restarting under the apply (`laib add stimme`) is waited for, at most
`HA_WAIT_SECONDS` (300), and the step runs once more; Wyoming entries still
not set up make post-deploy red after its other steps, never green with a
„NOT set up“ line (#557, #588). The CalDAV
integration for Radicale (#495) and Music Assistant (#526, below) come from
post-deploy too. Frage
as the conversation agent is not clicked together: post-deploy adds Home
Assistant's Ollama integration on `FRAGE_URL` (default
`http://127.0.0.1:8710`, Frage speaks Ollama's `/api/tags` and `/api/chat`)
and its conversation agent "Frage" (model `frage`, no HA tools: HA's own
intents answer commands), once Frage answers there (#491). HA 2026.10 has no
built-in integration for an OpenAI server with its own URL; Solaris took the
same path for `conversation.solaris`. Which Assist pipeline uses Frage stays
the owner's choice, unless a pipeline's agent is gone (above); a pipeline
whose agent is Frage gets `prefer_local_intents: true` (ADR 0015, #557):
„Mach Bambi aus“ is HA's intent in about a second, not a web search. Without Frage the hook says so and stays green; `laib
diag` lists „Frage als Gesprächsagent: fehlt“.

## The house persona's Assist pipeline (#634)

One Assist pipeline is Laib's, named after the house persona (box.yml
`persona`, ADR 0020; Joshua: „Joshua“), in the persona's first language:
Frage with `prefer_local_intents` (HA's own agent while Frage is not
there), Stimme's Whisper and Piper (`tts_voice` empty: the voice Piper runs
with, the persona's), and a wake word. Post-deploy creates it once Stimme
answers, sets those parts again when they drifted, makes it preferred, and
points every Assist satellite's „Assistent“ select at it (a Voice PE, found
by HA's translation key `pipeline`; a second slot only when it was on a
pipeline that goes). The persona comes from `render/persona/<name>/persona.json`;
the check's unit carries the persona's name, wake word and phrase, so a
persona change applies this hook again.

- **Wake word.** The persona's (`hey_joshua`) once Stimme's openWakeWord
  offers it (`wake_word/info`); until then the one the preferred pipeline
  had, if openWakeWord offers it; else none, never a wake word nobody can
  say. Stimme's trainer builds a microWakeWord model for the device
  (templates/stimme): once it is flashed and the satellite's
  „Aktivierungswort“ select offers the persona's phrase, post-deploy selects
  it; until then the device keeps its own.
- **End of speech.** `VOICE_VAD` (default `default`; `aggressive` cut sentences at a breath) is every satellite's
  „Sprechpausen-Erkennung“: how much silence ends a sentence — `default`
  0.7 s, `relaxed` 1.25 s, `aggressive` 0.25 s (HA's assist_pipeline/vad.py).
  Empty leaves it as set in HA. Without speech ending, HA records until its
  15 s timeout.
- **The old pipelines.** `VOICE_OLD_PIPELINES` (default `Sol,Solaris`, the
  Solaris era's) are deleted once nothing uses them; one that a select still
  shows, or a file under HA's config names by id (its YAML, `.storage`), is
  renamed „… (alt)“ and the hook says who names it.

The check measures it without switching anything: the pipeline is there and
preferred; HA's local intents recognize „Wie spät ist es“ in its language
(`conversation/agent/homeassistant/debug`: recognized, never executed); the
wake word is the persona's or the status says which one wakes until then;
each satellite is on the pipeline. One line in the status, e.g. „Sprache:
lokale Befehle aktiv (HassGetCurrentTime in 40 ms) · Weckwort nur am Gerät,
bis hey_joshua da ist“. It does not measure the microphone, the end of
speech or Whisper: a spoken test on the device does.

## Its home comes from box.yml zuhause (#561)

The household's address lives once in box.yml (`zuhause`, docs/STATE.md);
the unit carries `${ZUHAUSE_REV}`, so a move applies Home Assistant again,
and post-deploy gets the `ZUHAUSE_*` values. With latitude and longitude
set it compares them, the elevation, the country and box.yml's time zone
with HA's core config (`GET /api/config`) and, where one differs, sets them
through `config/core/update`; HA's home zone follows. The same home twice
is no update. Without coordinates HA keeps the location it has (set in its
onboarding or by hand) and the hook says why: Laib never looks an address
up, the coordinates come from whoever enters the address (Haus's form: a
map pin, or a lookup the person starts knowingly). No value reaches the
hook's log. To change the address: `laib zuhause set …`, a proposal the
owner approves.

## The tokens Laib mints (#349), each on its own user (#591)

Nobody clicks a long-lived token together in HA's UI. post-deploy logs in
through Home Assistant's own login flow as its administrator, makes one HA
user per consumer, mints that consumer's long-lived token over HA's
WebSocket API (`auth/long_lived_access_token`) and hands it back into
`secrets.age` (`laib-secret:`). A machine never switches as a person: HA's
logbook says „Haus hat … eingeschaltet“, not the name of whoever onboarded
HA (#591). HA mints a token only for the user of the session that asks, so
each of these users has a homeassistant login with a generated password
that only post-deploy uses:

| secret | HA user (login, password) | administrator | for |
|---|---|---|---|
| `HA_TOKEN` | „Laib-Prüfung“ (`laib-pruefung`, `HA_CHECK_PASSWORD`) | no | the checklist below: config entries and states, which every user may read |
| `HAUS_HA_TOKEN` | „Haus“ (`haus`, `HA_HAUS_PASSWORD`) | yes: voice counting reads Assist's debug runs, admin-only (below) | Haus's pages and widgets, voice counting |
| `BOTE_AUTOMAT_HA_TOKEN` | „Bote-Automat“ (`bote-automat`, `HA_BOTE_AUTOMAT_PASSWORD`) | yes: HA's automation config API is admin-only | the Bote's Automat; every write it makes needs the admin's approval code |
| `BOTE_HA_TOKEN` | „Bote“ (`bote`, `HA_BOTE_PASSWORD`) | no | the Bote's `ha` tool, on HA's Assist API: whoever chats with the Bote reaches no admin API |
| `MUSIC_ASSISTANT_HA_TOKEN` | „Music Assistant“ (`music-assistant`, `HA_MUSIC_ASSISTANT_PASSWORD`) | yes: MA reads HA's users (`config/auth/list`) for the family's login | Music Assistant's Home Assistant plugin; only once Music Assistant is there |
| `STIMME_HA_TOKEN` | „Stimme“ (`stimme`, `HA_STIMME_PASSWORD`) | no | Stimme's `stimme-names`: the entity, area and floor registries, which every user may list, for the names Whisper hears |

Each token is named `laib: <consumer>` under its user's Security tab and
revocable on its own. A user that is missing is created through HA's API
(`config/auth/create`, `config/auth_provider/homeassistant/create`); one
someone moved into the other group is moved back on the next apply.

On every apply: a token that works, on its own user and of its kind, is
kept; any other is replaced. The apply that mints also restarts what uses
the tokens (Laib re-applies every service that declares a handed-back
secret, #349): Haus and the Bote have theirs in the same apply, and Home
Assistant restarts once, for the check's unit. A consumer added later finds
its token already there. Tokens and passwords are only ever on the
`laib-secret:` lines, never on argv or in a log line (#214).

**Moving a box from before #591.** There `HA_TOKEN`, `HAUS_HA_TOKEN`,
`BOTE_AUTOMAT_HA_TOKEN` and `MUSIC_ASSISTANT_HA_TOKEN` live on the
administrator, a person's user. Nothing goes dark on the way:

1. post-deploy creates the four users and mints a new token on each; the
   old token still works and is handed back in `HA_RETIRED_TOKENS`, the new
   one in its secret. Music Assistant's plugin gets the new one at once.
2. Laib stores both and re-applies the consumers (Bote, Haus) with the new
   tokens, then Home Assistant, whose post-deploy runs again.
3. That run finds each new token working on its own user and revokes the
   old one it replaced, with the old token's own session
   (`auth/refresh_tokens` → the current one → `auth/delete_refresh_token`),
   and only when its name is `laib: …`: a token Laib did not mint is left
   to the owner. `HA_RETIRED_TOKENS` is handed back empty.

A new token that does not work yet keeps its old one retired until a later
apply verifies it. Re-running changes nothing.

**Who else is in Home Assistant** is one line of post-deploy's output (the
apply's detail): every user that is not one of the machines above, whether
it is the owner or an administrator, whether it is `HA_ADMIN_USER`, and the
names of its long-lived tokens, read from HA's `.storage/auth` (names only).
Users from before (ServiceBay's or Solaris' administrator and its bridge
token, a second user of a person's name) show there; nothing is deleted,
that is the owner's decision.

**The administrator login.** `HA_ADMIN_USER` (value, default `laib`) with
`HA_ADMIN_PASSWORD` (secret):

- A fresh Home Assistant: post-deploy onboards it as that user with the
  generated password (the onboarding API: user, core config, analytics,
  integrations; time zone from the box), so the first start needs no
  wizard. The family signs in with "Login with Familienkonto" (below); the
  account `laib` is Laib's. Location and country are HA's settings
  (Settings → System → General).
- A box from ServiceBay: ServiceBay onboarded HA as `OSCAR_HA_ADMIN_USERNAME`
  (`oscar`) with a generated `OSCAR_HA_ADMIN_PASSWORD`; migration day
  carried the password as `HA_ADMIN_PASSWORD` and set `HA_ADMIN_USER=oscar`.
- Neither (HA onboarded by hand): set `HA_ADMIN_USER` to an HA
  administrator and type its password once, `laib secret set
  HA_ADMIN_PASSWORD`. Until then the apply is red and says so.

Once the tokens exist the password is used only when no administrator
token works any more: a password changed in HA later does not lock Laib
out.

## Music Assistant, both ways (#526)

music-assistant's post-deploy onboards Music Assistant (its built-in
administrator `laib` is break-glass), adds Jellyfin and Radio Browser and
hands back `HA_MUSIC_ASSISTANT_TOKEN`, a long-lived MA token. This template
declares it and renders it into the check's unit, so the same apply
re-applies Home Assistant (one restart), and this post-deploy then

- connects MA's **Home Assistant plugin** with `MUSIC_ASSISTANT_HA_TOKEN`
  on `https://home.<domain>` (`HA_PUBLIC_URL`). Not `127.0.0.1:8123`:
  that plugin's URL is where MA sends a browser for "Mit Home Assistant
  anmelden", so the family signs in Authelia → Home Assistant → Music
  Assistant, and MA makes their user on the first login (HA
  administrators become MA administrators);
- adds the **Music Assistant integration** on `http://127.0.0.1:8095`
  (`MA_URL`): its config flow's browser step takes the token as the code
  MA's login page would return, and HA exchanges it for its own
  long-lived MA token. An entry that asks to sign in again (MA's tokens
  last a year) is answered the same way on the next apply.

Without Music Assistant the hook says so and stays green. The checklist
below turns yellow until Music Assistant is onboarded, has its Jellyfin
source with titles, knows at least one player and has the plugin.

## The checklist on the Box page (#55, #309)

With `HA_TOKEN`, `home-assistant-check.service` reads the config entries and
writes `.laib-status.json` into HA's config folder: which of the
integrations above are still missing. The Box page and `laib diag` show it
with its age ("geprüft vor 12 min"). Looking at either re-checks a result
older than five minutes in the background, the Box page has "jetzt prüfen",
and a timer re-checks once an hour so the morning report reads a fresh one.

## Updates: extensions and firmware (#528)

The same check reads Home Assistant's `update.*` entities that are on.
Each extension (HACS, a custom integration) is an info finding `Update:
<name> <installed> → <latest>` with its release notes; the installable ones
together are one finding whose fix is a value proposal, `HA_INSTALL=
update.x=<version>,…`. The Operator's weekly `aktualisierung` files it as it
stands; the review makes it a person's decision (a service with a
database), Laib snapshots Home Assistant before the approval applies, and
post-deploy installs each extension still offered at that version, then
restarts Home Assistant once. The snapshot stops Home Assistant, so the
hook first waits (`HA_WAIT_SECONDS`) until HACS has set its update entity
up again: before, `/api/states/<entity>` is 404, and the first version of
this read that as "installiert: None, angeboten: None". It waits again
until HACS has downloaded the extension (HA's service call returns
before), then checks that every custom integration lies where Home
Assistant loads it, `custom_components/<domain>/manifest.json`: HACS 2.0.5
unpacked marq24/ha-senec-v3 one level too deep (`custom_components/senec/
senec/`, a dummy `__init__.py` on top, "Integration not found"). One whose
manifest lies one level down is moved up, on every apply, and what lay on
top is kept in `.laib-<domain>-sicherung/<time>/`; one with no manifest at
all, an entity Home Assistant never shows and a download that does not end
make the hook red, never a quiet "nichts zu installieren". A manifest with
another version than the approved one is said (HACS downloads what it
thinks newest). The check names a folder without a manifest as a finding
too, so an update from Home Assistant's own page is not missed. Device firmware is `Firmware: …`, reported
only: a failed flash can brick a device, so it is installed only on an
explicit yes, by hand in Home Assistant. Both appear in laib-notify's weekly
"Was ist veraltet". Home Assistant's own image (`stable`) moves by a
proposal pinned to the registry's digest (`laib updates`).

## The family's login (#253)

"Login with Familienkonto" signs a family member in through Authelia with
their LLDAP account, as on ServiceBay; Home Assistant's own login stays as
the fallback. pre-start installs `christiaangoossens/hass-oidc-auth` from the
tag archive pinned in `values.yml` (`AUTH_OIDC_ARCHIVE`, `AUTH_OIDC_SHA256`: an
update is a values change) and writes `packages/laib_auth_oidc.yaml`, which
Laib owns whole: client `homeassistant`, users linked by name, a person
created per user, `admins` → administrator, `family` → user. The client
secret is not in it: the unit hands it over as `AUTH_OIDC_SECRET`, so a
rotation restarts Home Assistant with the new one. A top-level `auth_oidc:`
block of your own in `configuration.yaml` would be a duplicate key beside the
package: leave the login to the package.

## Failed logins and HA's own ban (#703)

Home Assistant logs every failed login ("Login attempt or request with
invalid authentication from … (<address>)", no user name) and every ban it
sets, in its unit's journal; the auth template's counter reads them and
tells the owner (templates/auth/README.md, "Anmeldungen melden"). That
needs nothing here: HA logs the line whether or not it bans.

HA's own ban is `HA_LOGIN_SPERRE` in values.yml: a number bans an address
after that many failed logins, `aus` never, empty (the default) leaves HA
as it is. It needs the proxy trust below (HA would otherwise see Caddy for
every login from outside and ban everyone), and post-deploy sets both in
one go.

## Caddy is a trusted proxy (#724)

Caddy, on the host, forwards `home.<domain>` to `127.0.0.1:8123` with
`X-Forwarded-For`. HA answers a forwarded request from a proxy it does not
trust with 400, and without `use_x_forwarded_for` logs every client as
127.0.0.1. Since 2026.8 HA keeps its HTTP settings (`use_x_forwarded_for`,
`trusted_proxies`, `ip_ban_enabled`, `login_attempts_threshold`) in its own
store (`.storage/http`) and ignores a YAML `http:` block, so post-deploy
sets them through HA's `http/config`, `http/config/configure` and
`http/config/promote` (the commands ServiceBay set the proxy trust with, its
#2573). HA restarts into a new HTTP config on trial and reverts an
unpromoted one after five minutes; post-deploy waits for it and promotes.

Every apply reads the store. When HA already reads `X-Forwarded-For` and
trusts 127.0.0.1, nothing is written and HA is not restarted; the owner's
own entries stay (a box from ServiceBay keeps its 127.0.0.1/32 and the
private ranges ServiceBay added). Otherwise post-deploy turns
`use_x_forwarded_for` on and adds `127.0.0.1/32` and `::1/128`, nothing
wider: HA is on the host network, and trusting a LAN range would let any
device there claim another address in `X-Forwarded-For`. Where it cannot
set them the apply is red after the other steps ran.

**Why it is off by default:** HA bans by address, and the box's own
consumers (the check, Haus, the Bote, Stimme) all come from 127.0.0.1. A
token that went stale makes the same failed-login line; after the threshold
HA bans 127.0.0.1, and all of them lose Home Assistant until someone
deletes the line in `ip_bans.yaml` and restarts HA. The counter tells the
owner of such a ban at once. Turning it on is the owner's call.

## Z-Wave JS and Matter stay off the LAN (#231)

Z-Wave JS's server websocket (3000; 3001 on a box migrated from
ServiceBay) and the Matter server's (5580) have no login: whoever reaches
them includes and excludes nodes, reads the network keys and opens locks.
They are declared `lan: false`, so `laib.nft` drops them on the wired and
wireless interfaces; Z-Wave JS UI itself listens on loopback
(`HOST=127.0.0.1`), Caddy serves it as `zwave.<domain>` behind Authelia.
Home Assistant is on the host network too and reaches the servers on
loopback, which the drop does not touch: the Z-Wave JS integration's URL
is `ws://localhost:3000` (or `:3001`), the Matter one's
`ws://localhost:5580/ws`.

## The Z-Wave stick under SELinux, and when it is red (#417)

`home-assistant-zwave.container` passes the stick (`ZWAVE_DEVICE`) with
`AddDevice=` and `SecurityLabelDisable=true`: under SELinux enforcing a
`container_t` process may not open a USB serial device, and with the label
on the stick gives `EACCES` while the node is `crw-rw-rw-`. ServiceBay ran
this container privileged; disabling the label is the narrower switch.

`home-assistant-check` asks zwave-js-ui's `/health/zwave` every 15
minutes (and when someone looks). Without a ready controller it writes a
red finding ("Z-Wave: kein Controller …") and the check unit fails, so
`laib status` shows home-assistant red. Home Assistant answering 200 says
nothing about the radio.

## Messages to people (#177, ADR 0022 §3)

pre-start writes `packages/laib_mitteilung.yaml` with
`rest_command.mitteilung`, and adds the packages include to
`configuration.yaml` once: under an existing plain `homeassistant:` block,
or as one; a `packages:` that points elsewhere is red, with the line to add.
An automation's action is then

```yaml
action: rest_command.mitteilung
data: {an: mama, text: "Die Waschmaschine ist fertig."}
```

It reaches the Bote's `bote-notify` on `127.0.0.1:8645` (`BOTE_NOTIFY_URL`)
with the bearer from `BOTE_NOTIFY_AUTH` in the container's environment,
rendered from `BOTE_NOTIFY_TOKEN`; the Bote generates that token, and
without the Bote the call is refused. On a fresh box pre-start writes Home
Assistant's default `configuration.yaml` with the include before the first
start, so both packages load at once.

## Voice: what Assist understood, and what not (#266)

Counted by Haus, not by a custom component or an automation: Home
Assistant keeps the last 10 Assist pipeline runs per pipeline in memory
(`assist_pipeline/pipeline_debug/list|get` on its WebSocket API; verified
in HA core `homeassistant/components/assist_pipeline`, 2026-10-16), each
with the sentence (`intent-start` `intent_input`), whether HA's own intents
handled it (`intent-end` `processed_locally`) and the response type
(`error`, e.g. `no_intent_match`). Haus reads them every minute with
its `HAUS_HA_TOKEN` and counts into usage-metrics (app `stimme`):
`erkannt` (HA's own intents), `weitergereicht` (answered by the fallback
conversation agent) and `nicht-erkannt` (an error); the sentence of a
`nicht-erkannt` run goes to `/miss` (`source: stimme`) without device,
satellite or conversation id, kept 30 days.

**What you switch on in Home Assistant:** nothing new. Those debug calls are
admin-only; `HAUS_HA_TOKEN` is the user „Haus“'s, an administrator, minted
above (#349, #591). A token set by hand that is not that user's is replaced
on the next apply. Known limits: more than 10 commands on one pipeline
within a minute, or an HA restart in between, lose those runs' counts.

## Who switched: the person behind an action (#591)

HA's logbook names the user of the token (`context.user_id`): with the
tokens above that is „Haus“, „Bote“ or „Bote-Automat“, never a person. Who
was signed in to Haus, wrote to the Bote or spoke to a satellite is
recorded once, in Laib's journal, by the hand that acted, with HA's context
id so the logbook row and the journal row meet:

```
laib_note  kind: watch
           text: "ha person=<user> service=<domain>.<service> entity=<entity_id>[,<entity_id>…] context=<HA context id>"
```

- `person`: Haus's `Remote-User` (Authelia), the Bote's person id from
  box.yml people; `-` when nobody is known (a voice satellite).
- `context`: the `context.id` HA returns for the call: the WebSocket
  `call_service` result carries `{"context": {"id": …}}`; over REST, each
  state in the answer of `POST /api/services/<domain>/<service>` carries
  it. HA's logbook and `GET /api/logbook` show the same id per row.
- Only what a person asked for; nothing for reads, nothing for the
  Automat's own triggers (HA's automation is the actor there).

The journal row is `actor=haus` (or `bote`) by the token that wrote it,
`verb=note`, `reason=watch`; `laib journal` lists it. The haus hand may
`note` from this release on (`state.DefaultPolicy`); a box from before gets
that verb as one policy proposal the owner approves (#575). The Bote's hand
has no verb list and may note already.

Who writes it:

- **Haus**, for a signed-in person's call (mdopp/haus).
- **The Bote** (Signal, Telegram), with its plugin `laib-journal` (bote
  README, „Who switched“). `person` is the box.yml people name whose
  `signal:`/`telegram:` id wrote the turn, else `-`. `service` is
  `intent.<HassName>` for HA's Assist intents (`intent.HassTurnOn`), since
  the Bote calls intents, not services; for the Automat's
  `automation_schalten` it is `automation.turn_on|turn_off`; a sentence
  HA's own intents switched before the model (#520, `laib-persona` sends
  every message to `/api/conversation/process` first) is
  `conversation.process`. `context` is
  read from the entity's state when it changed in the last 30 s, else `-`,
  because HA's MCP answer has no context id.
- **Voice**: a satellite talks to HA's Assist pipeline, not to the Bote,
  so nothing notes it yet. Haus's Sprechen field reaches the Bote over its
  API server and is noted as `person=-`.

Why not one HA user per resident whose token Haus uses: HA mints a token
only for the session's own user, and residents sign in through
Familienkonto (auth_oidc) without a password; Laib would have to give each
person's account a second login it holds, residents who are HA
administrators would hand Haus administrator tokens in their name again,
and the per-person tokens would be a secret that changes with people. The
journal row reaches Haus, the Bote and voice alike and needs no new
identity in HA.
