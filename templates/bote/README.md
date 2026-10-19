# bote — the agent you talk to (ADR 0016)

Hermes **as stock** (`nousresearch/hermes-agent`), configured only through
the post-deploy hook, plus signal-cli's REST API in the same pod when the
Bote has Signal. Nothing is forked. If this template ever needs more than
configuration, that is the signal to build a lean Bote instead (ADR 0016 §2).

## Channels: a choice (#184)

Signal, Matrix, Telegram and Discord are each optional; at least one is set. A
channel is on while its value is set, and an unset one leaves no container
and no line in Hermes' `.env` (laib.yml `when:` and `channels:`):

| Channel | On while | Secret | Who may talk | Approvals |
|---|---|---|---|---|
| Signal | `SIGNAL_ACCOUNT` (the Bote's number) | none (linked device, QR) | `SIGNAL_ALLOWED_USERS` | `ADMIN_NUMBER` |
| Matrix | `MATRIX_HOMESERVER` | `BOTE_MATRIX_TOKEN` | `MATRIX_ALLOWED_USERS` | `ADMIN_MXID` in `ADMIN_ROOM` |
| Telegram | `TELEGRAM_ALLOWED_USERS` | `BOTE_TELEGRAM_TOKEN` | `TELEGRAM_ALLOWED_USERS` | never |
| Discord | `DISCORD_ALLOWED_USERS` | `BOTE_DISCORD_TOKEN` | `DISCORD_ALLOWED_USERS` | `ADMIN_DISCORD_ID` in `ADMIN_DISCORD_CHANNEL` (#463) |

`bote-signal` runs only with Signal, `bote-relay` only with `ADMIN_NUMBER`
or `ADMIN_MXID` (both empty in the example: set yours to approve in the
chat). `laib plan` refuses, before anything restarts: a person in box.yml
`people` with a target on a channel the Bote does not have, `ADMIN_NUMBER`
without Signal, `ADMIN_MXID` without Matrix or `ADMIN_ROOM`, and Matrix,
Telegram or Discord without its token (laib.yml `requires:`); it prints each container
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

**Discord** (#421), once, without a phone number: in the Developer
Portal (discord.com/developers) create an application, add a **Bot**,
turn on **Message Content Intent** (Bot → Privileged Gateway Intents),
and `laib secret set BOTE_DISCORD_TOKEN` with the bot's token. Invite it
with the OAuth2 URL generator (scope `bot`, permissions Send Messages and
Read Message History) to a server the family shares, or to none: people
can message it directly once they share a server with it. List the
numeric user ids that may talk to it in `DISCORD_ALLOWED_USERS`
(Discord: Settings → Advanced → Developer Mode, then right-click a user →
Copy User ID). A bot is public; the list keeps others out, and an
unlisted user gets no answer. A person's Discord target in box.yml
`people` is the id of their **DM channel** with the bot
(`discord:<channel id>`; right-click the DM → Copy Channel ID), which
exists once they wrote to it. **Verdicts on Discord (#463)**: put your own
user id into `ADMIN_DISCORD_ID` (it must be in `DISCORD_ALLOWED_USERS`) and
your DM with the bot into `ADMIN_DISCORD_CHANNEL`. The relay then reads that
DM itself through Discord's API, with the bot token, and acts only on a
message Discord names you as the author of, in that DM alone. As on Signal,
it acts only on what is not destructive (#441); the rest gets the Inbox link.
Turn on two-factor login for your Discord account: whoever has it decides
in the chat as you. A bot token without `DISCORD_ALLOWED_USERS` leaves
Discord off (no line in `.env`), as with Telegram.

**Signal without a second SIM.** The QR above links the Bote as a
*second device* of a number whose primary is a phone. Linking it to the
admin's own number is not an option: the Bote would read every chat of
that number. The alternative is a number that only the Bote uses,
registered with signal-cli as the **primary** device: a spare landline
number (a Fritz!Box MSN) works, because Signal verifies by voice call. It
is supported by signal-cli and by Signal, and not wired into the Box page.
Once, on the box, with the number in `SIGNAL_ACCOUNT` and a captcha token
from signalcaptchas.org/registration/generate.html: `curl -s -X POST
-H 'Content-Type: application/json' -d '{"use_voice": true, "captcha":
"signalcaptcha://…"}' http://127.0.0.1:8085/v1/register/<number>` (Signal
may want an SMS attempt first; repeat with `use_voice` after a minute),
pick up the call, then `curl -s -X POST
http://127.0.0.1:8085/v1/register/<number>/verify/<code>`. Then set a
registration lock PIN, so nobody can take the number over by voice call.
The account's keys live only in `${DATA_DIR}/bote/signal` (and the
backups). Losing them means registering again on the landline. Without a
landline, Discord or Matrix need no number at all.

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
   API: turn on/off, set, play, and what is on right now) and expose the
   entities the family may touch under Settings → Voice assistants →
   Expose. The token is not yours to make: home-assistant's post-deploy
   creates HA's user `bote` (no administrator) and mints its token into
   `BOTE_HA_TOKEN` (#349). Hermes reaches HA's Assist API at
   `/api/mcp/assist`, the one MCP endpoint HA serves a non-admin user
   (`/mcp_server/sse` wants an administrator by default), so whoever chats
   with the Bote reaches no admin API. Without Home Assistant the `ha` tool
   is simply absent.

5. Automations by sentence: the `bote-automat` container (image
   `ghcr.io/mdopp/laib-automat`) serves HA's automation config API as MCP
   on loopback; Hermes gets it as `automat` with `trust: untrusted`, so
   every write asks you on Signal first. HA lets only administrators read
   and write automations, so the Automat has a token of its own,
   `BOTE_AUTOMAT_HA_TOKEN` (an administrator's, minted with the others,
   #349), in its container only; Hermes never holds it. Tools: entitaeten,
   automationen, automation_lesen, automation_schreiben,
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

## Haus's Sprechen field: Hermes' API server (#215)

Haus's pages have a Sprechen field (haus#12): a sentence typed there goes
to the Bote as the logged-in person, and the reply comes back on the page.
Haus calls Hermes' OpenAI-compatible API server, `POST /v1/chat/completions`
on 8642. `pre-start` turns it on with `API_SERVER_KEY` (the secret
`BOTE_API_KEY`, generated on add; Hermes starts no API server without a key
of at least 16 characters) and `API_SERVER_HOST=0.0.0.0`. The pod publishes
8642 beyond loopback, so the haus pod reaches it at
`host.containers.internal:8642` (ADR 0007, 0012 §4). `lan: false` keeps the
LAN out, and without the key nothing answers. bote-notify's copy of the
`.env` does not carry the key. Haus gets the same secret as `HAUS_BOTE_KEY`
(templates/haus).

Who is speaking: Hermes' API server has no per-person field and ignores
OpenAI's `user`. So Haus names the person (Authelia's `Remote-Name` and
`Remote-User`) in a system line, which Hermes adds on top of the SOUL for
that turn. It also sends `X-Hermes-Session-Key: haus:<login>`, Hermes'
per-person memory scope (`gateway/platforms/api_server*.py`, 2026-10-15).
The API server's tools are `platform_toolsets.api_server` (the same safe
list as every platform) plus the MCP servers. Hermes logs a warning at
start that the API server is network-accessible. That is expected here:
the port is off the LAN and needs the key.

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
| `ha` | `BOTE_HA_TOKEN` minted (Home Assistant installed, #349) | Home Assistant's Assist tools (depends on the exposed domains) |
| `automat` | `BOTE_AUTOMAT_HA_TOKEN` minted (the same) | 6 |

A normal conversation without Home Assistant sees 17 tools; with it, 23 plus
HA's. Hermes' `mcp_servers` has no per-server tool allowlist in the
verified config (2026-10), so the HA set is bounded only by what you
expose in Home Assistant (Settings → Voice assistants → Expose) — keep it
to the domains the family touches. The SOUL tells the model to take
`/model denken` for long multi-tool tasks (#53).

**Memory:** nothing is configured. The SOUL says so in plain words — the
Bote remembers nothing beyond a conversation and says so when asked to —
and points to the Haushalt (calendar, notes) once Haus serves it.

## What the Bote is used for: counts, and what it could not answer (#266)

With usage-metrics installed (its token `USAGE_METRICS_TOKEN` set), the
Bote counts into it, anonymously: per event and UTC day, never who. It is
Hermes' own plugin system (no fork, ADR 0016): post-deploy places
`nutzung.py` as `plugins/laib-nutzung/__init__.py`, writes its
`plugin.yaml` and enables it in `config.yaml` (`plugins.enabled`); without
the token there is no plugin, no `.env` line and no SOUL line. pre-start
writes `USAGE_METRICS_URL` (`USAGE_URL`, usage-metrics as the pod reaches
it, `host.containers.internal:8094`) and the token into Hermes' `.env`,
never into bote-notify's copy.

| Event (app `bote`) | Hook (hermes-agent, 2026-10-15) | Means |
|---|---|---|
| `werkzeug.<server>.<tool>` | `post_tool_call` | one tool call, e.g. `werkzeug.haushalt.kalender`; the persona router is left out (the SOUL sends every message there, so its count is the turn count) |
| `zug` | `post_llm_call` | one turn someone asked (a cron job is not counted) |
| `unbeantwortet` | `transform_llm_output` + `post_llm_call` | a turn the Bote could not help with |

**Unanswered** is the model's judgment: the SOUL asks it to start such an
answer with `[unbeantwortet]`; the plugin's `transform_llm_output` strips
the mark before the answer is sent or stored, so nobody reads it. That
turn's sentence goes to usage-metrics' `POST /miss` (`source: bote`), the
one place a text is kept (owner decision 2026-10-16): without the sender —
the `[Name]` Hermes puts before a message in a shared chat, a timestamp
prefix and a backfill's channel context are cut off — and with no number,
session or platform; at most 280 characters, kept 30 days, admins read it
on the Box page's "Was wird genutzt" (Haus). A name *inside* the sentence
stays what it is.

The hooks only touch memory; a daemon thread flushes every five minutes.
usage-metrics not answering drops that batch and writes one log line
(`[laib-nutzung] usage-metrics antwortet nicht`) until a flush gets
through again; a turn never waits for it. Tests:
`tests/test_nutzung.py` (the hooks as Hermes calls them, a fake
usage-metrics) and `tests/hermes_plugin_proof.py`, which ci.yml runs in
the stock image the box updates to: Hermes' own loader finds the plugin
from the rendered `config.yaml`, its tool-call and turn-finalizer call
sites fire it, and the mark is gone from the answer.

ServiceBay/Solaris counted nothing of the chat; the only counter was the
widgets' `?src=widget.*`, forwarded by the Solaris engine (solarisbay#1026).

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
Home Assistant's `/api/mcp/assist` needs no bridge. Not yet verified:
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
container (`persona/`, ADR 0020/0021), an MCP tool named `persona` with no
side effects; every message goes there first.

The SOUL is the house persona's `style.md` (box.yml `persona`, rendered to
`render/persona/<name>/`) plus the fixed rules: approvals, warnings and
errors in plain words, no approvals by the Bote, the never-list. No persona
changes those (ADR 0020 §3). A number in box.yml `chatPersonas` talks to its
own persona: Hermes shows the model the sender's profile name, the persona
engine resolves it to the number through signal-cli's contacts in the pod,
and answers with a `FIGUR:` line naming who to be. An ambiguous name gets
the house persona. That is a costume, never a right.

## Speed (#470)

On the Kraftwerk a one-line question took 15–30 s. The causes were measured
in llama-server's log: a ~7k-token context of SOUL plus every MCP tool
schema, one persona round per message, and Qwen thinking ~330 tokens before
each answer. Since #470 the Bote's chat answers **without thinking** (a
`custom_providers` entry `laib` with `chat_template_kwargs.enable_thinking:
false`, the only place Hermes reads `extra_body` from). `/model denken`
thinks, through its own entry `denken`. The owner measured "Wer bist du?"
going from 18.4 s to 5.0 s, and "Wie spät ist es?" from 33.3 s to 0.8 s.
Watch for two things without thinking: a raw tool call returned as the
answer, and a guessed fact (the time). Both were seen once.

Not changed yet, to measure first: the persona round per message (#471
redesigns it), a narrower tool list per MCP server, and the model's prompt
cache and slots.

Measure before and after, the same three questions, each in a fresh
session, through Hermes' API server (the key stays in a file, never on
argv):

    H=${DATA_DIR}/bote/hermes
    printf 'Authorization: Bearer %s\n' "$(sed -n 's/^API_SERVER_KEY=//p' $H/.env)" > /tmp/bote.h; chmod 600 /tmp/bote.h
    for q in "Wer bist du?" "Wie geht es der Box?" "Wie spät ist es?"; do
      /usr/bin/time -f "%e s  $q" curl -s -o /dev/null -H @/tmp/bote.h -H 'Content-Type: application/json' \
        -d "{\"model\":\"hermes-agent\",\"messages\":[{\"role\":\"user\",\"content\":\"$q\"}]}" http://127.0.0.1:8642/v1/chat/completions
    done; rm /tmp/bote.h
    journalctl --user -u llama -S -5min --no-pager | grep -E "prompt eval time|eval time" | tail -12   # tokens and t/s per call
