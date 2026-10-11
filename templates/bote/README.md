# bote — the agent you talk to (ADR 0016)

Hermes **as stock** (`nousresearch/hermes-agent`), configured only through
the post-deploy hook, plus signal-cli's REST API in the same pod. Nothing
is forked. If this template ever needs more than configuration, that is
the signal to build a lean Bote instead (ADR 0016 §2).

## Once, by hand

1. Link the Bote's phone number: `laib exec bote --container systemd-bote-signal -- signal-cli link -n Bote`
   and scan the QR code in Signal → Linked Devices on the phone that owns
   that number (a second SIM or a spare phone; the Bote should not share
   the admin's own number).
2. Tokens: `BOTE_LAIB_TOKEN` (hand `bote`) is minted on `laib add bote`;
   optional `laib token mint human --note "signal +49…"` → `laib secret set BOTE_HUMAN_TOKEN`.
   The human token is used only through the `laib-admin` MCP entry, which
   Hermes marks `untrusted`: every write asks, and the SOUL says it may be
   used only on the admin's own message.
3. Pairing: the first message from an unknown number yields a pairing code;
   approve with `laib exec bote -- hermes pairing approve signal <CODE>` or
   list the number in `SIGNAL_ALLOWED_USERS`.

4. The house as a tool: in Home Assistant add the integration **Model
   Context Protocol Server** (Settings → Integrations; it exposes the Assist
   API: turn on/off, set, play, and what is on right now), create a
   long-lived access token for a user who may see the exposed entities
   (Profile → Security), then `laib secret set BOTE_HA_TOKEN`. Expose the
   entities the family may touch under Settings → Voice assistants →
   Expose. Without the token the `haus` tool is simply absent.

5. Automations by sentence: the `bote-automat` container (image
   `ghcr.io/mdopp/laib-automat`) serves HA's automation config API as MCP
   on loopback; Hermes gets it as `automat` with `trust: untrusted`, so
   every write asks you on Signal first. Same HA token as the `haus` tool.
   Tools: entitaeten, automationen, automation_lesen, automation_schreiben,
   automation_loeschen, automation_schalten.

## What is verified and what is not

Verified from the Hermes docs (2026-10): the image and its `/opt/data`
volume, the Signal env keys (`SIGNAL_HTTP_URL`, `SIGNAL_ACCOUNT`,
`SIGNAL_ALLOWED_USERS`, `SIGNAL_HOME_CHANNEL`), `mcp_servers` with `url`,
`headers` and `trust`, `${VAR}` references resolved from `.env`,
`model.provider: custom` + `model.base_url` + `api_mode: chat_completions`,
`model_aliases.<name>: {model, provider: custom, base_url}` (all from
`cli-config.yaml.example`, 2026-10-10), `trust: full | untrusted` and the
MCP client's Streamable-HTTP-then-SSE fallback (`tools/mcp_tool.py`), so
Home Assistant's `/mcp_server/sse` needs no bridge. Not yet verified:
`SIGNAL_HOME_CHANNEL` (the Signal adapter reads `SIGNAL_HTTP_URL`,
`SIGNAL_ACCOUNT`, `SIGNAL_ALLOWED_USERS`; the home channel for cron
delivery may be set in-chat instead), and the whole thing on a box.
Issue: the Bote on the NUC (#23, phase 2).
