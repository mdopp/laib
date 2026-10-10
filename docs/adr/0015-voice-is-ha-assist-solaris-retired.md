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
   onboarding to a Box card against LLDAP. Soul, skills, topics,
   chronicle, sessions, compaction and the chat go away. A chat UI against
   llama-server, if anyone wants one, is an ordinary template.
5. **Hermes** has nothing left to do: there is no household agent
   conversation; the only agent conversation is Claude in the Werkstatt.

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
