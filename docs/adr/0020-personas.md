# ADR 0020 — Personas: the house's character is a package

Applies when you give the house a name, a voice, a wake word or a way of
talking; when you build a gimmick (a game, a directory, quotes) into the
Bote; or when a family member wants "their" assistant to be someone else.

- **Status:** Accepted, 2026-10-13 (#124)
- **Deciders:** operator (mdopp): "Lass uns das so umbauen, dass das System
  Personas unterstützt. Eine davon ist Joshua. Aber man weitere bauen kann.
  Inkl. Wakeword, Stil, ein paar Gimmicks, Icons."

## Context

Joshua began as an easter egg in the Bote (#119), then became the Bote's
persona with a directory and levels (#122). The house's names had drifted:
the voice path woke on "Balthasar", the chat was Joshua, the start page was
Haus. The character was spread over a SOUL text in a hook, a wake-word value,
page titles and an app name, with no single place that says who the house
is.

## Decision

1. **A persona is a package**, shipped like a template: in Laib's builtin
   registry and in registry repos. It holds `persona.yml` (name, display,
   languages, wake-word phrase, Piper voice, greeting, prompt, colours,
   gimmicks), `style.md` (how the character talks) and `icon.svg`.
2. **One house persona, chat personas per person.** `box.yml persona:` is the
   house's: the wake word, the speakers' voice, the Haus start page and the
   chat default. `chatPersonas:` maps a Signal number to another persona for
   the chat only. A wake word belongs to the devices in a room, not to a
   person.
3. **A persona is style, never rules.** The rules — no approvals by the Bote,
   plain words for approvals, warnings, errors and safety (ADR 0019 §6), the
   "never" list, tool limits — are code and appended to every persona.
   A `style.md` that contradicts them is overruled and the lint says so.
4. **Gimmicks are pluggable** in one engine (today `internal/joshua`):
   directory, levels and context are the engine; WOPR's directory skin,
   tic-tac-toe and Global Thermonuclear War are Joshua's gimmicks. Where a
   person is lives in code, not in the model (#122).
5. **The wake word is trained from the phrase.** The house persona's phrase
   is trained on the GPU box by Stimme's trainer; Laib owns the trigger (the
   trainer's queue lived in Solaris's database, retired with ADR 0015).
6. **Joshua is the first persona**: "Hey Joshua", WOPR's directory and games,
   calm and a little wry, in the person's language.

## Consequences

- The Bote's SOUL, Stimme's wake word and voice, Haus's title and icon and
  the app's in-app name come from the persona; a persona change is a plan
  like any other (`laib plan` shows the services it touches).
- Internal names stay: the `haus` repo and address, the `bote` template,
  the `joshua` tool. The family sees the persona.
- "Balthasar" retires when Stimme runs under Laib with "Hey Joshua".
- Work items: #125 (format, registry, box.yml), #126 (engine), #127 (Bote),
  #128 (Stimme), #129 (Haus), #130 (app).

## Amendment 2026-10-13 (#132): internal names follow ADR 0021

"Internal names stay" is withdrawn for the engine: a mechanism is named for
what it does, so the engine, its MCP tool, image and container are
`persona`, and Joshua is the first persona's data (#133). The `haus` repo and
address and the `bote` template keep their names; the Bote's Home Assistant
tool is `ha`, so "Haus" means only the surface (#134).
