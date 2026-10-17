# Deconstructing Solaris and solaris-android, with Laib

A fresh cut, 2026-10-10. The operator asked for sensible, maintainable
constructs and gave the one observation that changes everything: *the web
chat is rarely used; what is used is switching things and finding state —
concrete pages and cards.*

## What is there today (measured)

| Repo / part | Size | What it really is |
|---|---|---|
| `solarisbay/solaris-chat/server.py` | 7,226 lines, one file | chat API, portal pages (`/p/start`, `/p/energy`, `/p/device`, `/p/notes`, `/p/servicebay`), companion API `/napi`, pairing, Web Push, wakeword, favorites, topics, skills, model lease, admin |
| `solaris-chat/engine/` | 9,700 lines | the agent loop, 6,300 of it tools (HA, media, notes, documents, calendar, timers, onboarding, MCP) |
| `engine/ingest` + `engine/knowledge` | 6,100 lines | memory: Google Takeout importers, OKF vectors, notes search |
| `solaris-chat/static/index.html` | 616 KB, one file | the PWA: tabs home, chats, notes, documents, favorites |
| `voice-gatekeeper`, `tts-martin`, `wakeword-trainer` | 7,800 lines | the voice path: Wyoming satellites, speaker identity, TTS, training |
| `solaris-android` | 22,000 lines Kotlin | a TWA wrapper of the PWA plus native widgets: devices, energy, camera, lock, approvals, notices, diagnostics, active devices; device tokens; an SSE foreground service |
| `pi-web`, `templates/llama`, lease | moving | Werkstatt and the Laib `model` role (ADR 0011) |

The product people use is the **cards**: favorites on `/p/start`, the device
and energy widgets on the phone, the quick-settings tile. The chat is the
way to *create* a card ("pack das Bürolicht auf meine Startseite") and to
ask something the cards cannot show. Everything else in `server.py` is
plumbing for those two things plus the admin's own needs.

## The cut: six constructs, one job each

