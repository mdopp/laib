# bote — the agent you talk to (ADR 0016)

Hermes **as stock** (`nousresearch/hermes-agent`), configured only through
the post-deploy hook, plus signal-cli's REST API in the same pod when the
Bote has Signal. Nothing is forked. If this template ever needs more than
configuration, that is the signal to build a lean Bote instead (ADR 0016 §2).

## Channels: a choice (#184)

Signal, Matrix and Telegram are each optional; at least one is set. A
channel is on while its value is set, and an unset one leaves no container
and no line in Hermes' `.env` (laib.yml `when:` and `channels:`):

| Channel | On while | Secret | Who may talk | Approvals |
|---|---|---|---|---|
| Signal | `SIGNAL_ACCOUNT` (the Bote's number) | none (linked device, QR) | `SIGNAL_ALLOWED_USERS` | `ADMIN_NUMBER` |
| Matrix | `MATRIX_HOMESERVER` | `BOTE_MATRIX_TOKEN` | `MATRIX_ALLOWED_USERS` | `ADMIN_MXID` in `ADMIN_ROOM` |
| Telegram | `TELEGRAM_ALLOWED_USERS` | `BOTE_TELEGRAM_TOKEN` | `TELEGRAM_ALLOWED_USERS` | never |

`bote-signal` runs only with Signal, `bote-relay` only with `ADMIN_NUMBER`
or `ADMIN_MXID` (both empty in the example: set yours to approve in the
chat). `laib plan` refuses, before anything restarts: a person in box.yml
`people` with a target on a channel the Bote does not have, `ADMIN_NUMBER`
without Signal, `ADMIN_MXID` without Matrix or `ADMIN_ROOM`, and Matrix or
Telegram without its token (laib.yml `requires:`); it prints each container
it leaves off as `AUS:`. The `pre-start` hook writes Hermes' `.env` and
bote-notify's copy before the containers start, so a channel switched off
is gone before Hermes reads it; `post-deploy` renders the rest and runs
`pre-start` again first. The pod's health is bote-notify's `GET /health`
on 8645, which with Signal also asks signal-cli's `/v1/about`. A person's Matrix target is the room id of
their direct chat with the Bote (`matrix:!…:server`; Element: Room settings
→ Advanced → Internal room ID). Hermes' sender posts into a room and opens
no direct chat for a user id, so the state refuses `@user:server` (#189).

**Matrix**, once: give the Bote its own **plain user** account on your
homeserver: not a server admin, and not an application-service token
(either can act as other users, so the Bote could pose as you; the relay
refuses an account Synapse's admin API answers for). Log it in once to get
a token (a new device; do not take the token of an Element session):
`curl -s -XPOST https://<homeserver>/_matrix/client/v3/login -d
'{"type":"m.login.password","identifier":{"type":"m.id.user","user":"<bote>"},"password":"…","initial_device_display_name":"Bote"}'`,
then `laib secret set BOTE_MATRIX_TOKEN` with its `access_token`. The Bote
speaks Matrix **unencrypted only** (`MATRIX_E2EE_MODE=off`, ADR 0016
amendment 2026-10-14): create rooms with it with encryption off. For
approvals, create one more unencrypted room with only you and the Bote,
put its id (`!…:server`, Room settings → Advanced) into `ADMIN_ROOM` and
your Matrix ID into `ADMIN_MXID`; your account must be on the same
homeserver as the Bote's. Otherwise the relay refuses Matrix with a
journal line and keeps Signal running; it checks again every minute. It
also stops Matrix approvals when that room turns encrypted or someone else
joins. Whoever runs that homeserver, and every session or leaked token of
your Matrix account, can approve as you: run your own homeserver if you
approve on Matrix, and log out sessions you do not use.

**Signal off** (`SIGNAL_ACCOUNT: ""`): `bote-signal` goes, and with it
signal-cli's port 8085 (neither published nor declared, #190); its data does
not. The linked device's keys stay in `${DATA_DIR}/bote/signal` and in the
backups. Unlink the device on the phone (Signal → Linked devices), delete
the folder, and let the backup retention age the old snapshots out.

**Telegram**, once: create a bot with @BotFather, `laib secret set
BOTE_TELEGRAM_TOKEN`, and list the numeric user ids that may talk to it in
`TELEGRAM_ALLOWED_USERS` (a bot is public; the list is what keeps others
out). A person's Telegram target is their chat id with the bot, which
exists once they wrote to it.

## Once, by hand

1. Link the Bote's phone number (Signal only): open the Box page (`laib.<domain>`) →
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
   stream and from the admin's Matrix room, acted on only when the sender
   is `ADMIN_NUMBER` on Signal or `ADMIN_MXID` in `ADMIN_ROOM` (#184),
   answered with the proposal's consequence. Anyone else trying it gets a
   journal line and no reply; Telegram never approves. The Automat's writes and deletes execute only with
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
   Expose. Without the token the `ha` tool is simply absent.

5. Automations by sentence: the `bote-automat` container (image
   `ghcr.io/mdopp/laib-automat`) serves HA's automation config API as MCP
   on loopback; Hermes gets it as `automat` with `trust: untrusted`, so
   every write asks you on Signal first. Same HA token as the `ha` tool.
   Tools: entitaeten, automationen, automation_lesen, automation_schreiben,
   automation_loeschen, automation_schalten.

6. Messages to people (ADR 0022 §3, #177): declare the household in
   box.yml `people` (`mama: ["signal:+49…", "telegram:<chat id>"]`,
   preferred first, only on the Bote's channels; docs/STATE.md). The `bote-notify` container runs this
   template's `notify.py` (stdlib) in the stock Hermes image and serves
   `POST /mitteilung {"an": "mama", "text": "…"}` on `127.0.0.1:8645` with
   `Authorization: Bearer <BOTE_NOTIFY_TOKEN>` (generated on add). It looks
   the person up in `render/people/people.json`, refuses an unknown person
   (404, never a free target) and delivers with
   `hermes send --to <channel> --file -`, the next channel when one fails
   (20 s per channel, 50 s for all, under Home Assistant's 60 s). Hermes
   reads a private 0600 copy of `bote/notify/.env`, which post-deploy writes
   with the platform credentials only (`SIGNAL_*`, `TELEGRAM_*`, …): no Bote
   token, no model key, nothing of the gateway's HERMES_HOME. Text is capped
   at 1000 characters, a request at 4 KB, five messages per person and
   twenty in total per minute; `MEDIA:` is refused (Hermes would attach a
   file). The log names the person, the platform, the exit code and the
   first twelve characters, never the target or Hermes' output.
   `render/people/people.json` is 0600. Known limit: Hermes mirrors a sent
   message into the private copy's sessions, not the gateway's, so when the
   person answers, the Bote does not see what it sent. Home
   Assistant's template renders `rest_command.mitteilung` against it, and
   with the automat wired the SOUL turns "sag mir Bescheid, wenn …" into an
   automation with that action, through the automat's usual draft and
   code. The Bote itself has no tool to message anyone: only an automation a
   person confirmed does (Hermes' `send_message` is not a model tool).

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
| `haushalt` | Haus is installed (it generates `HAUS_HAUSHALT_TOKEN`) **and** answers with that token at `HAUSHALT_MCP_URL` (default: Haus on the host's port 8650, haus#11, #170) | calendar, reminders, notes |
| `automat` + `ha` | `BOTE_HA_TOKEN` set | 6 + Home Assistant's Assist tools (depends on the exposed domains) |

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

Verified from the Hermes source (`NousResearch/hermes-agent` at
`0e427d7`, 2026-10-14; #184): the channel env keys in
`gateway/config_env.py` (`_ENV_STEPS`: `SIGNAL_HTTP_URL` + `SIGNAL_ACCOUNT`
enable Signal, `SIGNAL_HOME_CHANNEL`; `MATRIX_ACCESS_TOKEN` +
`MATRIX_HOMESERVER` enable Matrix, `MATRIX_E2EE_MODE`, `MATRIX_HOME_ROOM`;
`TELEGRAM_BOT_TOKEN` enables Telegram) and the allowlists in
`gateway/authz_mixin.py` / `gateway/pairing.py` (`<PLATFORM>_ALLOWED_USERS`),
also listed in `plugins/platforms/{matrix,telegram}/plugin.yaml` and the
Matrix adapter's docstring. Its Matrix extra (mautrix with E2EE) is baked
into the image (`Dockerfile`). `hermes send` to Matrix outside the gateway
connects its own adapter with the same token
(`tools/send_message_senders.py`), which is one reason the Bote's Matrix
stays unencrypted. From the Hermes docs (2026-10): the image and its
`/opt/data` volume, `mcp_servers` with `url`,
`headers` and `trust`, `${VAR}` references resolved from `.env`,
`model.provider: custom` + `model.base_url` + `api_mode: chat_completions`,
`model_aliases.<name>: {model, provider: custom, base_url}` (all from
`cli-config.yaml.example`, 2026-10-10), `trust: full | untrusted` and the
MCP client's Streamable-HTTP-then-SSE fallback (`tools/mcp_tool.py`), so
Home Assistant's `/mcp_server/sse` needs no bridge. Not yet verified:
the whole thing on a box, Matrix and Telegram included.
Issue: the Bote on the NUC (#23, phase 2).

## Joshua (#119, #122)

The Bote's persona is Joshua, the WOPR from WarGames, in the person's
language. "Was kannst du?" (or "Joshua", "Menü") shows the WOPR main
directory: the Bote's real tools by number, then SPIELE. A number enters
that context; the prompt shows where you are (`JOSHUA/GERÄTE>`; the family reads words, never tool names, ADR 0021), the Bote
answers with that tool until ZURÜCK (one level up) or MENÜ. Under SPIELE:
the film's list, tic-tac-toe (it never loses; 0 players plays itself) and
Global Thermonuclear War, which nobody wins. Approvals, warnings and errors
stay plain words. Where you are is the persona engine, the `bote-persona`
container (`cmd/persona`, ADR 0020/0021), an MCP tool named `persona` with no
side effects; every message goes there first.

The SOUL is the house persona's `style.md` (box.yml `persona`, rendered to
`render/persona/<name>/`) plus the fixed rules: approvals, warnings and
errors in plain words, no approvals by the Bote, the never-list. No persona
changes those (ADR 0020 §3). A number in box.yml `chatPersonas` talks to its
own persona: Hermes shows the model the sender's profile name, the persona
engine resolves it to the number through signal-cli's contacts in the pod,
and answers with a `FIGUR:` line naming who to be. An ambiguous name gets
the house persona. That is a costume, never a right.
