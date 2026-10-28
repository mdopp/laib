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

## Amendment 2026-10-17 (#309): structure the kernel, then an honest cap

**Status: accepted, 2026-10-17.** The owner decided on the plan (#315):
the cap is **15,000** with the warning at 95 % (14,250); the lease stays in
the kernel; each program outside it gets its own budget; the relay stays in
the trust kernel.

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

**Programs in the kernel's count** (3,870 lines, 24 % of it); all but the lease move out:

| Program | Lines | Runs |
|---|---:|---|
| `cmd/laib-stick` + `internal/stick` | 1,468 | on the laptop; builds the install stick |
| `cmd/laib-lease` + `internal/lease` | 908 | the llama template's sidecar: policy proxy and GPU lease (ADR 0011); **stays** (owner, 2026-10-17): it pauses services and journals like a hand of the kernel. Reversed 2026-10-26, #677: see "The lease leaves" below |
| `cmd/persona` + `internal/persona` | 701 | the Bote's persona engine (ADR 0020), an image in the bote pod; the reason for the 16k raise above |
| `cmd/automat` + `internal/automat` | 421 | the Bote's HA-automation MCP tool, an image |
| `cmd/frage` + `internal/frage` | 261 | the "was ist …" tool (ADR 0015), an image |
| `internal/mcpserver` | 111 | the small MCP server that only persona, automat and frage use (the kernel has its own) |

Each of these is already its own binary with its own Containerfile or
release asset. None of them is part of the trust kernel. The five that move
sit in the
kernel's tree and count because `cmd/` and `internal/` were the only place
for Go code before `operator/` existed.

### Target layout

1. **Programs move out, as the Operator did (#268, ADR 0023 §1).** Each gets
   its own top-level tree with its package and `cmd/`: `stick/`,
   `persona/`, `automat/`, `frage/` (the last three already hold the
   Containerfiles), and `mcpserver/` as their shared library. Each program
   gets its own budget line in `scripts/loc.sh`: today's size plus about a
   quarter, rounded (stick 1,800 · persona 900 · automat 600 · frage 400 ·
   mcpserver 200). Each budget keeps the rule that tests are at most equal
   to production. `relay` stays in the kernel because it is invariant §2e;
   `lease` stays because it writes the kernel's pause markers and journal
   (`internal/lease`, `cmd/laib-lease` unchanged). That reason is gone since
   ADR 0027: the lease moved out on #677 (below).
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

One coupling needs more than a move:

- **persona** imports `state.Persona`. It will read the persona package
  that the kernel already renders (`persona/<name>/persona.json`) with its
  own decoder, and embed the `joshua` fallback itself.

### Order of the PRs

1. Pure moves with no behaviour change, golden/e2e/vm-install green: stick,
   then frage + automat + mcpserver. Each PR also updates its Containerfile,
   workflow path filters and its `loc.sh` budget.
2. Extraction: persona (its own decoder).
3. Split `cmd/laib` into the packages above. The first PRs only move code,
   and the boundary check comes in the same PR as the package it guards.
4. Template checks: the `check` hook, then HA, Stimme and the Bote route move
   out. Each is weighed against today under #246, since the family sees the
   same checklist and QR.
5. `scripts/loc.sh` gets the cap with its reason next to the number. It warns
   at 95 %, and CI on `main` opens or updates the "Kern-Diät" issue, which
   lists the box-chore packages and their sizes as the first candidates.
   CLAUDE.md rule 3 is rewritten to match.

### The honest cap: 15,000 (owner, 2026-10-17)

- **What the restructured kernel needs:** trust kernel ~9,650 + box chores
  ~2,240 + the lease ~910 + the check runner ~50 ≈ 12,850, plus about 300
  for the split itself (package docs, exported names, the env passed in),
  so **~13,150**.
- **Headroom: ~1,850, about 14 %.** It is enough for the open trust-kernel
  and box-chore work at the recent pace (the kernel without the programs
  grew ~1,900 lines from 10-13 to 10-17, mostly the skill registry and
  typed MCP), and too little to absorb another program. The 95 % warning
  comes at 14,250 and opens the "Kern-Diät" issue before the cap bites.
- **Why not keep 16,000:** after the moves it would leave about 2,850 lines
  (18 %) of slack beyond that. Nobody would ask what belongs in the kernel
  for months, and that is the failure #309 describes.
- **Why not tighter (14,000, the plan's first number):** with the lease
  staying it would leave 850 lines; it would be met the way 16,000 was, by
  squeezing, which is no fix (#309).
- Crossing the cap means raising it in the same PR, with its one-line reason
  in `loc.sh` and the owner's approval (#309).

### Weighed against today (#246)

- **Maintenance gets better.** Each package has one job and a checked
  boundary. The count measures the kernel and not six bystanders. A
  template's quirks live in the template, so a new template no longer means
  a kernel PR.
- **The family's comfort is not worse.** Every move is behaviour-neutral, the
  HA checklist and the QR stay where the admin sees them, and persona keeps
  its wire behaviour.
- **What it costs:** about 300 lines for the package split, and five small
  budget lines in `loc.sh` beside the kernel's and the Operator's.

### Fewer, meatier packages (owner, 2026-10-18)

After the split the owner merged the smallest new packages, keeping the
boundaries: `run` (the lock and the apply run) and `converge` went into
`internal/apply`, the template lint into `internal/registry`, and the
intake into `internal/inbox`. The last one only works because the intake
imports no box chore. It also made the two seams (`inbox.Approved`,
`envelope.FileIssue`) unnecessary. `cmd/laib` stays at about 1,000 lines
of small verbs (no `internal/hands`). The Bote's Signal QR stays in
`internal/pages`.

### The first Kern-Diät cut: laib-disk (2026-10-19, #443)

At 14,387 of 15,000 the 95 % warning opened #443. The box chore
`internal/disk` (the data RAID, `laib disk`) and diag's RAID, SMART and
thermal probes moved into the program `disk/` (`laib-disk`, a release
binary beside `laib`, budget 700): they read `/proc`, `lsblk`, `mdadm` and
`smartctl` and need nothing of the kernel. The kernel keeps the verb `laib
disk` (tier apply; the program refuses `add` to anyone but a human) and
diag's call `laib-disk probe [--hardware]`, whose JSON findings it shows as
before. They meet over a process and its output, never by import
(`scripts/loc.sh`). Kernel 14,387 → 13,936.

### The second cut: laib-notify (2026-10-19, #443)

The owner's mail (#393) and the outside watchdog ping (#57) were two loops
in `laib serve`. They are the program `notify/` (`laib-notify`, budget 400),
a oneshot on a five-minute timer the kernel renders when there is a
watchdog URL or Authelia has mail settings. It reads laib like the Operator
does, over MCP as its own hand `notify` (`diag`, `updates`, `note`: ceiling
propose only because `note` is that tier; it proposes and decides nothing)
and `/ready`, with a token apply mints. `laib init` writes the hand, and
`state.Load` supplies it to an older `policy.yml`, like #444's `laib`. The
texts and the cadence are the same; a failed mail is a `note` (kind watch)
instead of a `notify` line, so it does not wake the Operator. Its token
and the SMTP password are in `laib-notify.env` (`EnvironmentFile=`), which
apply writes 0600, never in the unit; the Operator's token, model key and
usage-metrics token moved the same way into `laib-operator.env`, and a
changed env file restarts its long-running unit.

### The cap raised to 16,000 (owner, 2026-10-19, #443)

After train 21 the kernel stood at 14,492 of 15,000. The growth since
v0.7.0 is trust-kernel work: the relay's verdicts on Discord, repairs as
proposals, the credentials handover. The Kern-Diät (#443) moved out what
honestly could (laib-disk, laib-notify) and found no further honest cut:
the relay's transport reading is the sender check of §2e, the recovery
sheet and the credentials need the secrets, and the lease stays by the
owner's decision of 2026-10-17. The owner (mdopp) raised the cap to
**16,000**; the 95 % warning now sits at 15,200.

### Tests are not capped (owner, 2026-10-18)

"Tests may have more lines than code." The rule "test lines at most equal
production lines" (the Decision above, ADR 0023 §1) is dropped for every
program; `scripts/loc.sh` still prints the test count, and caps only
production lines.

### The lease leaves, and three household verbs go behind the door (owner, 2026-10-26, #677)

At 19,744 of 20,000 the 95 % warning opened #677 again. The owner chose
"a+b":

- **(a) The lease is its own program, `lease/`** (`laib-lease`, budget
  1,300). The 2026-10-17 reason for keeping it — it writes the kernel's
  pause markers and journal — has not held since ADR 0027 (#624): it runs
  in its container and pauses, resumes and notes through laib's MCP as the
  `lease` hand, like any program outside the kernel. It brings its own
  ~40-line MCP client and atomic write instead of importing `internal/mcp`
  and `internal/boxfs`; its tests resolve the real templates' units
  without `internal/state`. The image name (`ghcr.io/mdopp/laib-lease`) and
  the release's pin (`render.LeaseImage`) are unchanged.
- **(b) `zuhause` and `person_signal` are served by laib-haushalt** behind
  the verb door (ADR 0026 §4), with their names and shapes; laib-haushalt
  proposes the box.yml path with `laib propose --path` as the `haushalt`
  hand, which holds `zuhause` and `people.*.signal` in code. `person_pending`
  and `person_invite` stay in the kernel: both run the identity template's
  tool in the hooks image with its secrets (LLDAP's admin password,
  Authelia's data), which laib-haushalt must not get (ADR 0027). Haushalt's
  budget rises to 920.

Kernel 19,744 → 18,580 (the lease 1,076, the two verbs 88); lease 1,154,
haushalt 610 → 736.


## Amendment 2026-10-26 (#778): the relay leaves the trust kernel

Owner decision 2026-10-26 („B3“, #751): approvals only in the Inbox. The
row `internal/relay` + `cmd/relay` of the 2026-10-17 table is gone with the
relay; invariant §2e is now kept by the kernel alone (only the hand `human`
approves, ADR 0019 amendment of the same date). `internal/mcp`'s client,
whose last production user the relay was, moved into the tests.
