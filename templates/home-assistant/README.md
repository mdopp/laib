# home-assistant

Home Assistant, Z-Wave JS UI and the Matter server on the host network.
First run: the onboarding in the UI (ServiceBay automated it with 2,000
lines of post-deploy; Laib does not — once per box is a human's five
minutes). What belongs here from ADR 0015, configured in HA after
install: the Wyoming integrations for Stimme (whisper, piper/Martin,
openWakeWord), the CalDAV integration for Radicale, Music Assistant,
MQTT for mosquitto, and Frage as the conversation agent fallback.

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
