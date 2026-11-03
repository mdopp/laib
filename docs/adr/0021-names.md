# ADR 0021 — Names: one name, one thing; the family sees the assistant's name

Applies when you name a template, a container, an image, a Bote tool, a
hand, a Go package or a page title; when a word you are about to use
already means something else in the house; or when you meet "Solaris",
"Balthasar" or "ServiceBay" in code and wonder whether it is still meant.

- **Status:** Accepted, 2026-10-13 (#132). Rule 2 and the glossary's persona
  rows amended by ADR 0030 (2026-11-02): no personas, one assistant name.
  Rules 1, 3 and 6 amended by ADR 0031 (2026-11-03) for one service: Haus
  is renamed Homes (template, hand `homes`, app), and the program, the app
  and the assistant share that one name.
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
2. **The family sees the assistant's name** (box.yml `assistant`, ADR 0030;
   until then the persona, ADR 0020) and German words for what a thing
   does: Licht, Kalender, Freigabe. Never a template, hand, image or tool
   name.
3. **A service is one German noun**, and that noun is everything about
   it: the template, the pod or unit (`bote`, `bote-*`), the data dir
   (`${DATA_DIR}/bote`), the secret prefix (`BOTE_`) and its hand where it
   has one. The services are Bote, Haus, Stimme, Frage and Werkstatt, plus
   the plain upstream ones (immich, radicale, …).
4. **A mechanism is named for what it does, in English**: lease,
   directory (the persona engine until ADR 0030), watchdog. A mechanism is
   never named after the assistant, the person or the box it serves first. Programs this
   repo builds are imaged as `laib-<program>` and run as `<pod>-<program>`.
5. **A Bote tool is named for the system it reaches**: `laib`, `ha`,
   `frage`, `haushalt`, `automat`, `directory`. The directory label the
   family reads is separate and German.
