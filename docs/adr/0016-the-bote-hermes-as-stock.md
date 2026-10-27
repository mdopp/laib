# ADR 0016 — The Bote: the agent you talk to, on Signal, built from Hermes as stock

Applies when you add a conversation channel, a chat page, a tool the
family talks to, or consider forking Hermes.

- **Status:** Accepted, 2026-10-10
- **Deciders:** operator (mdopp): "Ich möchte ja auch mit Signal mit ihm
  reden können."

## Context

ADR 0015 removed the LLM from the voice path and retired Solaris the
agent. That left the claim "the only agent conversation is Claude in the
Werkstatt", which was wrong: the admin wants to talk with the house's
agent on the road, exchange, ask, approve. ADR 0009 declined Hermes
because of the voice path (prefill, latency, no Wyoming); those reasons
are gone, and its strengths — messaging gateways, HA integration, MCP
client, memory, cron — are exactly what a text agent needs.

## Decision

1. **The Bote** is the agent people talk to: Signal first, Matrix and
   Telegram when wanted, and the Sprechen field in Haus. It knows the
   house (HA), the box (Laib MCP as hand `bote`, read + propose), questions
   (Frage), the household memory (a Haus tool), and reminders (cron).
2. **Hermes, as stock.** No fork, no sidecar, no trace proxy. A template
   configures gateways, tools and prompts. The June lesson stands as a
   threshold: when the Bote needs more than a few hundred lines of glue,
   it is rebuilt lean; the first sign of a workaround stack is the signal.
3. **Approval by verified number.** A message from the admin's Signal
   number is the admin. The relay (not the Bote) holds the `approver`
   token, a `human` hand that can only decide, and uses it only on the
   admin's word; Hermes holds no token that can approve. *(Corrected
   2026-10-14, #184: this said the Bote held a `human` token; since #48 it
   is the relay's approver token, see the amendment of 2026-10-11.)*
   Amends ADR 0012: two approval
   surfaces, the Box page and the verified Signal number. *(Amended
   2026-10-14, #184: the verified identity is the admin's Signal number or
   the admin's Matrix ID in one room; Telegram never approves. See the
   amendment below.)*
4. **Models.** The Bote is not latency-critical and may request the
   `thinking` preset through the lease for hard questions; the household
   preset is its default. *(Amended 2026-10-14, #157: there is no
   `thinking` preset or window anymore. The Bote asks the lease for the
   preset itself, `qwen3.6-35b-a3b`, and the lease picks the window;
   ADR 0011 holds the contract.)*
5. Family members may each get the Bote on their own number with their
   own memory namespace; the house yes, the box no. Default: the admin.

## Consequences

- Haus drops the Verlauf page; the morning report and escalation answers
  reach the admin through the Bote.
- ADR 0009 is superseded for the text case and stays for voice.
- ADR 0015 §5 is corrected: Hermes has one job, the Bote.

## Amendment 2026-10-11 (#48): approval by verified number is code, not prose

Hermes holds no token that can approve. The `bote` hand reads, proposes,
notes and escalates (its MCP entry is `trust: untrusted`, so proposing
asks the person in the chat). "gib #N frei" / "lehne #N ab" are handled
by the **relay** (`cmd/relay`, container `bote-relay` in the pod): it
subscribes to signal-cli's event stream, acts only when the sender is
`ADMIN_NUMBER`, looks the proposal up, decides through Laib with the
`approver` hand — `human: true`, `verbs: [approve, reject, inbox, status,
note]` — and answers with the consequence. Another number trying it gets a
journal line. The Automat executes writes and deletes only with the code
its first answer named, and keeps deleted automations for restore.

## Amendment 2026-10-13 (#122): the Bote's persona is Joshua

