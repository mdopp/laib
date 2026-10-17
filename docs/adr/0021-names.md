# ADR 0021 — Names: one name, one thing; the family sees the persona

Applies when you name a template, a container, an image, a Bote tool, a
hand, a Go package or a page title; when a word you are about to use
already means something else in the house; or when you meet "Solaris",
"Balthasar" or "ServiceBay" in code and wonder whether it is still meant.

- **Status:** Accepted, 2026-10-13 (#132)
- **Deciders:** operator (mdopp): "Ist das alles klar und direkt
  ersichtlich, oder ist das historisch gewachsen und wir müssen eine klare
  Architektur und Namensstruktur einführen?" — "Mach das zu issues und
  arbeite die alle ab."

## Context

The cut between the pieces is clear (ADR 0014, 0016, 0020). The names
were not: three renames (Solaris → Haus, Balthasar → Joshua, easter egg →
persona) left words that mean several things. "Haus" was the pages, the
Bote's Home Assistant tool, a level in Joshua's directory and, nearly, the
Haushalt. "Joshua" was a persona and also the package, tool, image and
container of the engine that runs every persona. There was no rule for
which kind of name goes where.

## Decision

1. **One name, one thing.** A name used for a service is not also used for
   a tool, a level or a mechanism elsewhere. When two things share a word,
   the one that is not the family-facing meaning is renamed.
2. **The family sees the persona** (ADR 0020) and German words for what a
   thing does: Licht, Kalender, Freigabe. Never a template, hand, image or
   tool name.
3. **A service is one German noun**, and that noun is everything about
   it: the template, the pod or unit (`bote`, `bote-*`), the data dir
   (`${DATA_DIR}/bote`), the secret prefix (`BOTE_`) and its hand where it
   has one. The services are Bote, Haus, Stimme, Frage and Werkstatt, plus
   the plain upstream ones (immich, radicale, …).
4. **A mechanism is named for what it does, in English**: relay, lease,
   approver, persona (the engine), watchdog. A mechanism is never named
   after the persona, the person or the box it serves first. Programs this
   repo builds are imaged as `laib-<program>` and run as `<pod>-<program>`.
5. **A Bote tool is named for the system it reaches**: `laib`, `ha`,
   `frage`, `haushalt`, `automat`, `persona`. The directory label the
   family reads is separate and German.
6. **A hand is named for who holds it**: `human`, `resident`, `ops`, `pi`,
   `claude`, `bote`, `approver`, `haus`.
7. **Upstream products keep their names**: Hermes, Home Assistant,
   signal-cli, Caddy, Authelia, Piper, Whisper, openWakeWord,
   microWakeWord, SearXNG.
8. **A legacy name lives only in a named place**: a migration bridge or a
   contract with installed devices. It is listed below with its end
   condition and is removed when that condition holds. New code never
   takes one on.
9. **Machines are named by their role in the house**, not as services:
   the Kraftwerk (the GPU box) and the NUC.

## Glossary

| Name | What it is | Where it lives |
|---|---|---|
| Laib | the kernel: state repo, plan/apply, MCP, the Box page | `cmd/laib`, `internal/` |
| Operator | the box's admin agent, hand `ops` | `operator/` (ADR 0013, 0023) |
| skill | a declared unit of the Operator's work: trigger, tools, prompt, output; active or paused in `policy.yml`; *Fähigkeit* on the pages; not Hermes' skills (the Bote's are off) | `skills/` (built in), `skills/` in the state repo (ADR 0023) |
| Werkstatt | the dev container: Claude Code and pi, and the autoloop that works issues | `templates/werkstatt`, `werkstatt/` |
| Bote | the agent people write to, on Signal and in Haus's Sprechen field | `templates/bote` (ADR 0016) |
| Hermes | the upstream agent the Bote is built from, as stock | container `bote-hermes` |
| relay | approvals by the admin's verified number, code not model | `cmd/relay`, container `bote-relay` |
| Automat | Home Assistant automations as a Bote tool | `cmd/automat`, container `bote-automat`, tool `automat` |
| notify | a message for a person on their chat channel, sent by the Bote with `hermes send` (ADR 0022 §3); Home Assistant calls it as `rest_command.mitteilung` | `templates/bote/notify.py`, container `bote-notify` |
| persona (engine) | where a person stands in the Bote: directory, levels, context, gimmicks | `internal/persona`, container `bote-persona`, tool `persona` |
| Joshua | the first persona: WOPR from WarGames, "Hey Joshua" | persona package (ADR 0020) |
| Haus | the family's surface: pages, cards, `/napi`, the app's server | repo `mdopp/haus`, `templates/haus`, `haus.<domain>` |
| Haushalt | the household's data: calendar, contacts, notes, reminders | Haus's MCP source (ADR 0017), Bote tool `haushalt` |
| memory library | one search over the household's notes, by words and by meaning (ADR 0017 §3, haus#13) | qmd, container `haus-qmd` in the haus pod (#195) |
| Vermittler | serves the memory library: qmd's `/query` on qmd's SDK, plus each hit's cosine (`vec`), until qmd returns it itself | `qmd/vermittler.mjs`, container `haus-qmd` (#198) |
| ha | Home Assistant as a Bote tool: lights, switches, players | Bote tool `ha` |
| Stimme | the voice set Home Assistant Assist speaks: wake word, Whisper, Piper, trainer | `templates/stimme` (ADR 0015) |
| Frage | "was ist …": search plus summary | `cmd/frage`, `templates/frage` |
| Kraftwerk | the GPU box | — |
| NUC | the trial box | — |

### Legacy names

| Name | Where it still is | Ends when |
|---|---|---|
| ServiceBay | the predecessor, migrated once | `scripts/migrate-servicebay.py` is run on the last box |

Ended (2026-10-14, ADR 0022): Solaris, `sol_device_`, `cloud.dopp.solaris://`
and `/napi/servicebay` — nothing of the Solaris app is carried; the new app
has its own names. Ended (2026-10-13): `solaris-wakeword-trainer` and Balthasar. #128 built
the trainer in this repo (`laib-wakeword-trainer`), and Stimme trains the
house persona's phrase.

## Consequences

- #133 renames the engine (`joshua` → `persona`), #134 the Bote's Home
  Assistant tool (`haus` → `ha`), #135 fixes the docs that contradict each
  other; haus#5 confines Solaris to the bridge.
- ADR 0020's "internal names stay: … the `joshua` tool" is amended: the
  engine and the tool are named `persona`, and Joshua is data.
- A new name is checked against this table first. A name that would need
  a second row is rule 1's case.
