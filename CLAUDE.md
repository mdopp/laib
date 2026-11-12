# Laib — house rules for every session, human or agent

Laib is the agent-first control plane for one home box. Read `README.md`, then
`docs/DESIGN.md`, then the ADR index in `docs/adr/README.md`. The decisions in
`docs/adr/` are binding; the weirdness you are about to "fix" is usually one of
them.

## The three rules that outrank everything else

1. **Scripts do the mechanics, agents produce artifacts.** An agent's output is
   a diff, a proposal, a question, a report or a test. Applying, waiting,
   polling, snapshotting, rolling back and notifying are done by `laib` itself,
   because those must happen 100 % of the time, also when the session dies.
   Never put a step that must always happen into prose an LLM re-reads.
2. **Proof is on the box, not in-process.** A green unit test proves the
   renderer. `laib apply` ending green on the box proves the feature. If you
   cannot verify on the box, say so in those words.
3. **KISS, and the kernel grows only on purpose.** Every program has a
   production-line cap in `scripts/loc.sh` with its reason next to the
   number: the kernel 22,370 (#609, +10 % 2026-10-30, homes#362, #1194), the Operator 3,300, and one for each program
   that left the kernel (ADR 0005 amendment #309, ADR 0023): stick, frage,
   automat, directory, mcpserver, disk, notify, haushalt and, since #677, the
   lease. Tests are not
   capped. At 95 % CI warns and opens the
   "Kern-Diät" issue; crossing a cap means raising it in the same PR, with
   its reason and the owner's approval. Lines count as gofmt writes them,
   and CI fails on a file gofmt would change. No gate until
   something recurs. Three similar lines beat a premature abstraction. No
   feature flags for cases that cannot happen.

## Where things are

- State repo on the box: `/mnt/data/laib/state` (`docs/STATE.md`).
- Journal: `/mnt/data/laib/journal/YYYY-MM-DD.jsonl`.
- Quadlet output: `~/.config/containers/systemd/<service>/`.
- Rendered proxy / sso / dns configs: `/mnt/data/laib/render/<role>/`.
- The binary: `/var/opt/laib/laib`, unit `laib.service` (user `core`).
- Names: check a new name against the glossary in `docs/adr/0021-names.md`;
  one name means one thing.

## Working

- **Issues first.** Non-trivial work starts as a GitHub issue: symptom, goal,
  starting-point files, acceptance. The "how" evolves in the PR.
- **Conventional Commits**, paren-free subjects beyond `(scope)`. Scopes mirror
  the path: `feat(render)`, `fix(apply)`, `docs(adr)`, `feat(template)`.
- **Releases via release-please only.** Never bump a version by hand.
- **A renderer change ships with its golden file.** `TestGolden`
  (`internal/render/render_test.go`) renders every case under
  `testdata/golden` and fails on a file that differs, is missing or is new;
  it sees what those cases render, no more.
- **Weigh a change against what worked before.** Rethinking is welcome, but
  the new solution must be easier to maintain or more pleasant for the family,
  and neither noticeably worse (owner, 2026-10-16, #246). Before replacing a
  solution, read the one it replaces (for anything ServiceBay did: its
  template and `assists/`), and say in the PR what was there and which of the
  two gets better. The Operator's prompts carry the same rule.
- **Every touch answers at once** (#588, `docs/DESIGN.md`): an action
  the family or an agent triggers answers at once, shows its progress and
  ends visibly where it began — done, an error with its reason and the
  next step, or waiting with a choice. A silent button, an endless spinner
  or a raw error is a bug; phone first. A test runs every POST route of
  the pages (#854); raw text goes through `proposal.Plain` and under
  Details, and a lint holds it there (#855).
- **Every behaviour has its Ablauf** (owner, 2026-11-12). `docs/ABLAEUFE.md`
  holds what the owner and the family see Laib do, as numbered scenarios in
  plain German (`L-1`…), only promises (rules at its top). A feature PR
  adds or changes its scenario and the test that names it with the
  scenario's fingerprint (`// Ablauf L-12 @3f9a`); a changed scenario
  turns its tests red until each is re-read and re-stamped (`scripts/ablaeufe.py
  --stempeln L-12` prints the new one, writes nothing); a scenario without a
  test is red unless it is in `scripts/ablaeufe-baseline.txt`, which only
  shrinks.
- **Regression ⇒ gate.** The second time a shape breaks, it gets a test or a
  check. Not the first time.
- **Whose data, who reaches it: `docs/DATA.md`.** A change to a template,
  Homes, the archive or the app that adds an account model, gives a
  service a key to another service's data, or lets one person's data
  reach others updates `docs/DATA.md` in the same PR — the section of
  the service and the table of crossings (owner, 2026-11-10: „Das sollte
  direkt in der Architektur stehen“).
- **No secrets in the tree.** Templates declare secrets in `laib.yml`; values
  live only in `secrets.age` on the box.
- **Finish the work.** Done means merged, released, applied on the box and
  verified green. An issue that names its proof as an `Auf der Box:` line
  with a read-only `laib` command is checked after its release lands,
  reopened on red (`werkstatt-boxcheck`, #867, `docs/TESTING.md`). If the operator must decide something, ask with concrete
  options, as early as you know, then carry on.

## The rules and the checks that hold them

Nobody reviews a PR before it merges (#866): a rule is held by a check that
runs in CI in seconds, or it is a guideline and says so. No rule here
"fails review".

| Rule | Check |
|---|---|
| Each program under its cap, no import across the boundaries; a shared leaf (`atomicfile/`) imports nothing of laib | `scripts/loc.sh` |
| Lines as gofmt writes them | `gofmt -l` in `ci.yml` |
| A skill: at most 55; its script one file, one theme, `run.py` at most 220 lines; a fact about the box is Laib's, read through a verb (`laib planned`), what two skills share is in `skills/lib/`; a second file in a skill warns (#839, #978, `docs/SKILLS.md`) | `scripts/loc.sh` |
| A renderer change ships with its golden file | `TestGolden` |
| The Operator's duties run in code, not in a prompt (rule 1, #858) | `operator/duties858_test.go` |
| A skill's prompt names only tools on its menu (#858) | `TestEveryToolAPromptNamesIsOnItsMenu` |
| Docs match the code: layout, the Operator's tools, ports, counts | `scripts/drift.sh` |
| What only the box answers carries a read-only probe (`<!-- box: laib … ~ … -->`), what only a person answers a Box-Aufgabe; the Werkstatt writes the answers and dates (#1169) | `scripts/drift.sh`, `werkstatt-docprobe` |
| A template with `private:`, `oidc:` or `perPerson:` has its section in `docs/DATA.md`, naming its privacy class | `TestEveryTemplateWithFamilyDataIsInDataDoc` |
| What builds an image is pinned: every `uses:` by commit, every `FROM` by digest (ADR 0034, #1165); the same file in each of the owner's repos and the scaffold | `scripts/pins.sh` |
| Dead code, staticcheck findings, duplicates and complexity only go down (see below) | `scripts/ratchet.py`, CI job `ratchet` |
| The trust kernel's tests bite (ADR 0019 §2, #1167): in `internal/policy`, `envelope`, `token`, `proposal`, `inbox` no new mutant survives | `scripts/ratchet.py mutation`, workflow `mutation` (a PR touching those packages, and nightly) |
| The gates have one source (#1170): `ratchet.py`, `test_ratchet.py`, `ablaeufe.py`, `test_ablaeufe.py`, `pins.sh`, `copies.sh` are `werkstatt/scaffold/repo/scripts/`'s, copied unchanged into every repo of the household (and `loc.sh` with one cap where a repo has `scripts/loc.txt`); a fix goes here first, then to the copies | `scripts/copies.sh`, in each repo's CI |
| Every scenario of `docs/ABLAEUFE.md` is named by a test (`// Ablauf L-12 @3f9a`), with the fingerprint of its current text; no test names an unknown id, the baseline only shrinks | `scripts/ablaeufe.py` |
| Every touch answers at once (#588): every POST route ends done, red with a next step, or waiting with a limit | `cmd/laib/taps854_test.go` |
| No raw or technical text in front of people (#855) | `cmd/laib/plain855_test.go`, `notify/plain855_test.go`, `frage/plain855_test.go` |

**The ratchet** (owner, 2026-11-03). `scripts/ratchet.py` runs four pinned
tools and compares with `scripts/ratchet/<tool>.txt`, today's findings
without line numbers: deadcode over every main package (`dead`: no caller
even in a test; `test-only`: production code only a test runs, fakes
aside), staticcheck's default checks with U1000 (not ST1005: the error
strings are German sentences), dupl at 75 tokens on production files, and
gocognit over 30 on production functions. CI fails when

- a finding is new or its number rose (a complexity score counts) — fix
  it; if it must stay, `scripts/ratchet.py --update --accept <tool>`, the
  reason in the PR and the owner's yes, as for a line cap;
- a finding went away or its number fell — lower the baseline in the same
  PR: `scripts/ratchet.py --update` (it lowers only) and commit the files.

A moved or renamed function is new under its new name and gone under the
old: `--update --accept`, the PR shows the move. `scripts/ratchet.py
deadcode` runs one tool. The same file ratchets a Python repo (a
`ruff.toml` at its root, as dokumente): ruff's lint and McCabe complexity
over 10, pinned in the script (#1170).
A fifth tool runs only when named: `scripts/ratchet.py mutation` (#1167)
runs gremlins over the packages in `scripts/ratchet/mutation.pkgs` with
each package's own tests; a survivor (`lived`, or `not-covered`: no test
of the package runs the line) is keyed by file, mutator and the line's
text. It takes minutes, so the workflow `mutation` runs it on a PR that
touches those packages, and nightly.

Guidelines, no check: what a `docs/DATA.md` section says beyond its class, issues first, Conventional Commits, releases via
release-please, weighing a change against what worked before, regression ⇒
gate, no secrets in the tree, finishing the work. Proof on the box (rule 2)
is the box's, not CI's.

## Language

Docs and commit messages in English. The operator speaks German; user-facing
strings in the pages and the Solaris cards are German.
