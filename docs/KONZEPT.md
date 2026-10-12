# Laib — the whole concept

State of the decisions on 2026-10-10. One sentence: **a box that keeps
itself in the state its owner declared, an operator agent that repairs
it, a messenger agent the family talks to, Home Assistant for the house
and the voice, and pages with cards for everything you look at or
switch.** Humans decide the irreversible; everything else happens and is
written down.

## The constructs

| Construct | What it is | Repo | Runs as |
|---|---|---|---|
| **Laib** | the control plane: state repo, plan/apply/converge, proxy, SSO, DNS, model role, backup, journal, inbox, `/mcp` | `mdopp/laib` | one binary, user service |
| **Operator** | the box's admin agent: watches, inspects, corrects inside the envelope, escalates, writes the morning report (ADR 0013) | `mdopp/laib` `cmd/laib-operator` | template `operator` |
| **Bote** | the agent you talk to: Signal (later Matrix, Telegram), the Sprechen field in Haus; house, box, questions, memory, approvals as you (ADR 0016) | Hermes, stock, configured by a template | template `bote` |
| **Haus** | the surface: pages and cards, companion API for the phone, push, favorites, energy, the memory library (ADR 0014) | `mdopp/haus` | template `haus` |
| **Haus-Android** | the phone: TWA of Haus plus native widgets and the quick-settings tile | `mdopp/haus-android` | app |
| **Home Assistant** | the house brain: devices, automations, Assist (intents, timers, media via Music Assistant) | upstream | template `home-assistant` |
| **Stimme** | the Wyoming set Assist speaks: wake word, Whisper, the Martin voice, the wakeword trainer (ADR 0015) | household registry | templates |
| **Frage** | search + summary for "was ist …": Assist's conversation agent and a Bote tool | household registry (~300 lines) | template `frage` |
| **llama** | the model role: router, policy proxy, lease, embeddings (ADR 0011) | `mdopp/laib` template | template `llama` |
| **Werkstatt** | where things get built: pi, Claude Code, autoloop, tickets, transcripts | `mdopp/laib` `werkstatt/` | template `werkstatt` |
| **Household registry** | every template the family uses, one Git repo with `laib template lint` in CI | `mdopp/laib-templates` | files |

Retired: ServiceBay (one migration), Solaris as an agent (its memory lives
on in Haus), the speaker-identity gatekeeper, pi-web as a separate
surface, the web chat as a primary surface.

## Division of labour

| Task | Who | How it shows up |
|---|---|---|
| Keep the box in the declared state | Laib (converge, systemd) | drift reverted, units restarted, journal |
| Install, update, roll back, back up | Laib | proposals and the dial, timers, journal |
| Notice a red, look inside, fix it, learn | Operator | `inspect`, `exec`, `restart`, values, journal; morning report |
| Escalate what it cannot fix | Operator → Werkstatt (Claude) | ticket with context bundle; result as PR or journal line |
| Talk with the family, answer, relay, approve as the admin | Bote | Signal; Sprechen field; cards in Haus |
| Switch, time, play, answer "was ist" by voice | Home Assistant Assist + Stimme + Frage | the speaker answers; no LLM for intents |
| Show state, switch by tap, pin favorites | Haus + Haus-Android | pages, widgets, tile |
| Build a template, a migration, a fix nobody has | Werkstatt: pi first, Claude for the hard ones | PR to the household registry; human merges |
| Decide the irreversible | the human | Box page or verified Signal: remove, reboot, public route, money, external secrets |
| Keep the knowledge | the journal (facts), the Haus memory (household), the Laib docs and ADRs (decisions), the Werkstatt transcripts (how) | all searchable |

## Hands, tiers, tokens

