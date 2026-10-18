# home-assistant

Home Assistant, Z-Wave JS UI and the Matter server on the host network.
What belongs here from ADR 0015, configured in HA after install: the
Wyoming integrations for Stimme (whisper, piper/Martin, openWakeWord), the
CalDAV integration for Radicale, Music Assistant, MQTT for mosquitto, and
Frage as the conversation agent fallback.

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
  (`oscar`) with a generated `OSCAR_HA_ADMIN_PASSWORD`;
  `scripts/carry-servicebay-secrets.sh` carries the password as
  `HA_ADMIN_PASSWORD`, `migrate-servicebay.py` puts `HA_ADMIN_USER=oscar` on
  the `laib add` line.
- Neither (HA onboarded by hand): set `HA_ADMIN_USER` to an HA
  administrator and type its password once, `laib secret set
  HA_ADMIN_PASSWORD`. Until then the apply is red and says so.

Once the tokens exist the password is used only when no administrator
token works any more: a password changed in HA later does not lock Laib
out.

## The checklist on the Box page (#55, #309)

With `HA_TOKEN`, `home-assistant-check.service` reads the config entries and
writes `.laib-status.json` into HA's config folder: which of the
integrations above are still missing. The Box page and `laib diag` show it
with its age ("geprüft vor 12 min"). Looking at either re-checks a result
older than five minutes in the background, the Box page has "jetzt prüfen",
and a timer re-checks once an hour so the morning report reads a fresh one.

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