Owner decision: Joshua, the WOPR from WarGames that started as an easter egg
(#119), is the persona of the whole Bote, in the person's language. The
Bote's tools become a directory you stand in: every message goes to the
`joshua` tool first, which knows where the person is (`JOSHUA>`,
`JOSHUA/HAUS>`, `JOSHUA/SPIELE>`) and answers with a screen or with
`WEITER: <tool>`, and then the Bote answers the request with that tool. Where
the person is lives in code, not in the model. ZURÜCK goes one level up,
MENÜ to the directory. The games keep the film's English.

The guardrail from ADR 0019 §6 stays: approvals, warnings, errors and
anything about safety are plain words with the consequence, no role-play.
The relay's answers are code and unchanged.

## Amendment 2026-10-14 (#184): channels are a choice; approval by Signal number or Matrix ID

Owner decision (option b): Signal, Matrix and Telegram are each optional,
and at least one is set. An unset channel leaves no container, no required
value and no line in Hermes' `.env`. The template says this with two
contract fields: `when:` (a unit exists only while one of its values is
set) and `channels:` (where the Bote delivers; a person's target on any
other platform fails plan). A third, `requires:`, makes plan refuse a
combination the relay or the hook could not use: an admin identity without
its channel or room, Matrix or Telegram without its token. The channel
config is rendered before the units start (the `pre-start` hook), so a
channel switched off is gone before Hermes starts and before verify asks.
A port takes `when` as well (#190): without Signal, signal-cli's 8085 is
neither published by the pod nor declared.

The relay decides on two identities and nothing else:

- **Signal:** `ADMIN_NUMBER`, as before.
- **Matrix:** `ADMIN_MXID`, in the room `ADMIN_ROOM`.
- **Telegram never approves.** It carries chat and messages only.

What a Matrix approval rests on:

- **The sender the homeserver asserts.** The relay reads the event's
  `sender` and nothing the client wrote. The compare is exact.
- **One trusted homeserver: the Bote's own.** The relay refuses Matrix
  when the admin's ID lives on another server, or is the Bote's own ID.
  Federation would add a second server that can assert senders.
- **That homeserver is trusted completely.** Whoever runs it can approve.
  So can every session of the admin's Matrix account, and anyone holding a
  leaked access token of it: a token is a login, and any client logged in
  as the admin is the admin. On a public homeserver the operator is a third
  party. This is weaker than Signal, where the server cannot forge a sender
  and a second device must be linked from the phone. Run your own
  homeserver if you approve on Matrix, and log out sessions you do not use.
- **The Bote's account is a plain user.** It may not be a homeserver admin
  and may not use an application-service token: either can act as other
  users, so the Bote could assert the admin. The relay asks Synapse's admin
  API about its own account and refuses Matrix when it answers. An
  appservice token cannot be told apart from the outside; the setup must
  not use one.
- **No encryption.** The room must be unencrypted and hold only the admin
  and the Bote. The relay cannot read Megolm. A second process on the
  Bote's encrypted device would also take that device's to-device keys.
  So the Bote's Matrix runs with `MATRIX_E2EE_MODE=off`: it reads and
  writes unencrypted rooms only, and the homeserver sees the text.
- **Checked, not assumed.** Before reading the room the relay checks the
  token, the homeserver, the room's encryption and its members, and
  refuses Matrix if any is wrong. While it reads, a room that turns
  encrypted or gains a member stops Matrix approvals, with a journal line,
  and the check runs again.
- **A refused Matrix never takes Signal down.** A failed check (homeserver
  down at boot, token revoked, room changed) is one journal line per
  reason; the relay keeps running, Signal approvals and the Box page go
  on, and the check is tried again every minute until it passes.
- **Read without `/sync`.** The relay polls the room with `/messages` using
  the Bote's token and answers in the room. Each event is handled once, by
  its id. Edits, notices and the Bote's own messages are not verdicts.

Not done, on purpose: encrypted rooms, device or cross-signing
verification, a second homeserver, approvals in any other room.

Switching Signal off removes `bote-signal`, not its data. The linked
device's keys stay in `${DATA_DIR}/bote/signal` and in the backups that
hold it. To remove them: unlink the device on the phone (Signal → Linked
devices), delete the folder, and let the backup retention age the old
snapshots out (or `restic forget` them).

## Amendment 2026-10-19 (#421): Discord as a fourth channel

The owner will not get a second phone number for the Bote, and the household
already uses Discord. Discord joins Signal, Matrix and Telegram under the
same rules: it is on while `DISCORD_ALLOWED_USERS` is set, the bot token is
the external secret `BOTE_DISCORD_TOKEN`, an unset channel leaves no line,
and users without the token fail plan. A person is reached on their DM
channel id (`discord:<channel id>`). **Discord never approves.** As with
Telegram, a bot token and a Discord account are not a verified identity
(#48). It carries chat and messages only, and approvals stay on the Box
page and in Haus. A token without users is not refused. It leaves Discord
off, the same as Telegram. Signal without a second SIM is possible on a
spare landline, registered with signal-cli as primary by voice call. The
template README has the steps; the Box page does not do it.

## Amendment 2026-10-19 (#441): Laib talks to the owner in the chat; the chat decides only what is not destructive

Owner decision (mdopp, 2026-10-19): "Das Interface ist jetzt der Chat." Laib's
messages to the owner go through the Bote's channels, whichever Hermes has
configured (Signal, Discord, Telegram, Matrix). Mail is only the fallback
when no channel is reachable. In the chat the owner gets incidents and
proposals, discusses them with the Bote and chooses between the options.
**Non-destructive** actions (an update, a restart, one of a proposal's
options) are approved right there. **Destructive** ones are approved
**only in the Inbox**, behind Authelia's second factor. The chat informs
and sends the link; it never approves them.

- **Delivery.** laib-notify (#443) gets `box.yml notify.person`, a person
  in `people`. It posts each message to bote-notify (`POST /mitteilung`,
  the Bote's `BOTE_NOTIFY_TOKEN`), which tries that person's channels in
  their order. Mail (Authelia's settings) is used only when that fails or
  there is no Bote. The messages are: a red that stays ten minutes, the
  weekly updates, and each new open proposal, once.
- **What the chat may decide.** The line is the one the kernel already
  draws: a proposal of tier `destroy`, of class `remove`, or one the
  content review marks for a human (a removal, data out of the backup, a
  script on the host, a public route, a token, policy.yml …) is the
  Inbox's. `laib approve` refuses it for every hand but `human`, so the
  relay's approver gets the Inbox link instead of a verdict. Everything
  else can be approved with "gib #N frei" in the chat. The Inbox
  (`laib.<domain>`) moves from one factor to two.
- **Who decides in the chat stays as it was (#48, §3).** Code that sees
  the sender decides, never the model. The relay acts only on the owner's
  verified identity: the Signal number, or the Matrix ID in its room. The
  Bote's allowed-user lists keep everyone else out of the conversation.
  Discord and Telegram carry the messages and the discussion, and their
  verdicts still do not count. A bot token is not an identity the relay
  can check itself, and the owner's decision does not change what a
  verified identity is. A Discord verdict needs the relay to read the
  owner's DM with the bot itself (#463).
- The SOUL tells the Bote that these messages arrive in the chat, that it
  reads the state from laib rather than from memory, and that it sends
  the Inbox link for what the chat may not decide.

## Amendment 2026-10-19 (#463): the owner's verdicts on Discord, read by the relay itself

Owner decision (#441: non-destructive actions are approved in the chat;
the owner talks to the Bote on Discord, #421). The relay now accepts a
verdict on Discord under the same rule as Signal and Matrix: code that
sees the sender decides, never Hermes or the model.

- **How it sees the sender.** The relay reads the owner's direct chat
  with the bot itself, polling Discord's REST API with the bot token. A
  verdict counts only when Discord names the owner (`ADMIN_DISCORD_ID`) as
  the author, in that one DM (`ADMIN_DISCORD_CHANNEL`). At start the relay
  checks that the channel is a DM (type 1) with the owner alone. A bot
  token can post as the bot and never as a person, so Hermes holding the
  same token cannot forge the owner's verdict. Messages from before the
  start, the bot's own messages and any other author never decide.
- **What counts as verified.** Identity decision (proposed, for the owner):
  the owner's Discord account, as Discord asserts it, counts as their
  identity for **non-destructive** verdicts, the same weight as the Signal
  number. Discord is a third party, so whoever holds the owner's Discord
  session decides in the chat as them: the owner keeps two-factor login on
  that account. The id must also be on the Bote's allowed list
  (`DISCORD_ALLOWED_USERS`). pre-start refuses an owner id the Bote would
  not even talk to. Destructive verdicts stay with the Inbox and the second
  factor (#441); `laib approve` refuses them to the approver whatever the
  channel.
- Telegram still never decides. The same construction would work there
  but has not been asked for.

## Amendment 2026-10-26 (#778): §3 is withdrawn — the chat decides nothing

Owner decision 2026-10-26 („B3“, #751): approvals only in the Inbox. §3
("approval by verified number") and its amendments of 2026-10-11, 2026-10-14
(Matrix), 2026-10-19 (#441, #463) and the poll of #509 no longer hold: the
relay and the `approver` hand are gone, and no channel (Signal, Matrix,
Telegram, Discord) approves. The channels stay chat channels for the Bote;
`ADMIN_NUMBER` and `ADMIN_ROOM` stay as Hermes' home channel, `ADMIN_MXID`,
`ADMIN_DISCORD_ID` and `ADMIN_DISCORD_CHANNEL` have no use any more. A new
proposal reaches the owner as one line, „Freigabe #N: <headline> · <Inbox
link>“ — no impact lines, no decision words, no poll. The SOUL tells the
Bote that the chat has no approvals and to send the Inbox link when asked.
Matrix stays unencrypted (bote-notify's `hermes send` still shares the
Bote's token).
