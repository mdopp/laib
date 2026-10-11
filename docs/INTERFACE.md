# Laib — the interface

Laib has exactly one interface, the CLI. The MCP surface, the three pages and
the Solaris cards are views on it.

## CLI

```
laib status [service] [--watch]      one line per service; first line is the readiness state
laib logs <service> [--since 1h]     journald + container logs, secrets redacted
laib diag [service]                  findings with severity and a proposed diff each
laib inspect <service>               units, containers, data files, health, last logs (class 1)
laib read <service> <path>           a file in the service's data paths, secrets redacted (class 1)
laib exec <service> [--container c] -- <cmd>   inside the container, output journaled (envelope)
laib write <service> <path>          content on stdin, in the data paths (envelope)
laib restart <service>               restart + verify (envelope)
laib escalate <service> "<was ich weiß>"       context bundle + red journal line for the next hand
laib note [--kind report|incident|watch] "<text>"   what a hand concluded; report = the morning report
laib operator enable|disable|status  mint the ops token, render and start laib-operator (humans only)
laib plan [service]                  render state → diff against the box (files + units)
laib apply [service] [--proposal id] snapshot → write → reload → restart → verify
laib rollback <service|commit>       git revert + apply
laib propose "<reason>"              human with a shell: commit the working tree to a proposal branch
laib propose --set services/x/values.yml:KEY=VALUE "<reason>"      agent: one value (repeatable)
laib propose --add <template> [--name svc] [KEY=VALUE…] "<reason>"  agent: a new service from a template
laib propose --patch "<reason>"      agent: unified diff on stdin (MCP: the patch property); validated in a worktree
laib approve <id> | laib reject <id> merge or drop a proposal (tier check from policy.yml)
laib backup                          restic snapshot of state, journal and declared data paths
laib restore <snapshot|latest> [svc] pre-restore snapshot, stop, restore paths, apply
laib context [--since 24h]           starter bundle for an agent session
laib template list|lint|add <git>    registries and the contract check
laib adopt <unit> | laib ignore <unit>
laib token mint <hand> | list | revoke <id>   humans only
laib secret set <NAME> | list        value on stdin, never an argument; list prints names only
laib init                            first setup as a dialogue
laib serve                           daemon: /ready, /api, /mcp, the three pages
```

Every verb has `--json`. The MCP tool list is generated from the same verb
table, so the two cannot drift. The `ops` hand in Solaris is the operator (ADR 0013): it sees every verb
up to `apply`, including `inspect`, `read`, `exec`, `write`, `restart`,
`escalate` and `propose`. What it never sees: `approve` of its own
proposals, `token`, `secret`, `serve`, `init`; and class-3 actions (remove,
reboot, a public route) are not verbs at all for it, they are proposals.

## Readiness: `/ready`

`GET http://127.0.0.1:5900/ready` answers one JSON line:

```json
{"state":"bereit","services":12,"green":12,"yellow":0,"red":0,"lastHeard":"2026-10-08T10:42:07Z","lock":null}
```

`state` is one of `bereit`, `eingeschränkt`, `nicht erreichbar` (the last one
is what the *client* renders when the call fails). `lock` names what holds
apply when `eingeschränkt`: `apply:media` or `gpu:werkstatt`.

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
Vorschlag #17   von ops (Solaris)   Stufe: apply   braucht: Admin

  Paperless für die Familie, nur intern
  + services/paperless/values.yml
  + caddy: paperless.<domain> → :8000, intern
  + authelia: Client paperless, Gruppe family

  Snapshot vor Apply: ja     Rollback: laib rollback paperless

  [Freigeben]   [Ablehnen]   [In der Werkstatt öffnen]
```

Journal: one line per entry, filter by service and actor. Nothing else.

The pages are server-rendered HTML from `laib serve`, behind Caddy and
Authelia (`admins`). Caddy adds `X-Laib-Proxy: <secret>`; without it the pages
answer 403, so a process on the box cannot forge `Remote-User` (ADR 0012).
No JavaScript framework; a form post per button.

## Solaris cards

The admin sees the Inbox as a card that links to `laib.<domain>/inbox`; the
decision happens there, behind Authelia (ADR 0012). Plus two more cards:

- **Ops report** (morning): "Alles grün, ein Zertifikat in 6 Tagen fällig, ich
  habe Vorschlag #18 vorbereitet."
- **Escalation**: "Das verstehe ich nicht. Claude dazuholen? Das Kontextpaket
  liegt bereit." Accepting creates a ticket labelled `needs-claude` with the
  output of `laib context` attached.

Solaris renders the readiness state in its chat header from `/ready` and says
once when the state changes.

## MCP

`laib serve` exposes `/mcp` (streamable HTTP, JSON-RPC 2.0, stdlib). Auth is
a bearer token minted by `laib token mint <hand>`; tokens are hashed in
`/mnt/data/laib/tokens.json`, never in the state repo. `tools/list` returns
only the verbs the hand's ceiling allows, named `laib_<verb>`, each taking one
`args` string exactly as on the command line. `serve`, `init`, `token` and
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
member on purpose (#29).

### `laib inbox`

Open proposals (`--all` for decided ones), as a table or `--json`; also an
MCP tool (read tier), so the Bote and Haus can show the inbox. A proposal
of class `human` lists why only a human may allow it (the content review,
docs/DESIGN.md). Deciding stays `laib approve` / `laib reject`; an agent's
approve of a human-class proposal is refused with the same reasons.

### Routes with `bypass:`

A route may list path prefixes that skip Authelia on its host, for a
token-only API next to pages: Haus serves `/napi/` to the phone's device
token and `/box` behind Authelia on `haus.<domain>`. Caddy proxies the
bypass paths straight through; Authelia gets a bypass rule per prefix.

### `laib remove <service>`

Humans only. Stops the service's units, takes `services/<name>` out of the
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
`läuft`, `stumm` (running but no note for three ticks: model unreachable or a
tick hangs), `antwortet nicht`, `fehlt` (enabled but the unit is down). `laib
diag` turns the last four into findings. An enabled operator that does
nothing is never silent.

### Hands with a verb list, and the approver

`policy.yml` may limit a hand to a few verbs (`verbs: [approve, reject,
inbox, status, note]`) and mark it `human: true`, so its verdicts count as
a human's. `laib init` ships the `approver` hand with exactly that: the
Signal relay's token (#48) — it can decide and nothing else, and it acts
only on the admin's verified number. Over MCP the tool list and every call
honour the verb list.

### The decision card (#58)

Every open proposal is shown as the same card — on the Box page, in
`laib inbox`, in the Bote's chat („zeig #17“) and in `laib inbox --json`
(`card`): **what changes** in one sentence („Paperless wird installiert —
unter paperless.<domain> nur im Heimnetz erreichbar“), **what it means for
the family** (the review's reasons: aus dem Internet erreichbar, ohne
Anmeldung, ein Laib-Schlüssel, ein Skript auf der Box …, or „nichts
Sichtbares“), **whether it can be undone** („Zurücknehmen“: files back,
data stays; a new service is stopped and removed, its data stays), **who
proposed it and why** (quoted), and **the safe default**: for a risky
class (human, a new service, a minted token) the first button is „Später“
and the diff sits behind „Details“. The card is built mechanically from
the classified change (`proposal.Card`), never by a model.
