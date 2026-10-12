# bote — the agent you talk to (ADR 0016)

Hermes **as stock** (`nousresearch/hermes-agent`), configured only through
the post-deploy hook, plus signal-cli's REST API in the same pod. Nothing
is forked. If this template ever needs more than configuration, that is
the signal to build a lean Bote instead (ADR 0016 §2).

## Once, by hand

1. Link the Bote's phone number: open the Box page (`laib.<domain>`) →
   **Bote verbinden (QR)** and scan it in Signal → Gekoppelte Geräte on the
   phone that owns that number (a second SIM or a spare phone; the Bote
   should not share the admin's own number). The page fetches the QR from
   signal-cli's REST API (`GET /v1/qrcodelink?device_name=Bote` on the
   service's first declared port); from the box itself
   `curl -o qr.png http://127.0.0.1:8080/v1/qrcodelink?device_name=Bote`
   does the same (#54).
2. Tokens: `BOTE_LAIB_TOKEN` (hand `bote`: read, propose, note, escalate)
   and `BOTE_APPROVER_TOKEN` (hand `approver`: approve, reject, inbox,
   status, note — nothing else) are minted on `laib add bote`. **Hermes
   holds no token that can approve** (#48): "gib #17 frei" / "lehne #17 ab"
   are read by the `bote-relay` container straight from signal-cli's event
   stream, acted on only when the sender is `ADMIN_NUMBER`, answered with
   the proposal's consequence. Any other number trying it gets a journal
   line and no reply. The Automat's writes and deletes execute only with
   the four-digit code its first answer names; deleted automations are
   kept under `bote/automat/geloescht` for `automation_wiederherstellen`.
3. Pairing: the first message from an unknown number yields a pairing code;
   approve with `laib exec bote -- hermes pairing approve signal <CODE>` or
   list the number in `SIGNAL_ALLOWED_USERS`. The relay and Hermes both
   listen to signal-cli's daemon (`/api/v1/events`, which serves every
   subscriber; Hermes also uses `/api/v1/rpc`) — the `bote-signal`
   container must run `signal-cli daemon --http` (#54 checks the image).

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

## Tools: only what answers, and a budget (#54)

The hook probes every MCP URL at apply time (any HTTP answer counts, a
connection error does not) and renders only the servers that answered,
printing the ones it left out (`frage: … antwortet nicht, weggelassen`).
The SOUL is rendered from the same list, so the model is never told about
a tool it does not have; remove the first line of `SOUL.md` to make the
file yours and stop the hook from rewriting it.

| Server | When | Tools |
|---|---|---|
| `laib` | always | 16 (hand `bote`: read, propose, note, escalate) |
| `frage` | `FRAGE_URL` answers (`templates/frage` serves `/mcp`) | 1 |
| `haushalt` | `HAUSHALT_MCP_URL` set **and** answers — Haus does not serve it yet (ADR 0017), so leave it empty | — |
| `automat` + `haus` | `BOTE_HA_TOKEN` set | 6 + Home Assistant's Assist tools (depends on the exposed domains) |

A normal conversation without the HA token sees 17 tools; with it, 23 plus
HA's. Hermes' `mcp_servers` has no per-server tool allowlist in the
verified config (2026-10), so the HA set is bounded only by what you
expose in Home Assistant (Settings → Voice assistants → Expose) — keep it
to the domains the family touches. The SOUL tells the model to take
`/model denken` for long multi-tool tasks (#53).

**Memory:** nothing is configured. The SOUL says so in plain words — the
Bote remembers nothing beyond a conversation and says so when asked to —
and points to the Haushalt (calendar, notes) once Haus serves it.

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