| Hand | Who | Ceiling | May | Never |
|---|---|---|---|---|
| resident | family through Haus | read | see status cards | change the box |
| bote | the Bote | read + propose | read box state and journal; propose; everything in the house via HA; Frage; memory | approve (its own token) |
| ops | the Operator | apply | all of class 1 and 2 (ADR 0013) inside the envelope | remove, reboot, public routes, secrets, tokens |
| pi | the Werkstatt's local coder | propose | propose template updates and values | apply |
| claude | Claude in the Werkstatt | apply, TTL | everything ops may, plus writing templates and migrations | class 3 |
| human | you, on the Box page, the host shell, or your verified Signal number | destroy | everything | — |

Tokens come from `laib token mint <hand>` by a human; the Bote's approval
token is a `human` token bound to one Signal number, revocable with one
command.

## Interfaces by user group

### Family (Bewohner)

- **Voice at home:** wake word, then: Wecker und Timer · "spiele Einslive"
  · "spiele Musik von …" / a playlist · "was ist …" · Schalter, Licht,
  Szenen. Guest mode for everyone: nobody is recognised, nobody needs to be.
- **Haus on the phone and in the browser:** Start (Jetzt, Favoriten,
  Haushalt, Häufig), Räume, Energie, Medien, Notizen & Dokumente,
  Kalender. The Sprechen field on every page talks to the Bote; the reply
  is a card.
- **Widgets:** Geräte, Energie, Kamera, Schloss, Aktive Geräte,
  Mitteilungen; the quick-settings tile opens Sprechen.
- **Signal (optional per family member):** the Bote with their own memory
  namespace; house and questions, no box.

### Guests

- Voice, the same five intents. No pages, no Signal, no memory.

### Admin

- **Signal with the Bote:** "was gibt's Neues" (the morning report), "wie
  geht's der Box", "gib #17 frei", "frag Claude, warum …".
- **Box page** `laib.<domain>`: Status, Inbox with Freigeben/Ablehnen,
  Journal. Also as cards on Haus's Box page: Operator "was ich gerade tue",
  Was läuft by purpose, Werkstatt tickets, Wakeword training, resident
  onboarding against LLDAP.
- **Werkstatt** `dev.<domain>` and the Claude Code mobile app: watch pi and
  Claude work, take over, build yourself.
- **Host shell as `core`:** the human hand; `laib` with every verb.

### Agents

- **Operator:** Laib MCP as `ops`; wakes on the webhook, the ten-minute
  tick and the nightly report; llama household preset.
- **Bote (Hermes):** Signal gateway; HA integration; Laib MCP as `bote`;
  the Haus MCP source "Haushalt" (calendar, contacts, notes, documents,
  memory) and Frage as tools; reminders are calendar entries, not cron. Because nothing
  here is latency-critical, the Bote may ask the lease for the
  `thinking` preset (qwen3.6-35b-a3b, 131k context, MTP drafter) for a
  hard question and fall back to the household preset when the card is
  busy.
- **Claude:** through the Werkstatt as `claude`; receives escalations
  with `laib context`; answers come back as PRs, journal lines and, via
  the Bote, on Signal.
- **pi:** Werkstatt tickets labelled `pi:ready`; proposes, never applies;
  a ticket it cannot finish gets `needs-claude`. Inside the Werkstatt,
  `laib` is a shell command (laibctl over the MCP, token from a 0600 file)
  for pi, Claude Code and a human alike.

### Template authors

- A folder: Quadlet units, `laib.yml` (routes, secrets, data, health,
  ports, provides, after, hostNetwork), `values.example.yml`, optional
  `post-deploy` hook. `laib template lint` is the contract. `podlet` turns
  an upstream compose file into units.

## Where the data lives (ADR 0017)

