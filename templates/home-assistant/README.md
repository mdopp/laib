# home-assistant

Home Assistant, Z-Wave JS UI and the Matter server on the host network.
First run: the onboarding in the UI (ServiceBay automated it with 2,000
lines of post-deploy; Laib does not — once per box is a human's five
minutes). What belongs here from ADR 0015, configured in HA after
install: the Wyoming integrations for Stimme (whisper, piper/Martin,
openWakeWord), the CalDAV integration for Radicale, Music Assistant,
MQTT for mosquitto, and Frage as the conversation agent fallback. The
hass-oidc-auth custom component for Authelia login is optional and not
part of the template.

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

The post-deploy hook writes `packages/laib_mitteilung.yaml` with
`rest_command.mitteilung` and, when `configuration.yaml` has neither
`homeassistant:` nor `packages:`, appends the packages include once (else
it prints the line to add by hand). An automation's action is then

```yaml
action: rest_command.mitteilung
data: {an: mama, text: "Die Waschmaschine ist fertig."}
```

It reaches the Bote's `bote-notify` on `127.0.0.1:8645` (`BOTE_NOTIFY_URL`)
with the bearer from `BOTE_NOTIFY_AUTH` in the container's environment,
rendered from `BOTE_NOTIFY_TOKEN`; the Bote generates that token, and
without the Bote the call is refused. On a fresh box the include lands after
Home Assistant wrote its first `configuration.yaml`; `laib restart
home-assistant` once loads it.

## Voice: what Assist understood, and what not (#266)

Counted by Haus, not by a custom component or an automation: Home
Assistant keeps the last 10 Assist pipeline runs per pipeline in memory
(`assist_pipeline/pipeline_debug/list|get` on its WebSocket API; verified
in HA core `homeassistant/components/assist_pipeline`, 2026-10-16), each
with the sentence (`intent-start` `intent_input`), whether HA's own intents
handled it (`intent-end` `processed_locally`) and the response type
(`error`, e.g. `no_intent_match`). Haus reads them every few minutes with
its `HAUS_HA_TOKEN` and counts into usage-metrics (app `stimme`):
`erkannt` (HA's own intents), `weitergereicht` (answered by the fallback
conversation agent) and `nicht-erkannt` (an error); the sentence of a
`nicht-erkannt` run goes to `/miss` (`source: stimme`) without device,
satellite or conversation id, kept 30 days.

**What you switch on in Home Assistant:** nothing new — but those debug
calls are admin-only, so the long-lived token behind `HAUS_HA_TOKEN` must
belong to a user with **Administrator** rights (Settings → People → the
user → Administrator). With a non-admin token Haus counts no voice and the
Box page says so. Known limits: more than 10 commands on one pipeline
between two reads, or an HA restart in between, lose those runs' counts.
