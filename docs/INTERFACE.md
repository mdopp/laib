# Laib — the interface

Laib has exactly one interface, the CLI. The MCP surface, the three pages and
the messages to the owner are views on it.

## CLI

```
laib status [service] [--watch] [--fresh]   one line per service; first line is the readiness state; --json adds `at`,
                                     when it was measured. Through MCP (laib_status) the answer is the kept readiness while
                                     it is under 30 s old and no journal line or apply lock came since (#593); a named
                                     service, --fresh and the CLI measure anew; /ready and the Status page always measure
laib journal [--since 24h] [--limit n] [--verb v] [--kind report|incident|watch]
                                     journal entries, oldest first; --verb and --kind (a note's kind) filter, --limit keeps
                                     the newest n, and without --since of any age (#593)
laib logs <service> [--since 1h] [--lines 200] [--priority err|warning] [--grep <re2>]   journald of every unit, the containers' output in it (podman log driver journald), secrets redacted; --priority is journalctl -p (this level and worse; a container's stderr is err); --grep reads the whole --since window and keeps the newest --lines lines that match (RE2 on the redacted line; all lower case = any case, #916)
laib diag [service]                  findings with severity and a proposed diff each
laib inspect <service>               units, containers, data files, health, last logs (class 1)
laib read <service> <path>           a file in the service's data paths, secrets redacted (class 1)
laib state [<path>]                  the state repo's files on main, or one file as committed there (box.yml, policy.yml,
                                     services/<s>/values.yml …), to write a --patch against the real text; secrets.age is
                                     never listed or shown, a secret's value and a literal under a declared secret's key
                                     read ${secret:NAME}, and the output names what was redacted (#576)
laib exec <service> [--container c] -- <cmd>   inside the container, output journaled (envelope)
laib write <service> <path>          content on stdin, in the data paths (envelope)
laib restart <service>               restart + verify (envelope)
laib start <service>                 start its units, lift a stop's pause, verify (envelope, #441)
laib stop <service> [--until 2h] ["<grund>"]   stop its units and pause the service, at most a day; converge leaves it alone (envelope, #441); off for good is laib remove
laib backups                         read: the last nightly backup (green/red, when, why), the last green one, the off-box copy, the restore test, with diag's backup findings and their fix (#441)
laib escalate <service> "<was ich weiß>"       context bundle + red journal line for the next hand; an issue when box.yml intake is set; from the bote hand never an issue but a Box-Aufgabe for the owner (#863)
laib wish [--von <login>] "<was das Haus braucht>"  the intake (#56): a GitHub issue with the wunsch label, never pi:ready; kept in the journal without an intake; laib-haushalt files it behind the door with its own GITHUB_INTAKE_TOKEN, as it files escalations (#797); from the bote hand a Box-Aufgabe for the admins, „Wunsch aus dem Boten: …“ with von bote:<login>, never a ticket (#883); from the ops hand a proposal (wishes/<time>.md, class human) whose approval files the issue (#270)
laib repair <probe> [service]                 a diag finding's repair (its `repair` field: restart <svc>, apply, backup, backup check, operator enable) as a proposal (repairs/<time>.md, class human); a human approves, then laib runs the verb as the human who approved; any other command in repairs/ is refused (#441)
laib note [--kind report|incident|watch] "<text>"   what a hand concluded; report = the morning report
laib aufgabe list [--alle] [--faellig] | zeigen <id> | add "<titel>" [--warum W] [--erinnern JJJJ-MM-TT]
            [--fertig-wenn "<probe> [<dienst|thema>]"] [--fuer admins|alle|person:<login>] [--weg /seite|gespraech]
            | erledigt <id> | zurueckstellen <id> <JJJJ-MM-TT>
                                     the Box-Aufgaben (#667): the owner's to-dos that are box topics, not code, in the state repo's
                                     aufgaben.yml (docs/STATE.md). Bookkeeping, no proposal: each change is one commit of that file
                                     on main and one journal line (verb aufgabe). ops, claude, Haus, humans and laib-haushalt (the Bote's wish, #883) add; humans, claude and
                                     Haus (for the logged-in person, --von <login>) check off and defer. A fertig_wenn (old name
                                     --befund) defers that diag finding until erinnern, and laib ticks the task off once diag
                                     measures it gone; list shows the open ones, --alle the done too, --faellig the due ones.
                                     --fuer and --weg (#878): who it is for and its guided way; Haus and the Bote see a
                                     person's own only for that person (--von)
laib zuhause show | set [--street S] [--postcode P] [--city C] [--country DE] [--latitude N --longitude E] [--elevation M]
                                     the household's address, box.yml zuhause (#561): show prints the block only; set replaces
                                     the whole block as a box.yml proposal of class human (the owner approves, never the caller);
                                     it is the value at the path box.yml:zuhause (#598); laib-haushalt serves it behind the door
                                     and proposes it as the haushalt hand (#677): the verb grant is what a caller needs
laib person signal <user> <+49…>    a person's Signal number in box.yml people (#596): sets the signal: channel, or changes
                                     the one there, as a box.yml proposal of class human; `laib person invite … --signal +49…`
                                     files the same one before it makes the account (a number the loader refuses stops both);
                                     it is the value at box.yml:people.<user>.signal (#598); laib-haushalt serves it behind the door
                                     (#677); invite --signal stays in the kernel: a hand with a verb list needs people.*.signal for it
laib scan [status] | start | weiter | abschliessen | verwerfen | redo --stapel <zeit> | suchen | pruefen | probe [--person p]
                                     the scan service (#568): status prints its status file; the rest is an order file in
                                     ${DATA_DIR}/scan/auftrag/ the service picks up: a stack from the SANE scanner, after a jam
                                     go on, finish with what is scanned or drop it, a stack split again from its archive;
                                     the setup's search, check and probe sheet (#571)
laib scan einrichten [--geraet <sane-gerät>]
                                     a proposal (#571): the scan template with that scanner, or SCANNER on the installed one
laib operator enable|disable|status  mint the ops token, render and start laib-operator's container (ADR 0027; humans only)
laib tell <person> "<text>"          one message to one person of box.yml people (#676, haus#123: a new podcast episode for its
                                     subscriber): bote-notify sends it on the person's first reachable channel. Answers
                                     {sent, message} in German whatever happened, exit 0 only when it went out; refused before
                                     sending: not a person's name, an empty text, more than 1000 characters. The journal names
                                     the person and the channel, never the text. Without a Bote on the box: sent false
                                     (laib scan, laib net geraete and laib tell are laib-haushalt's, in its container: the verb door hands
                                     each call over HTTP on 127.0.0.1:5902, ADR 0026, 0027)
laib updates [--run|--now]           what is behind: patch and minor releases of pinned images (#287), the registry digest of a tag or pin (#528); --run is laib-images.timer's nightly update, and first
                                     the built-in hands' new default verbs as one proposal (#575);
                                     --now (tier apply, #578) starts laib-images.service now without waiting — Laib's own update, then the images —
                                     refused while it runs, a backup runs, the lock is held or laib is not green; the laib row shows `running` meanwhile
laib net [--mermaid]                 every name (reach, Authelia, target), the direct ports, the host network, the pods' links: from the rendered config (#439)
laib net geraete [--wecken]          the LAN's devices as the host sees them (#577): address, MAC, reverse-DNS name, maker from the
                                     IEEE oui.txt laib-haushalt's image carries; only the box's /24, only complete entries of /proc/net/arp.
                                     `limits` says what the list cannot see (a sleeping device, another subnet). --wecken first sends
                                     one UDP datagram to port 9 of every address of the /24 so ARP fills the table; no port is read
laib skill list | enable <name> | pause <name>   the Operator's skills (ADR 0023); the switch is a human's, a commit to policy.yml
laib plan [service]                  render state → diff against the box (files + units)
laib apply [service] [--proposal id] snapshot → write → reload → restart → verify
laib rollback <service|commit>       git revert + apply
laib propose "<reason>"              human with a shell: commit the working tree to a proposal branch
laib propose --set services/x/values.yml:KEY=VALUE "<reason>"      agent: one value (repeatable)
laib propose --path <file>:<a.b.c>=<VALUE> "<reason>"  one value at a named path of box.yml or a values.yml (#598), as a line
                                     edit that rewrites only that top-level block and keeps its comments; VALUE is YAML (a scalar,
                                     or flow: {city: X}), empty removes it; a segment on a list names its "<segment>:" item
                                     (box.yml:people.mama.signal=+49…); a key box.yml does not know, or a state the loader refuses,
                                     is refused before any proposal; a hand with a verb list only at the paths policy.yml grants it
laib propose --add <template> [--name svc] [KEY=VALUE…] "<reason>"  agent: a new service from a template
laib propose --patch "<reason>"      agent: unified diff on stdin (MCP: the patch property), against the text laib state shows; validated in a worktree
laib propose --grant <hand> <verb> "<reason>"  one verb added to one hand's verb list in policy.yml, a line edit (#575);
                                     class human; an unknown verb or hand, a hand without a list, a verb it has: refused
laib propose … [--finding "<probe> <service>"] [--evidence …] [--undo …] [--source <url>]…
                                     the anchor (#544): the diag finding it answers (diag must show it now),
                                     what was seen, the way back; a --source makes it class human
laib approve <id> | laib reject <id> ["<grund>"]  merge or drop a proposal (tier check from policy.yml); an approve
                                     is the merge, then the run that applies it: here and now when the lock is free,
                                     else at once „freigegeben: wird mit dem nächsten Lauf angewendet“ (#609);
                                     a reject keeps the reason, "nicht jetzt" is free (#544)
laib backup                          restic snapshot of state, journal and declared data paths
laib restore <snapshot|latest> [svc] pre-restore snapshot, stop, restore paths, apply
laib context [--since 24h] [--full]  starter bundle for an agent session; by default the first look (#579): readiness, the services
                                     not green, the open proposals (`open`), the journal's newest 30 lines without the looks, each cut
                                     to a line (`omitted`: how many it left out); --full is the whole bundle
laib template list|lint|add <git>    registries and the contract check
laib ignore <unit>                   an unmanaged unit, deliberately left alone (ADR 0008)
laib ignore --log <service> <re2> <grund>   a log shape answered as harmless (#915): the log review counts it, with its reason, and raises it no more; committed to ignore.yml on main
laib token mint <hand> | list | revoke <id>   humans only
laib secret set <NAME> | list        value on stdin, never an argument; list prints names only
laib init                            first setup as a dialogue
laib serve                           daemon: /ready, /api, /mcp, the three pages
```

