# ADR 0030 — One assistant name, set once; no personas

Applies when you show or speak the assistant's name anywhere (the Bote, Haus,
the app, the speakers' Assist pipeline, a news line), when you are about to
give the house a character, a costume, a game or a second name, or when you
touch the wake word or the voice.

- **Status:** Accepted, 2026-11-02. Supersedes ADR 0020 (personas) and the
  parts of ADR 0021 that name the persona (rule 2, the glossary's persona
  and Joshua rows).
- **Deciders:** the owner (mdopp), 2026-11-02: the assistant is called
  „Homes“ (spoken like Holmes; the plural of home), set once, centrally;
  the wake word is „Hey Mycroft“ (Sherlock's brother), the pretrained model,
  no training; the games and WOPR's directory skin go; the colours stay as
  the one look (accent #3ddc84 on ground #0c120e, a green phosphor
  terminal), and so does the voice (Piper de_DE-thorsten-high). „Das Ganze
  wird schlanker.“

## Context

ADR 0020 made the house's character a package: name, display, languages,
wake-word phrase, voice, greeting, prompt, colours, gimmicks, style, an SVG
and a PNG icon, in Laib's builtin registry, the state repo or a registry
repo; one house persona plus a persona per Signal number (`chatPersonas`).
One persona was ever used, Joshua. What it cost:

- **Seven builtins** (`PERSONA`, `PERSONA_DISPLAY`, `PERSONA_PHRASE`,
  `PERSONA_WAKEWORD`, `PERSONA_VOICE`, `PERSONA_LANGUAGES`, `PERSONA_REV`), a
  loader that searched three places and checked PNG sizes, a rendered tree
  (`render/persona/<name>/`, `persona.json`, `chat.yml`) that Haus and the
  Bote mounted, and a script to make the icon.
- **A wake-word trainer** for a phrase nobody else ships: a GPU image
  (`laib-wakeword-trainer`, ~1,000 lines of Python), a retrain verb, a flash
  unit and verb that rebuilt the Voice PE's firmware, the family recording
  the phrase in Haus, false wakes kept as negatives. The first trained model
  woke twice in ten minutes on room talk (#825).
- **Games in the Bote** (tic-tac-toe, Global Thermonuclear War, WOPR's
  directory): code that answered nothing anyone asked.
- **A name in five places**, each read differently: the Bote's SOUL, Haus's
  persona.json, the app's `/napi/persona`, the HA pipeline's name, news lines.

## Decision

1. **One name, set once.** box.yml `assistant:` (one word of letters;
   „Homes“ when unset) is the name the family talks to. It reaches units as
   the builtin `${ASSISTANT}`, hooks as `LAIB_ASSISTANT`, and a news line's
   `${ASSISTANT}` on the card. Nothing else holds the name:
   - the Bote: `bote-directory` gets `ASSISTANT` (the prompt `HOMES>`, the
     help „Hallo, hier ist Homes.“); its post-deploy writes the SOUL's first
     line from `LAIB_ASSISTANT`;
   - Haus: `HAUS_ASSISTANT` (header, Sprechen, Willkommen, „Gemeinsam mit
     Homes“), and `GET /napi/assistent` → `{"name": …}` for the app;
   - the app: the name from `/napi/assistent`; its colours and icon are its
     own;
   - Home Assistant: the Assist pipeline is named `${ASSISTANT}`.
   Changing the name is a box.yml change like any other: a plan shows the
   services whose units carry it.
2. **No personas.** No packages, no `chatPersonas`, no style per person, no
   gimmicks, no per-persona icon or colours. The SOUL's style is fixed in
   the Bote's post-deploy next to the rules it always had.
3. **The wake word is „Hey Mycroft“**, the pretrained `hey_mycroft` that
   openWakeWord ships and the Voice PE's firmware offers out of the box.
   Laib selects it on the satellites (stimme-names, home-assistant's
   post-deploy) and names it in the pipeline; it trains and flashes nothing.
   The trainer, its image and workflow, the retrain and flash units, the
   `retrain` and `flash` verbs, Haus's recording page and the negatives go.
4. **The look is fixed**: accent #3ddc84 on ground #0c120e, in Haus and the
   app, as constants there. **The voice is fixed**: Piper
   `de_DE-thorsten-high`, a literal in the stimme template.
5. **What the Bote needed that was no costume stays, renamed for what it
   does** (ADR 0021 rule 4): the directory of the Bote's tools, its levels
   and context, the plain help (#863), the admin-only box level and the
   Box-Aufgabe hand-off (#878) are the program `directory/` (image
   `laib-directory`, container `bote-directory`, tool and Hermes plugin
   `laib-directory`, data `bote/directory/`), with its own line budget.

## Consequences

- Slimmer: the persona loader and renderer, the builtin Joshua package,
  `persona-icon.sh`, the games, the trainer and its tests, the flash and
  retrain paths are deleted; the directory's cap drops from 990 to 530.
- The family hears and reads one name everywhere; changing it is one line in
  box.yml.
- A satellite whose firmware does not offer „Hey Mycroft“ (not a Voice PE)
  shows up yellow in Stimme's status with what to do; Laib no longer builds
  firmware for a wake word.
- A box.yml `persona:` or `chatPersonas:` key from before is ignored (box.yml
  keys Laib does not know are not read); remove it when you next edit
  box.yml. `render/persona/`, `bote/persona/`, `stimme/micro/hey_joshua.*`,
  `stimme/recordings/` and `stimme/trainer/` stay on disk until the owner
  deletes them; nothing reads them.
- `laib-directory` is a new ghcr package: it must be public before the bote
  template update is applied, as `laib-persona` was.
- News lines from before that name `${PERSONA_DISPLAY}` count as their
  `${ASSISTANT}` form when a template update compares them (ADR 0021 §8,
  until every builtin template was updated once on the box).