*Superseded in part (2026-10-14).* The diagram and "Solaris becomes a brain
with an API" below are the plan of 2026-10-10. Since then:
- the Bote (Hermes, ADR 0016) is the agent people talk to;
- Solaris retires (ADR 0015);
- the memory moves into Haus (haus#13).

The table is current.

```
Stimme ──▶ Solaris (the agent) ◀── Haus (the surface) ◀── Haus-Android (widgets, TWA)
                 │                       │
                 ▼                       ▼
            Laib MCP  ◀── Laib Operator (the box's admin agent)
                 │
              Laib (the box)   ◀── Werkstatt (where things get built)
```

| Construct | Repo | Owns | Never owns |
|---|---|---|---|
| **Laib** | `mdopp/laib` | the box: state, apply, converge, proxy, sso, dns, model, backup, journal, inbox | an LLM call, a household page |
| **Laib Operator** | `mdopp/laib` (`operator/`, template `operator`) | the box's admin agent: watch, inspect, correct, escalate, morning report (ADR 0013) | the household, voice, a chat UI |
| **Solaris** | retired (ADR 0015, proposed) | nothing; its memory moves into Haus as a library, its spoken intents into HA Assist, "was ist …" into Frage | — |
| **Stimme** | templates in the household registry | openWakeWord, Whisper, Martin TTS, the wakeword trainer — the Wyoming set HA Assist speaks | identity: everyone is a guest |
| **Frage** | template in the household registry (~300 lines) | SearXNG search + llama summary; Assist's conversation agent and the Bote's `frage` tool (the Sprechen field goes to the Bote, ADR 0015 amendment) | memory, tools, a loop |
| **Haus** | new: `mdopp/haus` | the surface: pages and cards, companion API `/napi`, push, device tokens, favorites, energy, HA state aggregation, the "Sprechen" field that talks to the Bote, the memory library (haus#13) | an agent loop, a model |
| **Haus app** | new (ADR 0022), successor of `mdopp/solaris-android` | the home-screen widgets and pairing; a live stream for states while the screen is on | a shell around web pages, notices, approvals, its own backend logic |
| **Bote** | Hermes as stock, template `bote` | the agent you talk to: Signal, the Sprechen field; house, box (read + propose), Frage, memory, approvals as the admin by verified number (ADR 0016) | the voice path, box repairs |
| **Werkstatt** | `mdopp/laib` (`werkstatt/`) | pi, Claude Code, autoloop, tickets, transcripts | a chat for the family |

Why this cut holds:

- **Solaris becomes a brain with an API**, about 10k lines: `POST /v1/turn`
  (text or transcript in; text, cards and actions out, streamed) and
  `GET /v1/events` (timers, reminders, proactive notices). Stimme and Haus
  are its two clients. Nothing in it renders HTML.
- **Haus is a BFF plus pages**, the ServiceBay ADR 0011 idea kept and
  moved: one pairing, one token, one SSE for the phone; HA, Laib and
  Solaris aggregated behind `/napi`. The companion contract
  (`companion-api.md`) moves with it unchanged, so the Android widgets
  change a hostname and the asset links, nothing else.
- **The Operator is its own binary in the Laib repo**, not a Solaris
  profile: different model cadence, different memory (the journal), no
  voice, and the decision "no LLM in the kernel" stays true because it is
  a separate program and template.
- **The 616 KB file dies.** Haus pages are server-rendered with a small
  amount of HTMX-style updates over SSE; each page is a file.

## Haus: the pages and the cards

Every page is behind Authelia, `Remote-User` selects the person. Cards are
one component with one renderer, used by pages, by the chat replies and by
the Android widgets (which get the same JSON from `/napi`).

| Page | Cards | Who |
|---|---|---|
| **Start** (`/`) | *Jetzt*: wer ist zu Hause, Lichter an, läuft Musik, nächster Termin · *Meine Favoriten* · *Haushalt* · *Häufig genutzt* | everyone, personal section per person |
| **Räume** (`/raeume`) | one card per room: Lichter, Heizung, Rollläden, Medien, Präsenz; tap toggles, long-press opens the device card | everyone |
| **Energie** (`/energie`) | PV/Haus/Netz/Akku flow tiles, totals, circuits, 24h/7d charts (as today) | everyone |
| **Medien** (`/medien`) | Läuft gerade (Jellyfin, Radio), Weiterschauen, Radio-Presets | everyone |
| **Notizen & Dokumente** (`/notizen`) | Suche, Zuletzt, Paperless-Eingang (Anzahl), Diktat | per person |
| **Kalender** (`/kalender`) | Heute, Erinnerungen, Timer | per person |
| **Box** (`/box`) | *Laib*: readiness line · *Inbox*: proposals read-only with their card, and a link to Laib's inbox (Haus decides nothing, haus#1) · *Morgenbericht* · *Was läuft*: services grouped by purpose · *Operator*: was ich gerade tue · *Werkstatt*: offene Tickets · *Stimme*: the persona's wake word, trained or not (training follows the persona, #128) | admins |

**Sprechen** is not a page: a microphone/text field on every page that
talks to the Bote. The reply comes back as transient cards into the current page ("Bürolicht ist
aus", with the device card), with "auf meine Startseite" as the pin
affordance. The Android quick-settings tile opens exactly this field.

**Widgets (Android, kept):** Geräte, Energie, Kamera, Schloss, Aktive
Geräte, Mitteilungen; new: *Box* (readiness + inbox count for admins).

## What disappears

- The chat as primary surface: the `chats` tab, the session list UI.
  Sessions go with Solaris; the Bote keeps its own (Hermes), and Haus's
  memory library (haus#13) is the one search.
- Solaris's admin profile, `servicebay_admin` toolbox, `sb_*` modules,
  `/p/servicebay`, the model widget: the Operator and Laib's pages take
  over (about 2,000 lines).
- The model lease server in Solaris: the `llama` template's sidecar
  (ADR 0011). Solaris keeps a client.
- `pi-web`: the Werkstatt.
- Web Push as a second notification path for the app: Haus keeps Web
  Push for browsers and the SSE stream for the app, as today, but in one
  module with one store.

## Order of work

1. **Laib Operator** (`cmd/laib-operator`): the loop, the ops profile,
   the morning report, the webhook receiver, the escalation to the
   Werkstatt. Solaris drops the admin profile the same week.
2. **llama to Laib** (issue #18), lease sidecar; Solaris lease becomes a
   client.
3. **Haus, step one: lift and shift.** New repo; move `/p/*`, `/napi`,
   pairing, push, favorites, topics, energy, HA aggregation out of
   `server.py` as they are. Solaris keeps `/api/chat`, sessions, voice,
   soul, skills. Android app: hostname + asset links. Nothing is
   redesigned yet; `server.py` falls to ~2k lines.
4. **Haus, step two: the pages.** Replace `index.html` page by page,
   Start first (it already exists as `/p/start`), Räume second. Each page
   a file, cards one renderer.
5. **Solaris retired (ADR 0015, proposed).** The five spoken intents are
   Home Assistant Assist with Music Assistant; "was ist …" is the small
   Frage service as Assist's conversation agent; the memory (ingest, OKF,
   notes search) becomes a library inside Haus. Soul, skills, sessions and
   the chat go away. Gate: the two checks in ADR 0015 on the box.
6. **Stimme**, **Frage**, **music-assistant** and **searxng** as templates
   in the household registry; the speaker-identity gatekeeper is dropped;
   `solarisbay` is archived once Haus holds the memory library.
7. ~~Rename `solaris-android` to `haus-android`.~~ Superseded by ADR 0022:
   a new widget-only app with its own names; nothing of the old one is
   carried. Messages to people go through their chat channel.

Each step leaves every surface working. Steps 1 and 2 are Laib work and
start now; 3 to 7 are solarisbay and haus work with their own issues.

**Stand 2026-10-13** (tracking: #21):
- **Steps 1 and 2:** done. The Operator is `operator/` (its own budget, ADR 0023), and llama is a Laib role.
- **Step 3:** half done.
  - Haus has the Box page, pairing (`/pair-device`), the four companion keys and the persona.
  - The house paths, tool rows, camera and upload come from Haus (haus#18, haus#25). The bridge and the old names go by ADR 0022.
- **Step 4:** open (haus#12).
- **Step 5:** waits on the two checks of ADR 0015 on the box. The Bote (ADR 0016), not Solaris, is the agent people talk to.
- **Step 6:** done as built-in templates (the registry repo is postponed). The gatekeeper is dropped.
- **Step 7:** superseded (above).

## Risks named

- Step 3 is a large mechanical move inside one 7,000-line file; it needs
  the route inventory above and tests per route group before the move.
- Two repos share the `/napi` contract; it is documented and must stay
  the contract, not a convention.
- The Operator must not become a second Solaris: no memory beyond the
  journal and an incidents file, no voice, no household tools.
