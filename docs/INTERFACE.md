# Laib — the interface

Laib has exactly one interface, the CLI. The MCP surface, the three pages and
the Solaris cards are views on it.

## CLI

```
laib status [service] [--watch]      one line per service; first line is the readiness state
laib logs <service> [--since 1h]     journald + container logs, secrets redacted
laib diag [service]                  findings with severity and a proposed diff each
laib plan [service]                  render state → diff against the box (files + units)
laib apply [service] [--proposal id] snapshot → write → reload → restart → verify
laib rollback <service|commit>       git revert + apply
laib propose "<reason>"              commit the working tree to a proposal branch, create an inbox entry
laib approve <id> | laib reject <id> merge or drop a proposal (tier check from policy.yml)
laib backup | laib restore <when>    restic over state + declared data paths
laib context [--since 24h]           starter bundle for an agent session
laib template list|lint|add <git>    registries and the contract check
laib adopt <unit> | laib ignore <unit>
laib token mint <hand> [--ttl 8h]    one token per hand, scopes from policy.yml
laib secret set <NAME> | list        value on stdin, never an argument; list prints names only
laib init                            first setup as a dialogue
laib serve                           daemon: /ready, /api, /mcp, the three pages
```

Every verb has `--json`. The MCP tool list is generated from the same verb
table, so the two cannot drift. The `ops` hand in Solaris sees eight verbs:
`status`, `logs`, `diag`, `plan`, `propose`, `context`, `template list`,
`ticket`. Never `apply`, never `approve`.

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
Authelia (`admins`). No JavaScript framework; a form post per button.

## Solaris cards

The admin sees the same Inbox as a card, plus two more:

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
