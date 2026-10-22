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
delete, and the check names it („… antwortet nicht“). The CalDAV
integration for Radicale (#495) and Music Assistant (#526, below) come from
post-deploy too. Frage
as the conversation agent is not clicked together: post-deploy adds Home
Assistant's Ollama integration on `FRAGE_URL` (default
`http://127.0.0.1:8710`, Frage speaks Ollama's `/api/tags` and `/api/chat`)
and its conversation agent "Frage" (model `frage`, no HA tools: HA's own
intents answer commands), once Frage answers there (#491). HA 2026.10 has no
built-in integration for an OpenAI server with its own URL; Solaris took the
same path for `conversation.solaris`. Which Assist pipeline uses Frage stays
the owner's choice, unless a pipeline's agent is gone (above). Without Frage the hook says so and stays green; `laib
diag` lists „Frage als Gesprächsagent: fehlt“.

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

## The tokens Laib mints (#349)

Nobody clicks a long-lived token together in HA's UI. post-deploy logs in
through Home Assistant's own login flow as its administrator, mints one
long-lived token per consumer over HA's WebSocket API
(`auth/long_lived_access_token`) and hands each back into `secrets.age`
(`laib-secret:`); HA lists them under that user's Security tab by name:

| secret | user | for |
|---|---|---|
| `HA_TOKEN` | the administrator (`laib: home-assistant-check`) | the checklist below; it runs inside HA's container, which holds HA's whole config anyway |
| `HAUS_HA_TOKEN` | the administrator (`laib: haus`) | Haus's pages and widgets, and voice counting (admin-only debug calls, below) |
| `BOTE_AUTOMAT_HA_TOKEN` | the administrator (`laib: bote-automat`) | the Bote's Automat: HA's automation config API is admin-only; every write the Automat makes needs the admin's approval code |
| `BOTE_HA_TOKEN` | **`bote`**, no administrator (`laib: bote`) | the Bote's `ha` tool, on HA's Assist API: whoever chats with the Bote reaches no admin API |
| `MUSIC_ASSISTANT_HA_TOKEN` | the administrator (`laib: music-assistant`) | Music Assistant's Home Assistant plugin (it reads HA's users for the family's login into MA); minted only once Music Assistant is there |

Two administrator tokens instead of one for Haus and the check: each is
revocable on its own and HA names who holds it; it costs nothing. The user
`bote` is created through HA's API when it is missing (group "Users"), with
the generated `HA_BOTE_PASSWORD`; if someone makes it an administrator, the
next apply takes that back and replaces its token.

On every apply: a token that works and is of its kind is kept; one HA
refuses (revoked, or a box restored from an older HA) is replaced; an
administrator's token in `BOTE_HA_TOKEN` is replaced by the Bote's. The
apply that mints also restarts what uses the tokens (Laib re-applies every
service that declares a handed-back secret, #349): Haus and the Bote have
theirs in the same apply, and Home Assistant restarts once, for the check's
unit. A consumer added later finds its token already there. Tokens and
passwords are only ever on the `laib-secret:` lines, never on argv or in a
log line (#214).

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
restarts Home Assistant once. Device firmware is `Firmware: …`, reported
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
rotation restarts Home Assistant with the new one. On a box from ServiceBay,
its top-level `auth_oidc:` block leaves `configuration.yaml` (Home Assistant
would keep it over the package, with ServiceBay's secret); the users it
linked stay linked.

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
admin-only; `HAUS_HA_TOKEN` is the administrator's, minted above (#349). A
token set by hand that is not an administrator's is replaced on the next
apply. Known limits: more than 10 commands on one pipeline
within a minute, or an HA restart in between, lose those runs' counts.
