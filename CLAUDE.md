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
   number: the kernel 20,000 (#609), the Operator 3,000, and one for each program
   that left the kernel (ADR 0005 amendment #309, ADR 0023): stick, frage,
   automat, persona, mcpserver, disk, notify, haushalt and, since #677, the
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
  or a raw error is a bug; phone first. #854 makes it a test over every
  POST route, #855 a lint for raw text.
- **Regression ⇒ gate.** The second time a shape breaks, it gets a test or a
  check. Not the first time.
- **No secrets in the tree.** Templates declare secrets in `laib.yml`; values
  live only in `secrets.age` on the box.
- **Finish the work.** Done means merged, released, applied on the box and
  verified green. If the operator must decide something, ask with concrete
  options, as early as you know, then carry on.

## The rules and the checks that hold them

Nobody reviews a PR before it merges (#866): a rule is held by a check that
runs in CI in seconds, or it is a guideline and says so. No rule here
"fails review".

| Rule | Check |
|---|---|
| Each program under its cap, no import across the boundaries | `scripts/loc.sh` |
| Lines as gofmt writes them | `gofmt -l` in `ci.yml` |
| A skill: at most 50, each `run.py` at most 200 lines (#839) | `scripts/loc.sh` |
| A renderer change ships with its golden file | `TestGolden` |
| The Operator's duties run in code, not in a prompt (rule 1, #858) | `operator/duties858_test.go` |
| A skill's prompt names only tools on its menu (#858) | `TestEveryToolAPromptNamesIsOnItsMenu` |
| Docs match the code: layout, the Operator's tools, ports, counts | `scripts/drift.sh` |
| Every touch answers at once (#588) | #854, #855 |

Guidelines, no check: issues first, Conventional Commits, releases via
release-please, weighing a change against what worked before, regression ⇒
gate, no secrets in the tree, finishing the work. Proof on the box (rule 2)
is the box's, not CI's.

## Language

Docs and commit messages in English. The operator speaks German; user-facing
strings in the pages and the Solaris cards are German.