| Data | Store | Written by | Read by | Surface |
|---|---|---|---|---|
| Appointments | Radicale (CalDAV) | you on the phone, the Bote, imports | HA (CalDAV integration), Haus Kalender, the Bote | phone calendar via DAVx⁵, Haus |
| Contacts | Radicale (CardDAV) | you, imports | the Bote, Haus | phone contacts, Haus |
| Reminders | calendar entries with alarms in Radicale | the Bote | HA rings, Haus push, phone | the same |
| Timers, alarms | Home Assistant | voice | HA | the speaker |
| Notes | Markdown files in the file share (Syncthing) | you with any app, the Bote | Haus search (memory library), the Bote | Haus Notizen, any phone app |
| Documents | Paperless | scanner, import | Haus, the Bote | Paperless, Haus |
| What the Bote knows about you | Hermes memory (Honcho), per number | the Bote | the Bote | none |

Agents reach all of it through one MCP source, **Haushalt**, served by
Haus (calendar, contacts, notes, document search, memory search); Claude
mounts the same source in the Werkstatt. The Bote remembers *about* you,
never *for* you.

## Models and the card

One llama-server in router mode, four presets, the lease decides who
holds the card (ADR 0011):

| Preset | Used by | Why |
|---|---|---|
| household (gemma-4-e4b, MTP, 32k) | Operator, Frage, Bote by default | fast, always resident |
| thinking (qwen3.6-35b-a3b, MTP, 131k) | Bote for hard questions, long documents | text waits happily two seconds |
| coding (qwen3.8-27b) | Werkstatt (pi) | the window is a timer; the Bote can propose to move it ("heute Filmabend") |
| foundry (gemma-4-12b) | foundry-chronicle | unchanged |

Voice has no LLM in its path; the card is free for the agents.

## One box, and a trial box

The end state is one box, the Kraftwerk (the GPU box). The Intel NUC is
the proving ground for the kernel, the agents and Haus: no GPU, so
`box.yml model.url` points at the Kraftwerk's model proxy; wiped after
the trial. Two boxes with fixed roles would work (ADR 0018, parked) but
are not the plan. The path is `docs/ZIELBILD.md`.

## Three days in the life

**A wish on the road.** You write the Bote: "Paperless für die Familie,
nur intern." It knows the template exists, proposes with `--add` as
`bote`, and sends you the card. You answer "gib #17 frei"; your number is
you; Laib merges, applies, verifies, the hook creates the admin user, the
journal is green, the Bote writes "Läuft unter paperless.deinedomain, SSO
ist drauf."

**A red at three in the morning.** Auto-update pulls a new Immich;
`diag --journal` finds the health red; the Operator's next tick (ten
minutes; a webhook is planned, not wired) picks it up; it
inspects, reads the log, sees a changed config key, writes the fix into
the service's config (envelope: snapshot first), restarts, verify green,
journal: what it did and why. The morning report says it in two lines.
Had it failed twice, `escalate` would have handed Claude the bundle and
the Bote would tell you "Claude schaut sich Immich an".

**A better tool.** The weekly Werkstatt ticket "Fotos: gibt es Besseres
als Immich?" gets a short report from Claude in the journal. If it
convinces, the next ticket builds the template, a PR, you merge, the
Bote asks whether to propose it. `provides: [photos]` makes the
duplication a HINWEIS on the card. Nothing installs itself.

## What is gone, in one list

ServiceBay's dashboard and 64 tools · Solaris the agent, its soul, skills,
sessions and chat · the speaker-identity gatekeeper · pi-web as a surface
· the Verlauf page (your Signal history is the Verlauf) · the model lease
inside Solaris · Nginx Proxy Manager · 28 JSON stores · a second agent
runtime for voice (Hermes serves text only, as stock).

## Where the decisions live

ADR 0001 (state repo, one verb, repair in two halves) · 0002 (Quadlet,
one binary) · 0006 (identities) · 0007 (Caddy) · 0011 (model role) · 0012
(approval surfaces, trust boundary) · 0013 (the agent is the operator) ·
0014 (Operator, Solaris, Haus are three programs) · 0015 (voice is
Assist; Solaris retired) · 0016 (the Bote; Hermes as stock).
