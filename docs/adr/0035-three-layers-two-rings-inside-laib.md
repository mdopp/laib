# ADR 0035 — Three layers by purpose, two rings of trust inside Laib

Applies when you add a verb or a package to `cmd/laib` or `internal/`,
when the kernel's line cap (`scripts/loc.sh`) is near, or when a part of
Laib talks to the internet, parses data it did not write, or only reads
and judges.

- **Status:** Accepted, 2026-11-15
- **Deciders:** the owner (mdopp), on „gehört das alles in den Kern? Oder
  sollte davon etwas in ein eigenes System … mit anderen
  Sicherheitsstufen? Etwas das eh nicht so funktioniert wie es soll und
  weg kann oder neu gedacht werden sollte?“, and on the review below:
  „Mach das“.

## Context

The kernel is at 22,462 of 22,500 production lines (#1047). The diet
(#1229) found no further cut *inside* the trust kernel, and it is right
about that. But the kernel still does work that needs no trust:

- **It talks to the internet.** `internal/registry/updates.go` and
  `notes.go` ask registries for digests and fetch changelogs: the most
  privileged process on the box, with the age key in reach, parses
  answers from outside. ADR 0025 already keeps the Operator, which reads
  the web, from acting; the kernel itself does the opposite.
- **It runs household verbs.** ADR 0026 put household verbs behind the
  door in `laib-haushalt`, yet `laib calendar`, `laib person` and
  `laib aufgabe` still live in `cmd/laib`.
- **It observes.** Much of `internal/diag` (devices, images, OS update,
  operator state, self-update) only reads and judges; it needs no secret.

## Decision

The box has **three layers by purpose**, and inside Laib **two rings by
trust**. A layer depends only on the layers below it (owner, 2026-11-15:
„0 ist Laib, 1 utils, 2 fachlicher Mehrwert“).

| Layer | What | Examples |
|---|---|---|
| **0 · Laib** | keeps the box alive, safe and reachable | **0a kernel**, **0b Laib's helpers**, the foundation services: Caddy, auth, AdGuard, the Werkstatt, the GPU lease |
| **1 · Building blocks** | standard programs that hold data or give a capability, no household logic of their own | Immich, Paperless, Home Assistant, Radicale, Vaultwarden, Jellyfin, Music Assistant, the share, Mosquitto, the model server, Stimme, the print server |
| **2 · Value** | what gives the family something through technology, built on the blocks | Homes, the archive, the Bote, Frage, documents/scan, calendar subscriptions, the poll |

**Layer 2 is several independent programs, not Homes.** Homes, the
archive's programs, the poll, the Bote and Frage stand beside each other,
each with its own repo, address and login. Homes is one of them: a small
core with modules over building blocks and optional plugins (homes ADR
0003). The archive splits into the vault, a building block, and programs
that work in an open vault (archiv ADR 0001). A program may register with
Homes as a plugin; it never has to (owner, 2026-11-15: „die Abstimmung hat
nichts mit Homes zu tun, es ist eine eigene App“).

### Inside layer 0: two rings by trust

1. **Ring 0a, the trust kernel (host, `laib`).** Only what decides about
   the owner's approval, keys and rights: the state repo, `secrets.age`,
   `policy.yml` with hands, tokens and the envelope, the inbox and its
   approval, `review` (what only a human may allow), apply with snapshot
   and rollback, the journal, the MCP door, and the pages where a human
   approves and enters keys. **The card text a human reads before
   approving stays here:** whoever writes it can mislead. Ring 0a never
   talks to the internet except to verify what it is about to run.
2. **Ring 0b, Laib's helpers (containers, ADR 0027).** Programs that read
   and propose through the door with their own token, no age key, no state
   repo: the Operator, Notify, Haushalt, Lease, and three moved out of 0a:
   - **Updater**: asks registries, fetches changelogs and signatures,
     proposes new digests (ADR 0034). The kernel re-verifies digest and
     signature at apply; it never trusts the updater's word (#1233).
   - **Haushalt** gets `calendar`, `person` and `aufgabe` (ADR 0026, #1234).
   - **Observer**: the read-only findings of `diag`; findings that need a
     secret stay in 0a and say why (#1235).

### Laib stands without the layers above

Laib is the foundation; Homes is the household's program on top of it
(owner, 2026-11-15: „Laib ist etwas, das ohne Homes funktionieren sollte …
Homes ist erst fachliches“).

- Everything an admin needs to keep the box running is on Laib's own pages
  and verbs: status, findings, inbox, keys, the admins' tasks with a way to
  do them there. Nothing operational exists only in Homes (#1236).
- Homes may show the box's state to the family in its own words; it reads
  it from Laib and is never the only place for it.

### Layer 2 has no tie to the OS

**Stronger than before** (owner, 2026-11-15: „Homes sollte keine
Abhängigkeit zum OS haben“): a layer-2 program reaches the box only
through interfaces, never through the host.

- It talks to Laib through the door (MCP/HTTP with its token) and to a
  building block through that block's API, nothing else.
- It mounts only its own data folder: no other service's folders, no host
  path, no device.
- No host network, no `SecurityLabelDisable`, no capability.
- No host-side `podman exec`/`podman run` units for it: periodic work runs
  inside its own container or is asked of Laib through the door.
- Moving files to a building block (uploads for Paperless, music for beets,
  backups for the archive) goes through that block's API or a door verb,
  not a shared folder.

A layer-2 program that needs something only the host can do asks Laib for
it as a verb; it never gets the host. Homes' current ties are listed in #1245.

### Repositories follow the layers

The laib repo holds layer 0 only. Layer-1 templates move to their own
template collection, layer-2 programs to their own repos (as Homes, the
archive, documents and the poll already are); each is a registry the box
lists, one card per new source (#1244).

### What is rethought, not moved

- **A job with a frame instead of budget and hour windows (ADR 0032).** An
  owner-directed task over private data („gleiche die Google-Personen mit
  Immich ab“) is one card: which hand, which services, what it may read,
  what and how much it may change, until when. Approved once, bounded by
  its frame, journaled; not an Einblick per service plus a raised box-wide
  budget.
- **One card per Laib release for template updates**, in the family's
  words, and no card and no restart for a change that does not change the
  rendered unit (a comment, a README).
- **The Operator's log wishes are collected** and come as one card a week.
- **`laib-app register`** proposes the registry entry as an ordinary
  patch; its own box.yml writer goes.

## Consequences

- Ring 0a shrinks by roughly 2,400 lines to about 20,000; the cap is
  lowered with each move, never raised for a move.
- The kernel stops parsing registry and changelog answers. A compromised
  registry or changelog can at worst make the updater propose something
  the kernel then refuses on digest or signature.
- Dependencies point down only: value needs blocks and Laib, blocks need
  Laib, Laib needs neither. A box with only layer 0 is complete and
  maintainable, and vm-install proves it.
- Each move is its own issue and PR, behaviour unchanged, vm-install green
  on main before the next.
- A household verb or an observation added later goes to ring 0b by
  default; ring 0a needs a reason in the PR. A new program is placed in a
  layer first; one that needs the host is layer 0 or asks layer 0.