6. **A hand is named for who holds it**: `human`, `resident`, `ops`, `pi`,
   `claude`, `bote`, `haus`.
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
| skill | a declared unit of the Operator's work: trigger, tools, prompt, output, where the model decides (`modell`); a folder, `skill.yml` and, built in, `run.py`, its script (#839); active or paused in `policy.yml`; *Fähigkeit* on the pages; not Hermes' skills (the Bote's are off) | `skills/` (built in), `skills/` in the state repo (ADR 0023) |
| lesson | what the Operator learned from a solved incident — finding, cause, fix — proposed by the `lehre` skill, let in by a human; text in every session's context, never a rule; *Lehre* on the pages | `lessons/<name>.md` in the state repo (#544) |
| log review | the daily `durchsicht` skill: the units' error lines grouped by pattern in its script, real errors told from noise by content, only new, recurring or growing patterns shown to the model, each first checked as a person would (planned stop, over, resolved); *Protokoll-Durchsicht* on the pages | `skills/durchsicht/` (#550, #551, #839, #975) |
| maintenance marker | an action's announcement before it stops or starts services on purpose: what it acts on, their dependents, who, until when; cleared at its end, a red finding past its until; what „planned“ means for diag and the skills | `internal/apply/marker.go`, `LAIB_HOME/markers/`, verb `laib planned` (#978) |
| outcome | what came of a proposal, recorded by laib: effective, no effect, rolled back, rejected with the owner's reason, later (a free „nicht jetzt“), applied (nothing to measure) | the inbox entry's `outcome`, journal verb `outcome` (#544) |
| Werkstatt | the dev container: Claude Code and pi, and the autoloop that works issues | `templates/werkstatt`, `werkstatt/` |
| Bote | the agent people write to, on Signal and in Homes's Sprechen field | `templates/bote` (ADR 0016) |
| Hermes | the upstream agent the Bote is built from, as stock | container `bote-hermes` |
| Automat | Home Assistant automations as a Bote tool | `automat/`, container `bote-automat`, tool `automat` |
| notify | a message for a person on their chat channel, sent by the Bote with `hermes send` (ADR 0022 §3); Home Assistant calls it as `rest_command.mitteilung` | `templates/bote/notify.py`, container `bote-notify` |
| assistant | the name the family talks to, set once (ADR 0030): „Homes“ by default | `box.yml assistant`, the builtin `${ASSISTANT}`, `LAIB_ASSISTANT` in hooks |
| directory | where a person stands in the Bote: its tools, levels, context and help, in the assistant's name | `directory/`, container `bote-directory`, tool and plugin `laib-directory` |
| Homes | the family's surface: pages, cards, `/napi`, the app's server, and the app; was Haus until ADR 0031; the assistant's name too (ADR 0030) | repo `mdopp/homes`, app `mdopp/homes-android`, `templates/homes`, `homes.<domain>`, hand `homes` |
| Zuhause | the household's address and coordinates, once for every template (#561, ADR 0017 amendment) | `box.yml zuhause`, the `ZUHAUSE_*` builtins, verb `laib zuhause` |
| Haushalt | the household's data: calendar, contacts, notes, reminders | Homes's MCP source (ADR 0017), Bote tool `haushalt` |
| memory library | one search over the household's notes, by words and by meaning (ADR 0017 §3, haus#13) | qmd, container `homes-qmd` in the homes pod (#195) |
| Vermittler | serves the memory library: qmd's `/query` on qmd's SDK, plus each hit's cosine (`vec`), until qmd returns it itself | `qmd/vermittler.mjs`, container `homes-qmd` (#198) |
| ha | Home Assistant as a Bote tool: lights, switches, players | Bote tool `ha` |
| Stimme | the voice set Home Assistant Assist speaks: wake word („Hey Mycroft“), Whisper, Piper | `templates/stimme` (ADR 0015, ADR 0030) |
| Frage | "was ist …": search plus summary | `frage/`, `templates/frage` |
| Kraftwerk | the GPU box | — |
| NUC | the trial box | — |
| instance | a per-person template installed for one person (`perPerson`, `laib add --person`), the service `<template>-<person>`; its route shares one name with the template's other instances (#986); *Instanz* | `services/<template>-<person>/laib.yml` `person:` |
| Archiv | a person's message archive (mail, WhatsApp, Signal): one msgvault instance per person, behind `archiv.<domain>` (ADR 0029) | template `msgvault`, `${DATA_DIR}/msgvault-<person>/` |
| Tresor | the encrypted folder holding one person's Archiv and their keys; only that person opens it, it locks after two idle hours (ADR 0029); not Vaultwarden | inside `${DATA_DIR}/msgvault-<person>/` |
| Archiv-Eingang | where Homes puts a person's phone backups, still encrypted, until their Tresor opens (ADR 0029) | `${DATA_DIR}/archiv-eingang/<person>/` |

### Legacy names

| Name | Where it still is | Ends when |
|---|---|---|
| ServiceBay | the predecessor, migrated once (2026-10-19, ADR 0010) | its data dir is deleted, 30 days after migration day |
| `haus.<domain>` | the homes template's second route: the old Haus app's `/napi/` calls with their device token (ADR 0031) | every phone runs the Homes app (#1021 step 9); then a `redirectFrom` |
| `haus_device_` | the device-token prefix of phones paired with the Haus app; Homes accepts it (ADR 0031) | every phone runs the Homes app (#1021 step 9) |
| `was: HAUS_…`, `was: ${DATA_DIR}/haus…` | the `was:` lines in templates/homes and the templates that declare its secrets (ADR 0031) | the box moved (#1021 step 7) |
| `X-Archiv-Haus`, `ARCHIV_HAUS_SECRET` | archiv reads them beside `X-Archiv-Homes` and `ARCHIV_HOMES_SECRET` (ADR 0031) | the box moved (#1021 step 7) |
| `X-HAUS-*`, Haus PRODIDs | iCal properties and PRODIDs in entries Radicale already stores: data, kept on purpose (ADR 0031) | never in code; the stored entries keep them |
| `haus` (accounts) | the household's logins named `haus` in Radicale/LLDAP, Audiobookshelf, Jellyfin, Paperless and Home Assistant (user „Haus“), and the hook labels „Haus (laib)“, „Haus (Gesehen)“, `laib-haus` (ADR 0031) | the owner's decision in #1021 |

### Retired names

A name here is past: a current doc (README.md, CLAUDE.md, `docs/*.md`)
names it only where its line, its section or its file says so
(`scripts/drift.sh` check 8, #865). New code never takes one on.

| Name | Ended | Instead |
|---|---|---|
| Solaris | 2026-10-14, ADR 0015, ADR 0022 | the Bote (chat), Haus (pages, cards), the Operator (the admin agent) |
| Balthasar | 2026-10-13 | Joshua, since ADR 0030 Homes |
| Joshua | 2026-11-02, ADR 0030 | the assistant's name, box.yml `assistant` (Homes) |
| persona | 2026-11-02, ADR 0030 | the assistant's name; the engine is the directory |
| laib-wakeword-trainer | 2026-11-02, ADR 0030 | the pretrained „Hey Mycroft“ |
| relay | 2026-10-26, #778 | the Inbox: approvals happen only there |
| approver | 2026-10-26, #778 (the relay's hand) | the hand `human` in the Inbox |

Ended (2026-10-14, ADR 0022): Solaris, `sol_device_`, `cloud.dopp.solaris://`
and `/napi/servicebay` — nothing of the Solaris app is carried; the new app
has its own names. Ended (2026-10-13): `solaris-wakeword-trainer` and Balthasar. #128 built
the trainer in this repo (`laib-wakeword-trainer`), and Stimme trains the
house persona's phrase. Ended (2026-11-02, ADR 0030): that trainer, Joshua
and the personas; Stimme wakes on the pretrained „Hey Mycroft“.

## Consequences

- #133 renames the engine (`joshua` → `persona`), #134 the Bote's Home
  Assistant tool (`haus` → `ha`), #135 fixes the docs that contradict each
  other; haus#5 confines Solaris to the bridge.
- ADR 0020's "internal names stay: … the `joshua` tool" is amended: the
  engine and the tool are named `persona`, and Joshua is data.
- A new name is checked against this table first. A name that would need
  a second row is rule 1's case.
