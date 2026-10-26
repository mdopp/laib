# ADR 0015 — Voice is Home Assistant Assist plus Stimme plus Frage; Solaris the agent is retired

Applies when you are about to add a tool, a skill or a memory feature to the
household agent, or to route a spoken sentence through an LLM loop.

- **Status:** Proposed, 2026-10-10 — accepted once the two checks below
  pass on the box.
- **Deciders:** operator (mdopp): voice matters; realistically it is
  alarms and timers, Einslive, music from a playlist, "was ist …", and
  switches.

## Context

Solaris is a custom agent runtime (40k lines) built for a household
assistant with memory, identity by voice, skills and a chat. The chat is
rarely used (ADR 0014). The spoken use is five intents, four of which
Home Assistant Assist handles natively, and a guest mode for everyone is
enough: nobody needs to be recognised.

## Decision

1. **The voice brain is Home Assistant Assist.** Timers, alarms, lights,
   switches, scenes, and media via Music Assistant (Jellyfin library,
   radio stations, playlists) are Assist intents. No LLM in that path.
2. **Stimme** is the Wyoming set Assist already speaks: openWakeWord with
   the trained wake word, Whisper, the Martin TTS voice. The speaker-
   identity gatekeeper is dropped.
3. **Frage** is a small service (about 300 lines): SearXNG searches,
   llama-server summarises, and it registers as Assist's conversation
   agent for sentences no intent matched ("was ist …"). The same route
   serves Haus's Sprechen field.
4. **Solaris as an agent is retired.** Its memory (ingest, OKF, notes
   search) becomes a library used by Haus for the Notizen & Dokumente
   search. Pages, cards and the companion API go to Haus (ADR 0014);
   administration to the Operator and Laib (ADR 0013); resident
   onboarding to Haus's Box card, which invites through Laib's
   `person_invite` (an LLDAP user without a password, Authelia's reset mail
   for the first one, #360) instead of talking to LLDAP itself. Soul, skills, topics,
   chronicle, sessions, compaction and the chat go away. A chat UI against
   llama-server, if anyone wants one, is an ordinary template.
5. **Hermes** gets one job, the Bote (ADR 0016): the text agent on Signal
   and behind Haus's Sprechen field. Not in the voice path.

## The two checks

1. German Assist sentences for timers and media match how the household
   speaks (try the sentence set on the box; add custom sentences where not).
2. Frage registers cleanly as an Assist conversation agent for the local
   OpenAI-compatible server (own integration or the Extended OpenAI
   Conversation path).

## Consequences

- `solarisbay` becomes templates: stimme pieces, frage, music-assistant,
  searxng, in the household registry; the engine repo is archived after
  Haus has the memory library.
- `docs/DEKONSTRUKTION.md` step 5 changes from "Solaris API" to "Solaris
  retired": Stimme talks to Assist, Haus talks to Frage and the memory
  library, nothing talks to a turn loop.
- The RTX card loses its biggest resident consumer; Frage and the
  Operator share the household preset.

## Amendment 2026-10-13 (#135): Haus's Sprechen field goes to the Bote

§3 said Frage's route also serves Haus's Sprechen field. ADR 0016 made the
Bote the agent people talk to, in Signal and in the Sprechen field (Hermes'
OpenAI-compatible API, port 8642 in the bote pod). Frage stays Assist's
conversation agent for sentences no intent matched, and the Bote's `frage`
tool.

## Amendment 2026-10-26 (#760): a device command that misses is answered, not searched

„Schalte Sofa-Licht aus“ missed HA's intents (the light is „Sofalicht“) and
reached Frage, which searched the web. Two parts, neither puts an LLM in the
switching path (§1):

1. **Names as Whisper writes them.** home-assistant gives each entity exposed
   to Assist the hyphenated and split spellings of a German compound as
   aliases (a fixed list of device nouns, never a guess), marks which ones it
   added, and never touches the owner's.
2. **Frage answers a missed command honestly.** A sentence shaped like a
   command („Schalte …“, „… aus“) gets no search: Frage says which device it
   did not find and names the nearest from its prompt's line „Geräte: …“, a
   template HA renders with the names of what Assist switches. Frage never
   switches.

Rejected: HA's Assist API (`llm_hass_api`) on the agent Frage, to let it see
or call the devices — with an LLM API HA filters its own GetState and
media-search intents out of the local path and hands them to the agent
(assist_pipeline `_async_local_fallback_intent_filter`), so „Spiel …“ would
leave Music Assistant for Frage. And a model behind the lease that switches
by guessing a name is what §1 ruled out.
