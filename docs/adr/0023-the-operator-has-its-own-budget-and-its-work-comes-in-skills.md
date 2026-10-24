# ADR 0023 — The Operator has its own line budget; its work comes in skills

Applies when you are about to give the Operator a new behaviour, a new
schedule or a new tool, pause something it does, wonder whether Operator
code counts against the kernel's 16k lines, or decide what the kernel must
check about an agent's task before it runs.

- **Status:** Accepted, 2026-10-16 (#267); amended 2026-10-17 (#286):
  `more:` tools for a capable model, `plan` and `apply` in the vocabulary;
  amended 2026-10-21 (#543): a look that finds nothing new asks no model,
  `routineblick` files repairs; amended 2026-10-21 (#544): a proposal is
  judged by what happened; amended 2026-10-22 (#550): a look's small
  context, one note per session, no session into a busy graphics card;
  amended 2026-10-22 (#550, #551): the log review reads by content, the
  model sees patterns;
  amended 2026-10-22 (#550): no session within two minutes of a person's
  model request;
  amended 2026-10-24 (#550): a look without news is silent, an incident
  waits for the graphics card
- **Deciders:** the owner (mdopp): "Löse den Operator heraus und gib ihm
  eigene Grenzen. Und evtl. werden solche Fähigkeiten auch ein Plugin.
  Welche, die man aktivieren, hinzufügen oder ruhen lassen kann." — and on
  the cap: "lass uns bei 16k bleiben".

## Context

The kernel has a hard cap of 16,000 production lines (`scripts/loc.sh`,
ADR 0005 as amended 2026-10-13); it stood at 15,935. `scripts/loc.sh`
counts every Go file under `cmd/` and `internal/`, so the Operator —
`cmd/laib-operator` and `internal/operator`, about 920 production lines —
is in the kernel's count although ADR 0014 §1 already made it a separate
program that reaches the kernel only over MCP. Every Operator feature
competes with kernel fixes for the last 65 lines.

What the Operator does today (the solution this ADR replaces, #246):

- `cmd/laib-operator/main.go` hard-wires three behaviours: a session on
  every tick (10 minutes, `box.yml operator.tick`), one at the report hour
  (`operator.reportAt`, the report replaces that tick), and one per red
  journal event (debounced ten minutes per service).
- `internal/operator` holds a prompt per trigger kind and, for the typed
  surface (#53), a menu per trigger kind; a menu entry appears only if the
  ops hand's `tools/list` has its verb, so the kernel's ceiling already
  narrows it (`ops: {ceiling: propose}` hides `restart`).
- Nothing can be paused short of `laib operator disable`; a new behaviour
  is Go code and a release; which tools a behaviour may use is a Go slice,
  and only the hand's ceiling is enforced by the kernel.

That works and is small. What it lacks is exactly the owner's three verbs:
activate, add, let rest.

## Decision

### 1. The Operator is its own program with its own budget

- Its code lives in its own tree, `operator/` (the package and
  `operator/cmd/laib-operator`), in this repo, this Go module and this
  release (release-please, one version). #268 moves it.
- **Budget: 3,000 production lines** (tests at most equal to production
  until 2026-10-18; the owner dropped that rule, ADR 0005 amendment #309),
  counted and capped by `scripts/loc.sh` separately from the kernel, which
  keeps its 16,000. The number: today ~920; the skill runner and the first
  new skill (#269, #270) add a few hundred; the known next steps — runbooks
  for the failure classes and schema-constrained decoding (#53, open) — fit
  in the rest. 3,000 is about three times today and a fifth of the kernel:
  small enough to read in an afternoon. An Operator that needs more is
  either doing kernel work (it belongs behind a verb) or hard-coding what
  should be a skill file.
- **The boundary is checked, not described:** nothing under `operator/`
  imports a kernel package, nothing in `cmd/` or `internal/` imports
  `operator/`. They meet over MCP and the environment of the unit the
  kernel renders. `scripts/loc.sh` fails on either import.
- One repo, not two: the MCP verbs, the skill vocabulary and the eval
  incidents change together, and a second repo would need version pinning
  between the kernel's MCP and the Operator — a compatibility layer ADR
  0010 rules out. Revisit if the Operator gets a second kernel to talk to.

### 2. What stays in the kernel and what moves

The kernel keeps everything the trust kernel (ADR 0019 §2) and the envelope
(ADR 0013) rest on — whatever the Operator's code or model does, these hold:

- the hands, their ceilings and verb lists (`policy.yml`), the MCP tool
  allowlist (`mcpVerbs`), `tools/list` and every `tools/call` checked per
  hand;
- the verbs the Operator calls — `inspect`, `read`, `look`, `exec`,
  `write`, `restart`, `escalate`, `note`, `propose`, `wish`
  (`cmd/laib/operator.go` and friends: kernel verbs every hand uses, not
  Operator code);
- the envelope: budget per service per hour, the pre-repair snapshot, fail
  closed; the journal; rollback; the one lock; the content review and the
  proposal classes;
- rendering the Operator's unit, `laib operator enable|disable`, the
  heartbeat in `/ready` and `diag`;
- **the skill registry and its rights check** (§4).

The Operator takes the rest: the agent loop and the model client, the
prompts, the typed menu and its validation, the scheduler, the event
webhook, running a skill, reading usage-metrics, the eval harness.

### 3. Skills: a declared unit of Operator work

A **skill** (on the pages: *Fähigkeit*) is one YAML file, `<name>.yml`; the
file name is the skill's name, a German noun (ADR 0021 rule 3: the admin
reads it on the Box page).

```yaml
description: Jeden Morgen ein Bericht — was nachts passiert ist und was ansteht, Rot zuerst.
trigger: {daily: "${REPORT_AT}"}     # or every: 10m · weekly: "Mo 08:00" · event: red
tools: [status, diag, journal, note]  # kernel verbs; note is required
context: box                          # box (laib context) | usage (usage-metrics)
output: report                        # note | report | propose
prompt: |
  Lies journal (24 Stunden) und status, fasse zusammen, was nachts passiert
  ist und was ansteht. Rot zuerst. Dann note mit kind report.
```

- **trigger**: exactly one of `every: <duration>`, `daily: "HH:MM"`,
  `weekly: "<Mo…So> HH:MM"`, `event: red` (a red journal line that is not
  the Operator's own, debounced ten minutes per service, as today).
  `${TICK}` and `${REPORT_AT}` stand for `box.yml operator.tick` and
  `operator.reportAt`, so today's settings keep working. A weekly or daily
  skill remembers its last run under `OPERATOR_HOME`, so a restart does not
  repeat it.
- **tools**: kernel verb names from the skill vocabulary — `status`,
  `diag`, `logs`, `inspect`, `look`, `read`, `journal`, `context`,
  `inbox`, `plan`, `note`, `escalate`, `propose`, `wish`, `restart`,
  `exec`, `write`, `apply` (the last two added 2026-10-17, see the
  amendment). `note` is required: every session ends in a note, which is what
  the heartbeat counts. The Operator offers a typed menu entry when its
  verb is granted (`set_value` → `propose`, `suggest` → `wish`); the menu
  stays Operator code, the vocabulary is the kernel's.
- **context**: what the session starts with. `box` is `laib context`, as
  today; `usage` is usage-metrics' `/summary` and `/misses`, read by the
  Operator's code with its own token — the model gets no tool to reach it.
- **output**: the note a session ends with if the model forgets one (`note`
  is `watch`, or `incident` for an event; `report` is the morning report),
  or `propose`: the result is proposals in the inbox.
- **prompt**: the session's task, German. The system prompt (rules, the
  owner's maxim #246) stays the Operator's, the same for every skill.

**Built-in skills** live in `skills/` in this repo and are embedded in the
`laib` binary, as `templates/` are: data, not code, so no budget lines.
**A skill is added** by a file `skills/<name>.yml` in the state repo; one
with a built-in's name replaces it. A skill is never code: no script, no Go
plugin, no hook — agent output never reaches host execution (ADR 0019
§2a), and a skill is something an agent may one day propose.

### 4. Rights: the kernel decides, never the prompt

- The kernel reads the registry (built-in plus state repo) on every MCP
  request and on `laib skill list`. A skill that names a tool outside the
  vocabulary (`approve`, `apply`, `remove`, `token`, `rollback`, …), has
  no `note`, or has a malformed trigger or output is **refused** with a
  German reason and never runs.
- A skill's **granted tools** are its tools that the ops hand's ceiling and
  verb list reach. The rest are listed as *über der Decke* and not granted;
  the skill runs without them, exactly as today's menu runs without
  `restart` at ceiling `propose`.
- The Operator sends `X-Laib-Skill: <name>` with every call of a session.
  The kernel then answers `tools/list` with the granted tools only, refuses
  every other call, and refuses every call made in the name of a paused or
  refused skill. The model cannot set a header; the Operator's code sets it
  from the skill it runs; and the ops token caps the Operator program
  itself, whatever it sends. The skill file narrows, the hand's ceiling
  caps; neither is a sentence in a prompt.
- A new or changed skill file in the state repo is a file outside the
  services, so the content review (#34) makes it class `human`: an agent
  can propose a skill, only a human lets it in.

### 5. Active or paused: `policy.yml`

```yaml
skills:
  routineblick: paused     # absent = active
```

`policy.yml` and not the Operator's own config, because it is where the
box decides what an agent may do; every change to it is already class
`human` in the review, so the Operator cannot un-pause itself by proposal;
and the kernel reads it for the rights check anyway, so a pause is
enforced, not requested. `laib skill list` shows every skill with its
state, its granted and refused tools; `laib skill enable|pause <name>`
(humans only) edits `policy.yml` and commits it. `laib_skill list` (read
tier) is how the Operator and, later, Haus's Box page see the registry.

### 6. Today's behaviours become built-in skills

| Skill | Trigger | Tools | Output | Today |
|---|---|---|---|---|
| `routineblick` | `every: ${TICK}` | status, diag, repair, note (#543) | note | the tick |
| `morgenbericht` | `daily: ${REPORT_AT}` | status, diag, journal, note | report | the report hour |
| `vorfall` | `event: red` | diag, logs, inspect, restart, propose, escalate, note | note | the red event |
| `nutzung` (new, #270) | `weekly: "Mo 08:00"` | wish, note | propose | — |

The prompts move as they are (the typed surface's wording, which also
serves the `cli` interface). Two differences are accepted: at the report
hour the tick runs as well instead of being replaced, and the trigger line
("Auslöser: …") is written by the runner, not per kind.

`nutzung` (#270) reads `/summary?days=7` and `/misses?days=7` and proposes
at most three improvements a week, each with what it saw (numbers), the
suggestion and which of maintenance or the family's comfort gets better.
A wish from the ops hand is a proposal, not an issue: it waits in the
inbox as class `human`, and approving it files the intake issue (#56). The
cap of three and the rule that a suggestion names a topic and a count but
never quotes an unanswered sentence (they are kept 30 days, without names,
for admins only, and an intake repo may be public) are code, not prompt.

### Weighed against today (#246)

- **Maintenance gets better:** the kernel's count drops by the Operator's
  lines and stays under its cap; a new behaviour is a file and no release;
  what a behaviour may touch is one line in one file, checked by the
  kernel, instead of a menu slice in Go.
- **The family's comfort is not worse:** the same three sessions run with
  the same prompts and menus; the admin can rest a noisy one (the
  ten-minute watch) without switching the Operator off, and the new weekly
  skill only proposes.
- **What it costs:** one more concept and one more file kind; a second
  narrowing (the header) on top of the ceiling; about 400 kernel lines for
  the registry, the switch and the wish proposal.

### Rejected

- *A second repo for the Operator* — version pinning between the MCP and
  the Operator (ADR 0010).
- *Skills as code* (Go plugins, scripts, hooks) — ADR 0019 §2a.
- *The switch in the Operator's own config* — the kernel could not enforce
  a pause it does not read, and it would be a second policy file.
- *Refusing a whole skill when one tool is above the ceiling* — it would
  switch the incident session off in the NUC trial (`ops: propose`), worse
  than today.
- *Skills naming the typed menu's tools* (`set_value`, `suggest`) — the
  kernel would have to know the Operator's menu; verbs are the kernel's
  words.

## Consequences

- #268 moves the Operator to `operator/` and gives `scripts/loc.sh` two
  budgets; #269 builds the registry, the switch and the runner with the
  three built-in skills; #270 adds `nutzung`.
- ADR 0014 §1 stands and gains the budget; ADR 0013's classes and ADR
  0019's grant schedule are unchanged; the skill rights check is part of
  the trust kernel.
- `docs/INTERFACE.md`'s typed-surface table becomes the skill table.
- Haus's Box page shows the skills later, from `laib_skill list`; the
  switch stays a human's `policy.yml` change (ADR 0012: no fourth approval
  surface).
- Still prose: whether the model follows a skill's prompt. That is the
  eval's job (`werkstatt/evals`), per skill.

## Amendment 2026-10-17 — a capable model gets the wide surface (#286)

The owner (mdopp), on #286: **"a capable model may run `apply` itself,
within the budget"** (ADR 0019, amended the same day). For skills:

- A skill may name **`more:`** tools besides `tools:` — the ones only a
  capable model gets. The kernel checks them exactly like `tools`
  (vocabulary, ceiling, verb list) and grants them; the Operator offers
  them only when it runs a capable model (`SmallModel`: `OPERATOR_INTERFACE`
  when set, else a window above 32k tokens). `vorfall` names
  `more: [look, read, journal, plan, apply]`.
- The vocabulary gains **`plan`** (read) and **`apply`**. `apply` from the
  ops hand is the kernel's narrow form: only the Operator's own open
  proposal, never one only a human may free, inside the envelope of every
  service it touches (ADR 0019 amendment). `approve`, `rollback`,
  `restore`, `remove`, `token`, `secret` stay outside it.

## Amendment 2026-10-21 — a look that finds nothing new is silent; a repair is a proposal (#543)

On the Kraftwerk the Operator wrote 131 notes in 72 hours, every one
repeating the same yellow findings, and not one proposal: `routineblick`
only described, `vorfall` runs only on a red, and the restore test diag
offered a repair for (`repair: [backup, check]`) was noted every ten
minutes for 35 days. `policy.yml` kept `sicherung` from `backup`, and only
`laib skill list` said so. What changes, all of it code, never prompt:

- **The runner looks first.** An interval skill granted `diag` starts with
  one `laib_diag` call of its own. When the findings are the ones its last
  session saw that ended without an error, the tick asks no model and
  writes no note (outcome `unchanged`, not in `incidents.jsonl`). A
  finding's identity is its severity, probe, service and detail with the
  numbers taken out, so an age or a percentage moving on is not news.
  Memory: `OPERATOR_HOME/findings.json`.
- **A repair verb becomes one proposal.** When such a skill is granted
  `repair`, the runner files `laib repair <probe> [service]` for every
  finding that is not red and carries a repair verb, before the model
  runs, and appends the outcome to the note. It files a finding once: not
  again while the proposal is open, nor after a no, until the finding
  changes or goes away. Red stays `vorfall`'s, which runs on the red
  event and has `repair` in `more:`. `routineblick` gains `repair`
  (tier `propose`): what it proposes still waits for a human (#441).
- **The model sees news.** In every skill session, the runner rewrites a
  `laib_diag` result: what the skill's last good session already saw is a
  count, red excepted, plus a line for what is gone. The owner's own items
  — the RAID mirror, the watchdog, the NAS — appear in one routine note and
  one morning report, not in every one.
- **The heartbeat no longer needs a note per tick.** The Operator's
  `/ready` carries `looked`, the time its last session or look ended
  without an error; the kernel's heartbeat (`diag/opstate.go`) counts it
  like a note. After a failed session the runner skips no look, so a model
  that stops answering is `stumm` after three ticks, as before.
- **diag names a skill the policy keeps from its job.** For each active
  skill whose own `tools:` the ops hand's ceiling or verb list refuses,
  `laib diag` has one yellow finding (probe `skill`) for the owner, whose
  choice the fix names: grant the verb in `policy.yml`, `laib skill pause
  <name>`, or a state-repo file without that tool. `more:` tools are a
  capable model's extras and never a finding.

Weighed against before (#246): maintenance gets easier — a finding diag
can fix reaches the Inbox once instead of the journal 144 times a day, and
a skill the policy starves says so in diag. The family is not worse off:
nothing is applied without a human, red is handled as before, and the
morning report still leads with red. What it costs: a model outage while
nothing changes is noticed at the next session — a change, an event or the
morning report — instead of within three ticks; whatever the family would
notice is a change and runs a session. ServiceBay's diagnose offered the
fix beside the probe and a human clicked it; the proposal is that click,
filed by the box.

## Amendment 2026-10-21 — a proposal is judged by what happened (#544)

The owner, on #544: the Operator should get better from solved incidents
and be anchored on what makes a proposal good or bad. Until now nothing
measured that: approved, rejected or rolled back, the Operator never heard
the outcome, and neither did the evals. What changes:

- **A proposal carries its anchor** (`proposal.Anchor`): the diag finding
  it answers (`<probe> <service>`), what was seen, how it is undone, and
  the outside sources it rests on. `laib propose --finding` is refused
  when diag does not show that finding now, so an anchor cannot be made
  up; `laib repair` anchors itself. A source makes the proposal class
  `human`. The runner refuses a model's `laib_propose` without finding,
  evidence and undo in every session that reads diag (the updates
  skill's patch versions answer an update, not a finding, and are exempt);
  without a finding diag shows, the model escalates.
- **The kernel records the outcome**, never the proposer: a whole-box
  `laib diag` settles each approved proposal — its finding gone is
  `effective`, still there two hours after the approval `no-effect`, one
  without a finding `applied` — and journals the measured ones once
  (verb `outcome`); `laib rollback #N` makes it `rolled-back`; a reject
  keeps the owner's short reason (`rejected`), and „nicht jetzt“ is
  `later`, free and counted against nobody. The Inbox page asks for the
  reason; „Später“ still decides nothing.
- **Every session hears its record.** The runner reads the inbox with the
  ops hand's own token and puts the last ten measured proposals before the
  context ("7 wirksam, 2 abgelehnt (#12 „…“), 1 zurückgenommen"); the
  morning report's note ends with the month's rate. Code, not prompt.
- **Real rejections become eval cases.** `werkstatt/evals/rejections.py`
  turns the Operator's anchored, rejected proposals (`laib inbox --all
  --json`) into draft incidents whose expectation is `calls_lack` — the
  same finding not proposed again, with the record in the replies. A
  human reads the reason and keeps or sharpens each draft.

- **A solved incident becomes a proposed lesson.** The built-in skill
  `lehre` (daily at the report hour, `lessons: vorfall`, no model) looks
  at the `vorfall` sessions whose proposal laib measured as effective and
  proposes, once per finding, `lessons/<probe>-<service>.md` in the state
  repo: the finding, the cause the session saw, what fixed it. It is a
  file outside the services, so the review makes it class `human` (§3):
  only the owner lets it in. `laib context` then carries every lesson
  into every session. A lesson whose finding the Operator has since
  proposed for twice without success (no effect, rolled back, rejected)
  is named in the morning report for removal. A lesson is text the model
  reads, never a tool or a rule the kernel enforces; a skill
  (`skills/<name>.yml`) stays the way to change what a session may do,
  and an agent may propose one as before.

Weighed against before (#246) and against ServiceBay, which had the assist
catalog as curated know-how and `propose_learning` (dropped without an
owner decision, #441): there, an agent's "learning" was its own claim.
Here the measure is the box — the finding went away or did not — and the
owner's no, in his words. Maintenance gets easier: the owner sees in one
line whether the Operator's proposals are worth reading, and a bad habit
shows up as a rate, not as a feeling. Nothing is worse for the family:
nothing is applied that was not before; a reject gains an optional text
field. What it costs: about 200 kernel lines (the anchor, the settle in
diag, the reason, the lessons in context) and one MCP read per session.

## Amendment 2026-10-22 — a look reads little, a session notes once, the graphics card comes first (#550)

The Kraftwerk's log on v0.11.1: 143 prompts over 5,000 tokens in a day,
4.0 million tokens together (about 28,000 a look), about 2.8 hours of
GPU just reading; every look evicted the Bote's prompt cache, so Joshua
often started cold; the same note five times within 14 s; seven red
sessions whose log line said only "red". #543 (v0.16.0) already made an
unchanged look ask no model and write no note. What changes, all of it
code in the runner:

- **A look's context is diag and the last journal lines, in its own
  budget.** For an interval skill granted `diag` the runner no longer
  hands the model the `laib context` bundle (every service and the day's
  journal, whose budget grew with the 131k window): it hands it what diag
  found, as news (the rewrite the #543 amendment gives a `laib_diag` result), and
  the journal's last 15 entries without the Operator's own notes. The
  look's context and each tool result have their own budget (9,000 and
  4,000 chars), whatever the window; with the Kraftwerk's day the first
  request falls from ~21,000 to ~800 tokens. The other skills keep the
  bundle and the window's budgets (#285): an incident needs the middle.
- **A session writes its note once.** The runner carried out every
  `laib_note` the model asked for, in one reply or over passes; a second
  one in a session is refused and the model is told its note stands. A
  session cut off after its note writes no second one.
- **A red session's log line says what was red and what came of it**:
  `session vorfall/jellyfin: red, 12 calls, 98000ms — jellyfin: apply:
  unit failed (abgebrochen nach 12 Aufrufen) → vorgeschlagen #12`. What
  was red is the event's detail or a look's red findings; why it ended
  red is the text call, the budget answer or the call limit; what came of
  it is the proposals by number, a restart, an escalation, or „nichts
  geändert“. The session record carries both (`red`, `why`).
- **No session into another's window or a cold start.** Before a session
  asks the model, the runner reads the lease proxy (`/api/model-lease`,
  `/v1/models` at `MODEL_URL`'s host): while the lease's mode is not
  `haushalt`, the window is still preparing, or the router reports the
  model not `loaded`, the session is `skipped` with its reason. Skipped
  is no error and writes no note; a look keeps its news for the next one,
  and a daily or weekly slot tries again at the next step rather than in
  a day or a week. A model without the lease proxy in front is never
  busy. Nor while a person asked the model less than two minutes ago
  (see the last amendment).
- **Updates and firmware wake no look.** `aktualisierung` was and stays
  weekly (Sa 10:00), but Home Assistant's status file names each new
  extension version and firmware as an info finding (`Update: …`,
  `Firmware: …`), and a new finding is news: an update woke a model
  session in the ten-minute tick. The look leaves those findings to the
  weekly skill, which proposes the extensions and reports the firmware
  (#528).

Weighed against before (#246): the family gets the graphics card back —
no Operator prompt between the Bote's turns while someone else holds the
card or the household model is cold, and a few looks a day instead of 140
— and the journal keeps one note per session. Maintenance gets easier: a
red session's line names its cause and result without opening
`incidents.jsonl`. What it costs: a look's model no longer sees the whole
day's journal and every service at once (it has `status` and `diag` to
ask), and an incident that comes while the card is lent is skipped; it
reaches the Operator again with its next red, ten minutes on, and the
morning report reads the journal. ServiceBay had no Operator of its own
on the GPU; there is no earlier solution to compare with.

## Amendment 2026-10-22 — the log review reads by content, the model sees patterns (#550, #551)

On the Kraftwerk a human read 48 hours of the journal (#551): about 36,000
lines of priority `err`, most of them a container's INFO on stderr
(podman's journald driver files stderr as `err` whatever it says:
Radicale 4,297, Whisper 2,937, llama 1,586, Paperless 1,246, Beets 829),
and among them a few real errors that came back every day — AdGuard's PTR
timeouts, Immich's thumbnails failing on the same 44 assets, Jellyfin's
broken files, Caddy's aborted responses. `-p err` is no alarm, and no
skill did this review. What changes:

- **A built-in skill `durchsicht`** (daily, 06:30; tools `status`, `logs`,
  `wish`, `note`; `limits: {wish: 3}`; output `note`). Daily, not weekly:
  a day's lines fit the line cap per unit, a setting that makes the family
  wait (a PTR timeout on every lookup) is seen the next morning, and the
  morning report, half an hour later, carries the note. Read verbs, a
  note and a wish — what `nutzung` has; a wish waits for the admin and
  then becomes an intake issue.
- **`context: logs`**: the runner reads, not the model. It lists the
  services (`laib status`) and reads each one's warnings and errors of 24
  hours (`laib logs <svc> --since 24h --lines 5000 --priority warning`;
  `--priority` is the kernel's one new flag, journalctl `-p`, a level and
  nothing else). A line's own level decides (`level=error`, `"level":
  "error"`, `[ERR]`, `ERROR (MainThread)`), else its words (error, failed,
  timeout, …Exception); `INFO`, `LOG`, `DEBUG` and warnings are noise,
  counted per unit, never shown as errors. Numbers, ids, paths, addresses
  and timestamps are taken out, so the 44 failed assets are one pattern
  with a count. The kernel requires `status` and `logs` for that context.
- **The model sees news only.** A pattern is shown when it is new (three
  times or more in the day), recurring (in the last review too, shown
  once) or growing (at least doubled since it was shown), at most eight,
  each with unit, count and an example line, plus one line counting the
  rest and the units' noise — about 1,000 tokens, no `laib context`.
  Nothing of that: no model, no note (outcome `unchanged`, as #543's
  look). Memory: `OPERATOR_HOME/durchsicht.json`, written only after a
  session that did not fail, so a model outage shows the same news again.

What it does not see: a real error a program writes to stdout (priority
`info`); the review reads `warning` and worse to keep a day inside the
cap. A unit that reaches 5,000 lines is reported as „mindestens“.

The fix at the source was weighed: podman cannot file a container's
stderr by content, and lowering an app's log level (Radicale's `[logging]
level`, uvicorn's access log, `PAPERLESS_LOGLEVEL`) takes away the INFO
lines a human reads in `laib logs` when something breaks. That stays a
per-template decision, not part of this change.

Weighed against before (#246) and ServiceBay, which had no log review:
maintenance gets easier — the review a human did by hand runs every
morning and costs the GPU nothing on a day without news; the family is
not worse off — nothing is applied, and a wish waits for the admin. What
it costs: about 230 Operator lines and 13 kernel lines (the flag, the
context check).

## Amendment 2026-10-22 — no session right after a person's request (#550)

#553 left one gap of point 5: "not within N seconds of a Bote, Haus or
Frage request", because nothing recorded that time. The lease proxy now
does (ADR 0011 amendment #550, `last_human_at`), and the Operator marks
its own model requests `X-Laib-Background: operator`, so they are not
counted. Before a session asks the model, the runner skips it while the
last person's request is younger than two minutes — `skipped` with its
reason, no error, retried at the next step, like the other skips. Two
minutes, a constant in `operator/llm.go`, covers the gap between a
person's turns; no box.yml knob. An incident session follows the same
rule as the other skips (whether it should wait in a queue instead is
the owner's open question). What it costs: a look or report on a busy
evening runs a few minutes later; ServiceBay had no Operator on the GPU
to compare with.

## Amendment 2026-10-24 — a look without news is silent, an incident waits (#550)

The Kraftwerk's journal after 0.18.0 still had looks noting „Alle 23
Dienste sind grün, keine neuen Befunde“: a finding had gone away, the set
differed from the last look's, and the model was asked to say nothing.
And an incident that came while the graphics card was lent was skipped.
The owner decided both (2026-10-24):

- **New means a finding the last look did not see.** A finding's identity
  stays the #543 one (severity, probe, service, detail without numbers).
  A look whose findings are all among the last look's asks no model and
  writes no note, also when some went away; the runner remembers the
  smaller set, so a finding that returns is news, and its repair can be
  filed again.
- **An incident waits in a queue.** When the lease, a cold model or a
  person's request makes an incident session skip, the runner writes the
  trigger to `OPERATOR_HOME/queue/`, one file per finding (skill, service,
  the detail without numbers); the same red again counts on that file.
  Each step, once the model is free, runs the queue oldest first, and the
  session's note says how long the incident waited and how often it came.
  At most 20 wait (the oldest gives way); one older than 24 hours is not
  run, since the next look sees what is still red. Each drop is a note.
  The folder survives a restart and the Operator's move into a container,
  which keeps `OPERATOR_HOME` mounted.

Weighed against before (#246): the journal loses the empty notes; an
incident on a gaming evening is handled when the card is back instead of
waiting for its next red, which a red that stays red only sends again
after the ten-minute debounce. What it costs: about 150 Operator lines.
ServiceBay had no Operator on the GPU to compare with.