Every verb has `--json`. Every verb takes `--help` or `-h` anywhere before
`--`, on the CLI and over MCP alike: it prints the verb's usage, exits 0
and runs nothing (#590). The verbs that stop, write, restore or apply
(`apply`, `approve`, `reject`, `rollback`, `backup`, `restore`, `stop`,
`start`, `restart`, `remove`, `exec`, `write`, `converge`, `add`, `pause`,
`resume`, `secret`, `token`, `propose`) name their flags in the verb table;
a flag they do not take exits 1 before anything runs, never read as a
service, a reason or a commit. What follows `--` is the container
command's own. The MCP tool list is generated from the same verb
table (`mcpVerbs` names which of them an agent may reach at all), and
`scripts/drift.sh` in CI checks that every tool the Operator's prompt
names is one of them. The `ops` hand is the Operator (ADR 0013): it sees every verb
up to `apply`, including `inspect`, `read`, `exec`, `write`, `restart`,
`escalate` and `propose`. What it never sees: `approve` of its own
proposals, `token`, `secret`, `serve`, `init`. `remove` is above its
ceiling and a reboot is not a verb; a public route is a values change,
which the dial may auto-apply — the "always a human's call" in
`policy.yml`'s comment is a convention today, not enforced (#47).

## Typed MCP tools and the Operator's menu (#53, #290)

The tools agents use most publish typed parameters (`cmd/laib/mcpschema.go`),
and the kernel turns a call's JSON into the verb's argv without joining and
splitting a command line, so quotes and spaces survive:

| Tool | Parameters |
|---|---|
| `laib_status` | `service?`, `fresh?` (a boolean) → `{ready, services, at}`; `at` (RFC3339) is when the answer was measured, up to 30 s old (#593). With `service`, its row carries the status file's own fields (all but `at`, `detail`, `findings`) as written, under `eigenes`, at most 32 KiB; a field past the cap is left out and named in `eigenes_gekappt` (#688). Beets: `services[0].eigenes = {wartet, letzte: [{at, eingang, von, titel, alben, namen, ordner, nicht_importiert: [{datei, grund}]}], …}`, newest upload first |
| `laib_diag` | `service?` |
| `laib_logs` | `service`, `since?` (`60m`, `24h`) |
| `laib_inspect`, `laib_restart` | `service` |
| `laib_read`, `laib_write` | `service`, `path` (`write`: `stdin`) |
| `laib_state` | `path?` → without it the list of files on main; with it `{path, content, redacted}` (read tier: every hand without a verb list reaches it by its ceiling, #576) |
| `laib_look`, `laib_exec` | `service`, `container?`, `command` (a list) |
| `laib_escalate` | `service`, `summary` |
| `laib_note` | `kind` (`watch`, `incident`, `report`), `text` |
| `laib_journal` | `since?`, `limit?` (a count as a string: the newest n, without `since` of any age), `kind?` (`report`, `incident`, `watch`: notes of that kind), `verb?` (#593) |
| `laib_context` | `since?`, `full?` (a boolean: the whole bundle, #579) |
| `laib_inbox` | `id?` (one proposal in full, with its card), `all?` (a boolean), `limit?` (decided ones, 20; `0` all) (#579) |
| `laib_propose` | `reason`, and `service` + `key` + `value` (one values.yml change), `path` + `value` (a value at a named path, `box.yml:people.mama.signal`, as `--path`; #598), `hand` + `verb` (one verb for a hand's list, #575) or `patch`; the anchor `finding?` (`<probe> <service>` from `laib_diag`), `evidence?`, `undo?`, `sources?` (a list of URLs: class human) (#544) |
| `laib_wish` | `text`, `saw?`, `better?` (`wartung`, `familie`, `beides`), `von?` (the Bote: the login of who asked, #883) |
| `laib_repair` | `probe` (from `laib_diag`), `service?` → a proposal that runs the finding's repair once a human approves (#441) |
| `laib_skill` | `action` (`list`, `enable`, `pause`), `name?` |
| `laib_backup` | `action` (`check`: restic checks a sample and restores one path into a scratch dir to compare; `run`) |
| `laib_updates` | `now?` (a boolean, tier apply): start the nightly run now → `{started, unit, message}` (#578) |
| `laib_person_invite` | `user`, `first`, `last?`, `email`, `group?` (`family`, `admins`; empty: family, or on a resend the group the user has), `resend?` (a boolean), `signal?` (E.164, `+49…`: a `laib_person_signal` proposal filed first; a number the loader refuses stops the invite, #596) → `{user, mailed, link?, expires (RFC 3339), message, proposal?}` (#360; `proposal` with `signal`, the message names it) |
| `laib_person_signal` | `user` (the name in box.yml people, lowercase; someone not there yet is added with that channel), `number` (E.164, `+49…`) → `{proposal, message}`: people.`<user>`'s `signal:` channel set in place or added after their others, a class-human proposal whose reason carries no number; after a human approves it the Bote takes the number (#586's pre-start). Tier propose: the haus hand by its verbs and its path grant `people.*.signal` (the person page), the Bote by its ceiling (the owner's answer to a drop notice) (#596, #598) |
| `laib_person_pending` | none → `[{user, name, email, invited_at}]`: invited, link not used yet |
| `laib_calendar` | `action` (`subscribe`, `every`, `unsubscribe`, `list`), `person?`, `name?` (a-z 0-9 -), `kind?` (`google`, `abo`), `title?`, `every?` (`30m`, `12h`, `30d`: how often this calendar is fetched, #619; empty: Google every run, a public abo every 30 days), `stdin`: the ICS address (subscribe only; kept in secrets.age as KALENDER_ABOS, in no answer) → `{message, abos: [{person, name, kind, title, since, every?}]}`; the kalender template mirrors each into `<person>/google-<name>` or `<person>/abo-<name>` (#495) |
| `laib_zuhause` | `action` (`show`, `set`); for set `street?`, `postcode?`, `city?`, `country?` (two letters, `DE`), `latitude?` + `longitude?` (degrees as strings, `"52.1234"`, together or not at all), `elevation?` (whole metres) — the whole block, what is left out is removed. show → `{street?, postcode?, city?, country?, latitude?, longitude?, elevation?}` (numbers as JSON numbers; `{}` when unset); set → `{proposal, message}`, a class-human proposal whose reason carries no address; the address applies once a human approves it (#561). The haus hand calls it from the admins' form, by its verb and its path grant `zuhause` (#598); Laib never geocodes: coordinates come from the form (a map pin, or a lookup the person starts knowingly) |
| `laib_scan` | `action` (`status`, `start`, `weiter`, `abschliessen`, `verwerfen`, `redo`, `suchen`, `pruefen`, `probe`, `einrichten`), `geraet?` (einrichten: a SANE device; the answer is `{proposal, class, message}`), `person?` (whom the closing message is for; empty: `notify.person`), `stapel?` (redo: `JJJJ-MM-TTThh-mm-ss`) → status: the service's status file as it is (templates/scan/README.md); otherwise `{message}`. Tier propose: the haus hand by its verbs, the Bote by its ceiling (#568) |
| `laib_aufgabe` | `action` (`list`, `zeigen`, `add`, `erledigt`, `zurueckstellen`); add: `titel`, `warum?`, `erinnern?` (`JJJJ-MM-TT`), `fertig_wenn?` (`<probe> [<service or topic>]` from `laib_diag`, e.g. `raid`, `watchdog`, `backup offsite`; `befund?` is its old name), `fuer?` (`admins`, the default; `alle`; `person:<login>`), `weg?` (a Haus path such as `/stimme`, or `gespraech`); zeigen, erledigt: `id`; zurueckstellen: `id`, `erinnern`; `von?` (Haus and the Bote: the login of the person they act for; Haus's changes are recorded as `haus:<login>`); list: `alle?`, `faellig?` (booleans). list → `{offen, faellig, aufgaben: [task]}`; the others → `{message, aufgabe: task}`; task: see "Box-Aufgaben for Haus" below. Tier propose; add for ops, claude, haus, humans and haushalt (the Bote's wish, `von` the caller it names, #883), erledigt and zurueckstellen for humans, claude and haus, list and zeigen for every hand with the verb; Haus needs `aufgabe` in its verbs (#667) |
| `laib_session` | `args`: the session's name → `{token, id, hand, expires}`: only for a hand with `session:` in policy.yml (the Werkstatt, #380) |
| `laib_apply` | `proposal` — for the ops hand only its own open proposal, inside the envelope (#286, ADR 0019 amendment) |

`service` is an enum of the installed services, `key` a values.yml key
(capitals, no path), `value` one line. An unknown parameter, a wrong type,
a value outside an enum or a missing required one is answered `ungültige
Argumente … — nicht ausgeführt` and the verb never runs. Every tool still
takes `args` (one command line) instead, for clients that send it.

The Operator's menu is these tools as the kernel lists them for the
running skill (ADR 0023): no second menu, no translation in the Operator.
The interface follows the model (#286): `OPERATOR_INTERFACE` when set
(`typed` = small, `cli` = capable), else the window — up to 32k tokens a
small model with the short menus and compact budgets, above it a capable
one that also gets each skill's `more:` tools, and whose plain answer is
never mistaken for a call written as text. Above a 32k window the budgets grow with the window —
about a fifth of it for the context bundle, a fifteenth per tool result
(#285); the window comes from llama-server's `/props?model=` (through the
lease proxy) or `box.yml operator.contextWindow`
(`OPERATOR_CONTEXT_WINDOW`). A reasoning model's `<think>` block is
dropped from its reply. The built-in skills' menus:

| Skill | Trigger | Menu |
|---|---|---|
| `routineblick` | every `operator.tick` | `laib_status`, `laib_diag`, `laib_repair`, `laib_note` — the runner reads diag first: unchanged since the last good look, no model and no note; a finding that is not red and carries a repair verb becomes one `laib repair` proposal, not again until it changes or goes away (#543); when it runs, its context is what diag found, the journal's last 15 entries without the Operator's notes and the owner's lessons for the findings it sees (#861), in its own budget (9,000 chars, each result 4,000) whatever the window; Home Assistant's `Update: …`/`Firmware: …` findings are `aktualisierung`'s and wake no look (#550) |
| `vorfall` | a red journal line, or a red a look sees for the first time (#792) | `laib_diag`, `laib_logs`, `laib_inspect`, `laib_restart`, `laib_propose`, `laib_repair`, `laib_escalate`, `laib_note`; a capable model also `laib_look`, `laib_read`, `laib_journal`, `laib_plan`, `laib_apply` — for a look's red the runner first reads each service's last log line, runs the `restart` diag offers when granted, looks again and notes in German what it saw and did, with the fix of a lesson for its finding (its script, #839, #861); still red, the model works it and may wake an unloaded model; an incident that waits 30 minutes (card lent, model failing) is `laib escalate`d once with what was seen and keeps waiting (#792); a session that ends red is escalated by the runner, once, unless the model did (#858) |
| `morgenbericht` | daily at `operator.reportAt` | `laib_status`, `laib_diag`, `laib_journal`, `laib_note` — when the model fails, the runner writes the report its script made from status and journal (#858); it ends with the model's use in the last 24 hours (sessions, tokens in and out, minutes, #859) and, in a month's first report, the lessons the sessions were offered and the findings that came back (#861) |
| `nutzung` | weekly, Monday 08:00 | `laib_wish`, `laib_note` |
| `aktualisierung` | weekly, Saturday 10:00 | `laib_updates`, `laib_propose`, `laib_note`, `laib_inbox`, `laib_diag` — a newer patch release per pinned image becomes a proposal; minor releases only in the note (#287); the runner itself proposes a data service's newer image by digest and the value a status file offers (Home Assistant's `HA_INSTALL`), each once while open; firmware only in the note (#528) |
| `lehre` | daily at `operator.reportAt` | `laib_propose`, `laib_note` — for each `vorfall` session whose proposal laib measured as effective, its script proposes `lessons/<probe>-<service>.md`, class human, once per finding (#544); the model condenses it in one short question without tools — cause, sign, fix, when not to apply it — and without an answer the transcription (finding, the cause the session saw, what fixed it) stands, marked (#861) |
| `durchsicht` | daily, 06:30 | `laib_status`, `laib_logs`, `laib_state`, `laib_wish`, `laib_note` — the log review (#550, #551): the runner reads every service's warnings and errors of 24 hours itself (`laib logs --since 24h --lines 5000 --priority warning`), a line's own level decides whether it is an error, else its words, never the journal's priority; a line the container runtime wrote (`pasta`, `conmon`) and a shape in `ignore.yml`'s `logs` (`laib ignore --log`) are only counted, never a pattern (#915); lines become patterns per unit with numbers, ids, paths and timestamps taken out, compared with the last review (`OPERATOR_HOME/durchsicht.json`); the model sees only patterns new (three times or more), recurring (in the last review too) or growing (doubled), at most eight, each with count and an example line, no `laib context` — about 1,000 tokens; nothing of that asks no model and writes no note; a failed session is not remembered; at most three wishes |
| `sicherung` | weekly, Sunday 05:00 | `laib_backup`, `laib_journal`, `laib_note` — no model (#858): its script runs `laib backup check` (the canary restore), reads the week's backups and writes the note, an incident when the check is red or no backup was green for a day; the morning report reads it (#287) |

Every session starts with its record (#544): the runner reads `laib inbox
--all --limit 0` as the ops hand and puts the last ten measured proposals before the
context — how many were effective, without effect, rolled back, rejected,
with the owner's reasons; the morning report's note ends with the month's
rate and names each lesson whose finding has since failed twice, for
removal. `laib context` carries the lessons the owner let in (`lessons`,
at most 4000 bytes). In a session that reads diag, a model's `laib_propose` without
`finding`, `evidence` and `undo` is not filed.

Reds within a minute (`OPERATOR_EVENT_GATHER`, default 60s) are one
incident session (#287): a cascade — lldap, then Authelia, then Caddy —
reaches `vorfall` once, with every red in its detail, and the prompt asks
for the one cause instead of a restart per symptom.

`nutzung` (#270) starts from usage-metrics instead of `laib context`:
`/summary?days=7` and `/misses?days=7` with `USAGE_METRICS_TOKEN` (the
Operator's unit carries URL and token when the box runs usage-metrics).
A wish from the ops hand is a proposal the admin approves; Operator code
refuses a fourth wish in a session, one whose `saw` has no number, and one
that quotes an unanswered sentence word for word (they are kept 30 days
without names, for admins only; the intake repo may be public). Without
usage-metrics the skill writes one note and asks no model.

**The web, read, never acted on** (#544, ADR 0025). A session whose output
is proposals, that may `propose` and that has none of `apply`, `restart`,
`exec`, `write` granted (today `aktualisierung`) is offered one more tool,
`websuche` (`frage`, `dienst?`): Operator code, not a kernel verb. It asks
Frage's `/search` (`FRAGE_URL`, rendered into the Operator's unit when the
box runs Frage — SearXNG behind it, no model, no page fetched): first the
service's upstream (`laib context` → `upstream`, from its laib.yml or its
template), then the general entry points in `operator/websources.json`
(Home Assistant, podman, systemd, Fedora CoreOS, uCore), the open web only
when they give fewer than three hits. The answer is excerpts with their
address, marked as untrusted text. At most four requests and 64 KiB per
session; the note says how many and which addresses. A proposal filed
after a search carries `sources` — the addresses the model names that the
search returned, else all of them — so laib makes it class human.

A tool is in the menu only if the skill is granted its verb (`ops` at
ceiling `propose` gets no `restart`). `approve`, `reject`, `restore`,
`rollback`, `converge`, `template`, `apply`, `remove` are in no
skill's vocabulary.

### Skills (ADR 0023)

`laib skill list` (MCP `laib_skill`, read tier) lists every skill — the
built-ins in `skills/` of this repo, embedded in `laib`, and the state
repo's `skills/<name>/skill.yml` (#839) — with its status (`aktiv`, `ruht`, `verweigert`),
its granted tools and, per tool it does not get, why. The Operator sends
`X-Laib-Skill: <name>` with every call of a session: `tools/list` then
answers the skill's granted tools only, and a call outside them — or any
call of a paused, refused or unknown skill — is answered `nicht
ausgeführt: …` and never runs. A skill is granted a verb when it names it,
the verb is in the skill vocabulary (`status diag logs inspect look read
journal context inbox note escalate propose wish restart exec write`) and
the hand's ceiling and verb list reach it. `laib skill enable|pause
<name>` (humans only) writes `policy.yml skills` and commits.

## Box-Aufgaben for Haus (#878, haus#188)

Haus builds Mehr → „Einrichtung“, /aufgaben and the quiet line on Start
from `laib_aufgabe`; the Box page keeps the admins' tasks.

A task, as `list` and `zeigen` return it:

```json
{
  "id": 7,
  "titel": "Stimme aufnehmen",
  "warum": "damit Joshua dich an der Stimme erkennt",
  "seit": "2026-10-28",
  "von": "haus:mdopp",
  "fuer": "person:anna",
  "weg": {"seite": "/stimme"},
  "erinnern": "2026-11-15",
  "fertig_wenn": "status stimme",
  "erledigt": {"am": "2026-10-30", "von": "laib", "wie": "Befund weg"}
}
```

- `fuer` is always there: `admins` (every task from before #878), `alle`,
  or `person:<login>` (Authelia's `Remote-User`).
- `weg` is missing, `{"seite": "<path>"}` (a Haus page that leads through
  it), or `{"gespraech": true}` (Haus offers „Gemeinsam mit Joshua“ and
  opens the Bote with „Aufgabe #7 mit dir erledigen“, below).
- `fertig_wenn` is missing or the finding that closes the task by itself;
  `erledigt` is missing while it is open. `erledigt.von` is `laib` when
  diag closed it (`wie`: „Befund weg“), `haus:<login>` when Haus ticked
  it off for someone.
- `befund`, the key before #878, is no longer in the answer; `befund` is
  still taken as an argument.

**Who sees what.** Haus calls with `von`: the login of the person who is
looking. The kernel answers with the tasks for `admins`, for `alle` and
for `person:<von>`; another person's task is not in `list`, and `zeigen`,
`erledigt` and `zurueckstellen` answer „Box-Aufgabe #N gibt es nicht“ for
it. Without `von`, Haus gets the admins' and everyone's only. `offen` and
`faellig` count what the caller sees. The kernel does not know Authelia's
groups: Haus shows the `admins` ones to admins (the Box page), the
`alle` and `person:` ones under Einrichtung and /aufgaben. The Bote's hand
follows the same rule; a human, claude, ops and laib see every task.

- Einrichtung: `list` with `von`, keep `fuer` `alle` or `person:<von>`.
- /aufgaben: the same with `alle: true`, open and done.
- Start: Einrichtung's list; laib_status's `aufgaben` and `aufgabenFaellig`
  count only the tasks that are nobody's own.

**The guided conversation.** For `{"gespraech": true}` Haus sends the Bote
(Hermes' API server, as the Sprechen field does, with the person's system
line) the message „Aufgabe #7 mit dir erledigen“. The Bote reads the task
with `laib_aufgabe` `zeigen` (`id`, `von`: the person), leads step by step,
checks each step with laib's read tools (`laib_diag`, `laib_status`), and
proposes what changes the box; it never ticks the task off: `fertig_wenn`
or the person does (templates/bote/README.md).

## Readiness: `/ready`

`GET http://127.0.0.1:5900/ready` answers one JSON line:

```json
{"state":"bereit","services":12,"green":12,"yellow":0,"red":0,"lastHeard":"2026-10-08T10:42:07Z","lock":null}
```

`state` is one of `bereit`, `eingeschränkt`, `nicht erreichbar` (the last one
is what the *client* renders when the call fails). `lock` names what holds
apply when `eingeschränkt`: `apply:media` or `gpu:werkstatt`.

The guarantees are the same object in `/ready` and in `laib status --json`
`.ready` (#856, one verdict for every surface):

- `red` counts red services; each red row of `.services` carries `red`, its
  reasons beyond the units (`"health: 502 Bad Gateway"`).
- `problems` lists what is red beyond one service: disk, backup, firewall,
  and the last whole `laib diag` run's slow probes with its time.
- `lastBackup`: `at` (the last green backup), `repos` (places: 2 while the
  off-box copy is under 48 h old), `ok` (green, and no backup finding red),
  `onlyOnDataDisk` (the one place is the disk it protects), `problem` (why
  not `ok`), `text` (the fact in one neutral German sentence).
- `lastChange`: the last approved proposal (`proposal`, `summary`, `at`).

## The box page: three pages

Status:

```
Laib · bereit · 12 Dienste · zuletzt gehört 10:42:07

  grün   auth, caddy, adguard, media, immich, vaultwarden, ...
  gelb   paperless     Zertifikat läuft in 6 Tagen ab   [diag]
  rot    keine

Letzte Änderung   09:15  claude   media: Jellyfin auf 10.11   #c81f2a
Offene Vorschläge 1                                           [Inbox]
```

Inbox:

```
Vorschlag #17   von ops (der Operator)   Stufe: apply   braucht: Admin

  Paperless für die Familie, nur intern
  + services/paperless/values.yml
  + caddy: paperless.<domain> → :8000, intern
  + authelia: Client paperless, Gruppe family

  Snapshot vor Apply: ja     Rollback: laib rollback paperless

  [Freigeben]                                         [Ablehnen]
```

Every card has the same one button row (#941): „Freigeben“ left, „Ablehnen“
right. „Ablehnen“ takes a second tap: it opens „Warum nicht? (optional)“
with „Ablehnen bestätigen“ and „Abbrechen“ in place. The open cards stand
oldest first; „Zuletzt entschieden“ (decisions older than 15 minutes) is
always at the bottom, so nothing moves when a card is decided.

After „Freigeben“ the card keeps its place and turns, without buttons, into
what the box does with it — the step the run that carries
it writes on the proposal (`step`, #686): „wartet, bis die Box mit der
Arbeit davor fertig ist“, „sichert immich“, „startet immich“, „prüft, ob
immich läuft“; then „fertig“ (with „Zurücknehmen“ where it undoes something, #852) or „zurückgenommen, weil
immich danach nicht richtig lief“. While something runs the page fetches
the decided cards every 3 s and swaps each into its place; it never reloads
as a whole. „Alle freigeben“ (two or more open cards) is one confirmation
page that names the cards; it approves exactly those, one after the other,
each with its own run, so a red one is taken back alone.

„Schlüssel hinterlegen“ (`/schluessel`, #696, linked from Status) is the
owner's `laib secret set` without a console, and the one place the keys
live. It offers only the secrets a template marks `owner:` (docs/STATE.md),
never one the box generates or a hook mints: per service, each key by its
German label, what it brings, „fehlt“ or „hinterlegt“ (never the value) and
the env name in small print, with „Hinterlegen“ or „Ersetzen“ to its own
form.

**One key's form, the deep link:**
`https://<laib host>.<domain>/schluessel?name=<NAME>[&zurueck=<url>]`
opens exactly that key: label, what it brings, where to get it (the
template's how-to and link), whether it is there, and the password field.
Other places that need a key link here instead of explaining
`laib secret set`: a diag finding `schluessel` names a missing key whose use
Laib can see (a set value `requires` it, or the DNS-01 token) with this
link (`render.KeyLink`); an app links it from its own settings (Haus'
Discogs settings, haus#148: `name=HAUS_DISCOGS_KEY` and `HAUS_DISCOGS_SECRET`,
`zurueck=https://haus.<domain>/discogs`). `zurueck` is offered as „Zurück“,
also after saving, only when it is `https://` on the box's domain or a
name under it; anything else (another site, `http://`, `//host`) is
dropped, so the link is no open redirect. A name the page may not set
answers „Diesen Schlüssel kann die Box-Seite nicht setzen.“

Saving stores the value like `laib secret rotate` does (under the apply
lock, committed on main, journaled by name) and applies every service that
uses it; the key's page says „<label> hinterlegt, <Dienst> neu gestartet“
or why not, reloading while it runs. The value is never shown, journaled or
logged. It is behind the same guard as Freigeben, refuses a cross-site
form, and no token reaches it (`secret` is not an MCP verb).

Journal: one line per entry, filter by service and actor. Nothing else.

The pages are server-rendered HTML from `laib serve`, behind Caddy and
Authelia (`admins`). Caddy adds `X-Laib-Proxy: <secret>`; without it the pages
answer 403, so a process on the box cannot forge `Remote-User` (ADR 0012).
No JavaScript framework; a form post per button.

## Messages to the owner

laib-notify writes the owner (`box.yml notify.person`) through the Bote, by
mail when no channel answers (#441): an incident red for ten minutes, the
image updates, and each new proposal as „Freigabe #N: <headline> · <Inbox
link>“. The decision happens in the Inbox, behind Authelia's second factor
(ADR 0012, #778); no chat approves.

An escalation is the context bundle under `/mnt/data/laib/escalations` and
a red journal line. The Operator's also becomes a `needs-claude` ticket in
`box.yml intake.repo`; the Bote's becomes a Box-Aufgabe for the owner and
never a ticket (#863). Haus reads the readiness state from `/ready`.

## MCP

`laib serve` exposes `/mcp` (streamable HTTP, JSON-RPC 2.0, stdlib). Auth is
a bearer token minted by `laib token mint <hand>`; tokens are hashed in
`/mnt/data/laib/tokens.json`, never in the state repo. `tools/list` returns
only the verbs the hand's ceiling allows, named `laib_<verb>`, with typed
parameters (above) or, for the rest, one `args` string exactly as on the
command line. `serve`, `init`, `token` and
`secret` are never tools. A call above the ceiling answers exit 3 with the
hint to propose. Claude Code: `claude mcp add --transport http laib
http://127.0.0.1:5900/mcp --header "Authorization: Bearer <token>"`.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | done, verify green where applicable |
| 1 | usage or validation error |
| 2 | apply done but verify red (state is applied, journal says so) |
| 3 | refused by policy (tier), proposal created if applicable |
| 4 | locked (`eingeschränkt`) |

### `laib disk`

`laib disk status` shows the data RAID (`/proc/mdstat` parsed, the recorded
member count). `laib disk add /dev/sdX` (humans only) wipes the device,
partitions it and joins it as the mirror of `/dev/md/data`; the sync runs in
the background and `laib diag` shows it. The stick creates the array with one
member on purpose (#29). The work is the program `laib-disk` beside `laib`
(`disk/`, out of the kernel since #443; `laib disk` hands it the actor and
`--json`), which also gives `laib diag` the RAID, SMART and temperature
findings (`laib-disk probe [--hardware]`, JSON).

### `laib inbox`

Open proposals in full, with their cards, as a table or `--json`; also an
MCP tool (read tier), so the Bote and Haus can show the inbox. `--all` adds
the decided ones newest first, without card, `reasons`, `warnings` and
`facts`, at most `--limit` of them (20; `0` for all, #579);
`laib inbox <id>` is one proposal in full, decided or not. A proposal
of class `human` lists why only a human may allow it (the content review,
docs/DESIGN.md). Deciding stays `laib approve` / `laib reject`; an agent's
approve of a human-class proposal is refused with the same reasons.

### Routes with `bypass:`

A route may list path prefixes that skip Authelia on its host, for a
token-only API next to pages: Haus serves `/napi/` to the phone's device
token and `/box` behind Authelia on `haus.<domain>`. Caddy proxies the
bypass paths straight through; Authelia gets a bypass rule per prefix.

### `laib remove <service>`

Humans only. Stops the service's units and resets their failed state (a
unit without its file is never listed as failed, #551; an apply that deletes
a unit file does the same), takes `services/<name>` out of the
state (a commit: `laib rollback` brings the service back), removes its
Quadlet and rendered files, applies (Caddy, Authelia, DNS lose the routes).
Data paths stay, and Laib prints where; deleting data is a separate, human
act. A role holder (proxy, sso, identity, dns, model) is refused until
`box.yml roles` points elsewhere.

### Tokens a template needs

A contract secret with `mint: <hand>` is a Laib token the service itself
uses (Haus as `ops`, the Bote as `bote`): `laib add` mints it on first
install and stores it in `secrets.age`, so there is no manual `token mint`
plus `secret set`. Human tokens are never minted this way: the admin's own
verdicts stay the admin's (`laib token mint human`).

### Operator heartbeat

The readiness line (`laib status`, `/ready`, the Status page, Haus) carries
the Operator's state: `aus` (not enabled), `startet` (enabled, no note yet),
`läuft`, `stumm` (running but no work done for three ticks:
model unreachable or a tick hangs), `antwortet nicht`, `fehlt` (enabled but
the unit is down). `laib diag` turns the last four into findings. A look
that found nothing new writes no note (#543); the Operator's own `/ready`
(`{"looked": …}`) says when its last session or look did work — ended
neither in an error nor skipped nor as a fallback (#858) — and that is the
heartbeat; the newest ops note counts only while `/ready` says none, right
after a restart. A daily or weekly slot that waits or fails writes one
line with its reason (#858); that line is no heartbeat. After a failed session no look is
skipped, so a model that stops answering still turns the state `stumm`. An
enabled operator that does nothing is never silent.

What a skill session's `laib_diag` shows the model (#543): findings its
last good session already saw come as one line with their count, red
excepted, plus what is no longer there — the owner's own items (the RAID
mirror, the watchdog, the NAS) are in one note and one morning report, not
in every one. The runner keeps this under `OPERATOR_HOME/findings.json`; a
finding's identity is its severity, probe, service and detail without its
numbers (an age or a percentage is not a new finding).

`laib diag` says when an active skill cannot do its job because the ops
hand's ceiling or verb list refuses one of its `tools:` (probe `skill`,
#543): "Fähigkeit sicherung läuft ohne backup, ihre Aufgabe bleibt liegen
…", with the owner's choice as the fix — grant the verb in `policy.yml`,
`laib skill pause <name>`, or a state-repo `skills/<name>/skill.yml` without it.
A capable model's `more:` tools are extras and never a finding.

### Hands with a verb list

`policy.yml` may limit a hand to a few verbs (`notify: {ceiling: propose,
verbs: [diag, updates, inbox, note]}`). Over MCP the tool list and every
call honour the verb list. Only the hand `human` counts as a human: the
Inbox (`human:<Remote-User>`) and the host shell decide what only a human
may (#778). The `approver` hand of the Signal relay (#48) is gone; an old
policy.yml that still has it, with `human: true`, loads, and the mark
counts for nothing — the owner removes the hand with a policy proposal.

A hand may name another in `session:` (#380, ADR 0006 amendment). It may
then get a token of that hand per session with `laib_session <name>`: the
TTL is the named hand's (a session hand must have one), the mint is
journaled (verb `session`), `laib token list` shows it as `session
<caller>: <name>`, and expired ones are dropped from `tokens.json` on the
next mint. `laib init` ships `werkstatt: {ceiling: read, verbs: [session],
session: claude}`; the Werkstatt holds that token, never a claude one.

The built-in hands' policy is one constant, `state.DefaultPolicy` (#575):
`laib init` writes it, and `laib updates --run` — laib-images.service,
right after `self-update.sh` installed a new release, nightly or through a
manual update — compares the box's `policy.yml` with it. Verbs a built-in
hand's one-line list lacks become **one** proposal adding exactly those
(class human: policy.yml). It never removes or narrows: a hand without a
list may already use every verb, a removed built-in stays removed, a hand
the owner added is not compared. No second one while one is open, none
again for the verbs the owner rejected; a list the owner wrote across lines
stays his (`laib propose --grant`).

### The decision card (#58)

Every open proposal is shown as the same card — on the Box page, in
`laib inbox`, in the Bote's chat („zeig #17“) and in `laib inbox --json`
(`card`): **what changes** in one sentence („Paperless wird installiert —
unter paperless.<domain> nur im Heimnetz erreichbar“), **what it means for
the family** (the review's reasons: aus dem Internet erreichbar, ohne
Anmeldung, ein Laib-Schlüssel, ein Skript auf der Box …, or „nichts
Sichtbares“), **whether it can be undone**, computed by
`proposal.Undoing` from the tier, whether it changes files or only runs a
verb, and whether a snapshot precedes it (#852): „Zurücknehmen“ brings the
files back and the data stay; a new service is stopped and removed, its data
stay; a service with data gets its program back with one tap, its data only
from the snapshot laib takes first; a repair, a wish and the destroy tier have
no „Zurücknehmen“ and say what stays. **Who proposed it and why** (quoted),
and **the safe default**: for a risky class (human, a new service, a minted
token, a service with data, the destroy tier) „Freigeben“ loses its accent
and a risk line stands above the button row (#941; the card's `primary`
still names „Später“ for the apps that show cards), and the diff sits behind „Details“. The card is built mechanically from
the classified change (`proposal.Card`), never by a model. What the
proposer saw (`evidence`) follows its reason, and its way back (`undo`)
follows laib's own.

**What came of it** (#544) is recorded by laib, never by the proposer,
on the inbox entry (`outcome`) and, when measured, as a journal line with
verb `outcome`: a whole-box `laib diag` settles every approved proposal
that names a `finding` — the finding gone is `effective`, still there two
hours after the approval (`proposal.Window`) is `no-effect`; a proposal
without one is `applied`; `waiting` is approved and not yet applied
(#609), never settled. `laib rollback #N` or a red run makes it `rolled-back`, and
`laib reject <id> "<grund>"` `rejected` with the owner's reason
(`rejectReason`) — „nicht jetzt“ or „später“ is `later`, a free no that
counts against nobody. The Inbox page asks for the reason, optional,
after the first tap on „Ablehnen“ (#941); not deciding means leaving the card. The Operator
reads this record (ADR 0023, amendment #544).

### `laib look <service> [--container c] -- <command>`

A read-only command (cat, ls, grep, ps, df, stat, find, …) in one of the
service's own containers: no budget, no snapshot, journaled. `laib exec`
is for changing something and stays budgeted. Neither reaches another
service's container. `policy.yml envelope.snapshot: optional` lets a
class-2 action go ahead without a pre-repair snapshot; the default refuses.

### One lock, pauses, pending activation (#39)

Everything that mutates the box — `apply`, `converge`, `restore`, `remove`,
`rollback`, the run that applies approvals — holds the one lock (`<home>/apply.lock`, a flock the
kernel releases when the process dies, with holder, pid and start inside)
for its whole duration. A `converge` tick that finds the lock taken is
skipped and journaled `übersprungen: gesperrt durch restore:human:core`; a
lock whose process is gone is taken over with a journal line.

**Approving is a merge, applying is a run (#609).** An approve never waits
for that lock: it runs its checks (the state loads, the own-proposal rule,
the human class, the second factor of a web decision), merges under the
state repo's short lock (`<state>.lock`, a flock that also serialises a
rollback's revert and apply's commits) and marks the proposal `outcome:
waiting`. The run (`inbox.Carry`) takes the one lock, applies `main` as it
is with every waiting proposal at once — one restart per service, its
snapshots (#528) first, fail closed — and records on each proposal and in
the journal which run carried it (`batch`: `#4, #5, #6 (<from>..<to>)`, the
state's commit range). Every release of the lock starts the run again, so
three approvals during one long apply give exactly one more apply with all
three; `laib serve` starts it after a restart, and a converge tick or a
whole-box `laib apply` that finds approvals waiting hands over to it. A run
that is not green (verify red, a snapshot failed) is rolled back as a
whole: each of its merges is reverted, newest first, each proposal is
`rolled-back` with the run named („#5 zurückgenommen, mit dem ganzen Lauf
#4, #5, #6 (…)“), and one apply follows. A proposal of that run is proposed
again to be approved alone.
A red run that touched a service with data (its pre-update snapshot taken,
the apply red) restores nothing by itself (#853, ADR 0019 §4 as amended):
the old program comes back, the data stay as the update left them, and each
proposal of the run says so with the snapshot's time („Das alte Programm
läuft wieder. Die Daten sind auf dem Stand nach dem Update; die Sicherung
von 14:05 liegt bereit.“). Laib files two proposals of its own, named on
the run's proposals as `restore` and `forward` and offered on their cards
as one tap each: „Sicherung zurückspielen“ (a repair running `laib restore
<snapshot> <service>` for each snapshot of the run) and „Nach vorne
reparieren“ (a repair running `laib rollback <revert>` for each revert of
a proposal with data: its change on again, the data untouched, no new
snapshot). A forward that verifies red leaves its red journal line, which
the Operator opens as an incident and escalates after 30 minutes as for
#792; the restore stays open beside it, its snapshot kept a week.
`laib pause <service> [--until 2h] [--stop <unit>]… [--restart <unit>]… ["<grund>"]` and `laib resume <service>`
(apply tier, also over MCP): a paused service is left alone by converge and shows as
`pausiert von … bis …` in `/ready` (`paused`), status and the box page;
`restore` pauses the service it rewrites. `--stop` and `--restart` act on the
service's own units after the marker is written and the marker keeps them;
`resume` lifts it, starts what it stopped and restarts what it restarted. A
renewal by the same hand moves the end and touches no unit. That is how the
GPU lease, in its container (ADR 0027), as the `lease` hand, pauses every
service whose units a window takes the card from (`llama`, `stimme`) while a
mode stands. Apply records the services whose activation it owes
(`<home>/apply.pending`, `/ready` `pending`) before it writes files and
clears it after verify; the next apply or converge restarts and verifies
them even when the file diff is clean. Restart-only converge runs are
journaled green with the units and count against the budget of three per
hour.

### Rollback by proposal, stale proposals, the manifest (#41)

`laib rollback #17` takes a freed proposal back: the approve merge is
reverted (`-m 1`) without touching `secrets.age` — services that stay keep
the secrets their data was made with, services that leave the state lose
theirs — and one apply follows. The Box page offers it as „Zurücknehmen“
next to the last five freed proposals it undoes something for (#852: not a
repair or the destroy tier); `/ready`'s `lastChange.reversible` is the same
answer. `laib rollback <commit>` validates
the argument (`git rev-parse --verify --end-of-options`) and reverts a
merge commit with `-m 1`. An approve merges in a temporary worktree: a
conflict never reaches `main`, the merged result must load and render, and
a proposal that fails either way is `stale` with a reason („veraltet — neu
vorschlagen“). A structured proposal (`--grant`, `--set`, `--path`, a
template update) keeps its intent beside its diff (#637): when its branch
no longer merges, approve applies the intent to the current `main` again
and reviews the result. It is applied („neu berechnet auf dem aktuellen
Stand“) only when class, files, tokens, services, reasons, warnings and
the card's changes are what the owner approved, and a template update only
from the very template files the owner saw; otherwise nothing is applied,
it goes stale („geändert seit deiner Freigabe — neu vorgeschlagen als #N“)
and the new content is filed fresh. When the intent itself conflicts — a
verb the hand has by now, a value someone changed since the proposal — it
is stale with that reason; a merge that changes nothing is stale, never an
approval. Apply records every file it put on the box in
`<home>/managed.json`; plan deletes — and for a unit stops and disables —
whatever the manifest has but render no longer produces, so a removed
service, a disabled operator or a removed llama does not keep running.
`laib operator disable` also revokes the ops tokens.

### Roles are verified, the Caddyfile validated, dependants protect a service (#50)

A change to a role's output (the Caddyfile, Authelia's rules, AdGuard's
rewrites) restarts the role's service and verifies it like any changed
service; the rendered Caddyfile runs through `caddy validate` in the
proxy's own image before anything is written, and a Caddyfile that does
not validate refuses the whole apply. `laib remove <service>` refuses while
another service names it in `after:` („… braucht … — erst dort
entfernen“), before anything is stopped. The box's own proposals (the
template-sync timer, actor `laib`) are approved by the box when the dial
says auto; the same change proposed twice reuses the open proposal.

### The shared file-share tree is never relabelled (#230)

`laib template lint` refuses a unit that mounts `${DATA_DIR}/file-share/data`
or a path under it (also through a values entry) with `:z`/`:Z` or without
`SecurityLabelDisable=true`. `:z` makes podman relabel the whole
multi-writer tree on every start; the rootless `lsetxattr` fails on any
entry `core` does not own (a root-owned file from a disk import), and the
container exits 125 in a crash loop (ServiceBay #1731, #1923). The
service's own data folders keep `:z`; with labelling off it is a no-op.

### Templates on CoreOS: ports, health, needs, images (#46)

The state refuses two services publishing the same host port, and
`laib template lint` refuses a template that publishes a port a sibling
template already uses (whoami and the bote pod both had 8080; the bote's
signal-cli is on 8085 now). Every template declares health: an `http:`
probe or `none: "<why>"` (MQTT speaks no HTTP). The probe is green on 2xx/3xx only; an app whose probe
path challenges declares `expect: [401]`, and then only those codes count (#288). A template whose
containers talk to laib's MCP declares `needs: [mcp]`; the state then
insists on `box.yml laib.listen: 0.0.0.0` (ADR 0012 §4) instead of a
container that silently cannot reach laib. Apply pulls the images of new
or re-imaged containers before the units start, so the first start never
includes the pull. Services with a database (immich, paperless,
vaultwarden, Home Assistant, auth, radicale, audiobookshelf, media, beets)
no longer carry `AutoUpdate=registry` — a rollback restores an image, not
migrated data; their images change by proposal, snapshotted before the
approval applies (#528). The mark is what `laib-images.timer` keys on: it
runs `laib updates --run` nightly, 90 minutes after `backup.schedule`
(box.yml `updates` narrows the days and the time), pulls the floating
images of marked containers, restarts and verifies a service whose image
moved and tags the previous image back on red. `podman-auto-update.timer`
stays masked (ADR 0002 amendment).
### Hooks: `pre-start` and `post-deploy`

Both run in the hooks image (ADR 0004 amendment), never on the host:
`pre-start` before the units start, `post-deploy` after a green verify.
Protocol lines on stdout: `laib-secret: NAME=value` (into secrets.age; a
service that declares NAME is re-applied in the same apply when its output
changed, #349) and
`laib-exec: <container> <command…>` (run by Laib in the service's own
container). A `laib-exec` line that carries a secret is refused: ps shows
every argv on the box to every user (#214), so a secret travels on stdin,
in a file only its reader can read, or over the service's own socket.
`box.yml laib.hooksImage` names the image.
A hook is part of its service's plan: render writes each hook's hash to
`hooks.laib` next to the units, so a changed hook alone re-runs the hooks
and restarts the service (#496). A file the program reads only when it
starts (llama's `presets.ini`) is written in `pre-start`, never in
`post-deploy`, which runs after the restart (#512).

### The Operator and the MCP surface (#43)

`diag` is an MCP tool. A verb writes to the writer in its `cliEnv`; MCP
collects a verb's output in a buffer (nothing another goroutine prints
can land in a reply, no pipe can block) and `escalate` builds its bundle
the same way. The Operator's system prompt is generated from the tool
list the hand has; a reply that writes a tool call as text is asked once
for a real call and then ends the session red with a note; a tool call
whose arguments are not a JSON object is refused, never run; the context
bundle is budgeted (head and newest journal lines). A session writes its
note once: a second `laib_note` is refused (#550). Before a session asks
the model, the runner reads the lease proxy: while the lease's mode is
not `haushalt`, the window is preparing or the router reports the model
not loaded, or a person asked the model less than two minutes ago (the
proxy's `last_human_at`: every forwarded request without
`X-Laib-Background`, which the Operator sets on its own), the session is
`skipped` with its reason (no error, no note;
a daily or weekly slot tries again at the next step). A red or incident
session's log line names what was red, why it ended red and what came of
it (`… — <was rot war> (<warum>) → vorgeschlagen #N`). `laib operator
enable` points `notify.url` at the operator's event endpoint unless the
owner set one, so reds wake it; an operator enabled for three ticks
without a single note is red („stumm“).

### Hardware health (#59)

`laib diag` asks `smartctl -H -A --json` about every member of the data
array and the OS disk: reallocated or pending sectors, media errors and
wear are yellow („die Platte altert“), a failed self-assessment is red
(„Platte bald tauschen“), 55/65 °C are warm/hot; the kernel's thermal
zones give the box's temperature (80 °C yellow, 90 °C red). Without
smartmontools the probe says so once (`sudo rpm-ostree install
smartmontools`; install-box.sh will layer it with git, restic and diff).
The power-loss proofs (a cut during an apply, a backup, a resync) wait
for Actions minutes and for #81.
