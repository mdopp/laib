# ADR 0005 — Golden renderers, one booting integration job, verify inside apply; no gates until recurrence

Applies when you add a test, a CI job, a lint rule, a ratchet, or wonder what
"done" means.

- **Status:** Accepted, 2026-10-08

## Context

ServiceBay had more test lines than production lines, 19 invariant checks,
three ratchets, Playwright specs that never ran, and a real-box verify that
two agents died in the middle of. Its most common failure stayed "success
reported, nothing done". Tests proved the loader; proof was on the box.

## Decision

See `docs/TESTING.md`. In short: golden files for every renderer; contract
tests for every template; one CI job that boots and applies; verify is part of
`laib apply` and a red verify is exit 2. Test lines at most equal production
lines. No ratchets or invariant scanners at the start; a gate is added the
second time a shape breaks.

## Consequences

- The kernel's size is the architecture gate. Under 15k lines the structure is
  visible without a scanner.
- The real box is a canary, not a gate; an agent can never strand it mid-verify
  because the verify is inside the script.

## Amendment 2026-10-13 (#118): the cap is 16k

The owner raised the cap by 1,000 lines to 16,000 so the Joshua easter egg
(#119) fits into Laib beside the Bote. It is a raise, not a removal: the
slimming pass (#112) stays open, and the next feature still has to fit.

## Amendment 2026-10-17 (#309, proposed): structure the kernel, then an honest cap

**Status: proposed.** The owner reviews this plan before code moves.

The owner, on #309: "not only a diet — structure the code cleanly, then set
an honest cap." The kernel stood at 15,936 of 16,000. The cap is still the
architecture gate, but it only works if what it counts is the kernel. Today it
counts six programs that are not the kernel. It also counts one 6,000-line
`package main` that mixes the trust kernel with box chores and with logic
that belongs to individual templates.

### What is under `cmd/` and `internal/` today (production lines, 2026-10-17)

**Trust kernel** (ADR 0019 §2, ADR 0023 §2: hands, ceilings, verbs, envelope,
journal, rollback, lock, content review, render/plan/apply, the MCP surface):

| Package | Lines | Does |
|---|---:|---|
| `internal/state` | 1,233 | loads and validates the state repo, contracts, personas and people as render input |
| `internal/proposal` | 862 | proposals: branch + inbox entry, the card |
| `internal/apply` | 773 | snapshot, write, reload, restart, verify; the lock and pause files |
| `internal/review` | 641 | content review: class from content (§2b) |
| `internal/render` | 605 | state → files, pure |
| `internal/plan` | 385 | diff of rendered state against the box |
| `internal/skill` | 192 | skill registry and rights check |
| `internal/policy`, `token`, `secrets`, `journal`, `boxfs`, `sysd` | 636 | tiers per hand, hand tokens, secrets.age, the journal, paths, the systemctl seam |
| `internal/relay` + `cmd/relay` | 692 | the approval path that sees the sender (§2e); its own binary, but trust kernel |
| `cmd/laib`: `main`, `mcp`, `mcpschema`, `skill`, `operator`, `proposals`, `logs` (rollback), `token`, `secret`, `remove`, `pause`, `converge`, `notify`, `note`, most of `verbs` | ~3,630 | the verb table, the MCP surface, the envelope verbs, lock/apply/propose/judge, rollback |

**Box chores**: kernel, but not trust kernel. These are the guarantees and
gates of ADR 0019 §3–8, and none of them grants or decides anything.
`backup` (397), `recovery` (94), `watchdog` + family card (132), `diag`
(219), `hardware` (172), `disk` (313), `firewall` + `self rollback` (93),
`serve` pages and `/ready` (285), `opstate` (84), `registry` (116), `updates`
(175) and `wish` (163) come to **~2,240 lines**, all in `cmd/laib`. The
pre-repair snapshot is §2c, so backup counts as trust kernel even though its
file sits here.

**Template-specific logic in the kernel** (~175 lines): `ha.go` (84, Home
Assistant's integration checklist), `bote.go` plus its `serve` route (~70,
the Signal link QR) and `wakeword.go` plus the `stimme` lines in `verbs`
and `diag` (~50). A template should bring these itself.

**Programs that are not the kernel** (3,870 lines, 24 % of the count):

| Program | Lines | Runs |
|---|---:|---|
| `cmd/laib-stick` + `internal/stick` | 1,468 | on the laptop; builds the install stick |
| `cmd/laib-lease` + `internal/lease` | 908 | the llama template's sidecar: policy proxy and GPU lease (ADR 0011) |
| `cmd/persona` + `internal/persona` | 701 | the Bote's persona engine (ADR 0020), an image in the bote pod; the reason for the 16k raise above |
| `cmd/automat` + `internal/automat` | 421 | the Bote's HA-automation MCP tool, an image |
| `cmd/frage` + `internal/frage` | 261 | the "was ist …" tool (ADR 0015), an image |
| `internal/mcpserver` | 111 | the small MCP server that only persona, automat and frage use (the kernel has its own) |

Each of these is already its own binary with its own Containerfile or
release asset. None of them is part of the trust kernel. They sit in the
kernel's tree and count because `cmd/` and `internal/` were the only place
for Go code before `operator/` existed.

### Target layout

1. **Programs move out, as the Operator did (#268, ADR 0023 §1).** Each gets
   its own top-level tree with its package and `cmd/`: `stick/`, `lease/`,
   `persona/`, `automat/`, `frage/` (the last three already hold the
   Containerfiles), and `mcpserver/` as their shared library. Each program
   gets its own budget line in `scripts/loc.sh`: today's size plus about a
   quarter, rounded (stick 1,800 · lease 1,200 · persona 900 · automat 600 ·
   frage 400 · mcpserver 200). Each budget keeps the rule that tests are at
   most equal to production. `relay` stays in the kernel because it is
   invariant §2e.
2. **`cmd/laib` becomes thin.** It keeps `main`, the verb table, `cliEnv` and
   argument glue, about 400 lines. The verbs move into packages with one
   responsibility each:
   - trust kernel: `internal/mcp` (tools/list per hand, typed schemas,
     tools/call, skill gate), `internal/envelope` (the class-2 verbs, the
     budget, the jail), `internal/lock` (the one lock, §2d),
     `internal/inbox` (propose, judge, approve, reject, auto-approve,
     rollback by proposal) and `internal/backup` (snapshot, restore, the
     monthly check, the recovery sheet; §2c rests on it);
   - box chores: `internal/diag` (probes, RAID, SMART, thermal, the
     Operator's heartbeat, firewall state), `internal/disk` (`laib disk`),
     `internal/pages` (serve's pages, `/ready`, watchdog, family card),
     `internal/registry` (template registries, template and image updates)
     and `internal/intake` (wish → GitHub).
3. **Template logic goes to the template.** A template may ship a `check`
   hook. It runs in the hooks image like every hook (§2a) and prints
   findings as JSON lines, and diag shows them. Home Assistant's checklist
   and Stimme's wake-word state become their templates' checks. The Bote's
   link QR becomes a route the bote template declares (admins only) instead
   of a page in `serve`. The kernel keeps about 50 lines for the runner.
4. **Boundaries are checked, not described**, by `scripts/loc.sh` and with
   the same grep as the Operator's. No program imports `internal/` or
   `cmd/`, and the kernel imports no program. Programs may import
   `mcpserver/`. Inside the kernel, no trust-kernel package imports a
   box-chore package, and only `cmd/laib` wires the two together.

Two couplings need more than a move:

- **lease** reads the state repo, writes the kernel's pause markers and
  appends to the journal directly. The proposal: the kernel renders the
  role → service → units map the lease needs into the lease's folder.
  Pause markers and journal lines become a file contract documented in
  `docs/STATE.md`, and the lease gets its own writer for it (about 30
  lines). Behaviour stays the same. A separate issue covers the fact that
  the lease stops units outside the one lock.
- **persona** imports `state.Persona`. It will read the persona package
  that the kernel already renders (`persona/<name>/persona.json`) with its
  own decoder, and embed the `joshua` fallback itself.

### Order of the PRs (after the owner's yes)

1. Pure moves with no behaviour change, golden/e2e/vm-install green: stick,
   then frage + automat + mcpserver. Each PR also updates its Containerfile,
   workflow path filters and its `loc.sh` budget.
2. Extractions: persona (its own decoder), then lease (rendered unit map,
   file contract).
3. Split `cmd/laib` into the packages above. The first PRs only move code,
   and the boundary check comes in the same PR as the package it guards.
4. Template checks: the `check` hook, then HA, Stimme and the Bote route move
   out. Each is weighed against today under #246, since the family sees the
   same checklist and QR.
5. `scripts/loc.sh` gets the cap with its reason next to the number. It warns
   at 95 %, and CI on `main` opens or updates the "Kern-Diät" issue, which
   lists the box-chore packages and their sizes as the first candidates.
   CLAUDE.md rule 3 is rewritten to match.

### The honest cap: 14,000

- **What the restructured kernel needs:** trust kernel ~9,650 + box chores
  ~2,240 + the check runner ~50 ≈ 11,950, plus about 300 for the split
  itself (package docs, exported names, the env passed in), so **~12,250**.
- **Headroom: ~1,750, about 14 %.** It is enough for the open trust-kernel
  and box-chore work at the recent pace (the kernel without the programs
  grew ~1,900 lines from 10-13 to 10-17, mostly the skill registry and
  typed MCP). It is too little to absorb another program. The 95 % warning
  comes at 13,300, roughly a month's normal growth after the build-out,
  and it opens the diet issue before the cap bites.
- **Why not keep 16,000:** after the moves it would leave 3,750 lines (30 %)
  of slack. Nobody would ask what belongs in the kernel for months, and that
  is the failure #309 describes.
- **Why not tighter (13,000):** it would be met the way 16,000 was, by
  squeezing, which is a review finding (#309).
- Crossing the cap means raising it in the same PR, with its one-line reason
  in `loc.sh` and the owner's approval (#309).

### Weighed against today (#246)

- **Maintenance gets better.** Each package has one job and a checked
  boundary. The count measures the kernel and not six bystanders. A
  template's quirks live in the template, so a new template no longer means
  a kernel PR.
- **The family's comfort is not worse.** Every move is behaviour-neutral, the
  HA checklist and the QR stay where the admin sees them, and the lease and
  persona keep their wire behaviour.
- **What it costs:** about 300 lines for the package split, and six small
  budget lines in `loc.sh` instead of one big one.
