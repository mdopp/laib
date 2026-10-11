# Laib — testing

ServiceBay had 709 test files, 129k test lines, 19 invariants, three ratchets
and an out-of-band box verify, and still a third of its commits were fixes.
Tests proved the loader, not the box. Laib turns that around in four steps.

1. **Golden files for the renderers.** `internal/render` is pure: state in,
   files out. `testdata/golden/<case>/state/` is the input,
   `testdata/golden/<case>/expected/` the output. `go test ./internal/render`
   diffs them; `UPDATE_GOLDEN=1` rewrites. This is most of the tests.
2. **Contract tests.** `laib template lint` over every template in CI; schema
   tests for `laib.yml`, `policy.yml`, `box.yml`.
3. **One integration job that really boots.** CI starts a Fedora CoreOS VM
   (or a systemd-in-podman container as the cheap fallback), installs the
   binary, runs `laib apply` on `testdata/box/` and requires `laib status`
   green. This is the gate. The real box stays a canary via the `:dev`
   channel, not a gate.
4. **Verify is part of apply.** Every `laib apply` ends with: unit active,
   health endpoint answers, route answers through Caddy, SSO redirects where
   declared. A red verify is exit code 2 and a red journal line. The script
   checks the denominator, not the agent.

Plus one small eval for the `ops` hand: ten journal excerpts with the expected
triage, run nightly on the box, result in the journal. It lives in
`werkstatt/evals/`, not in the kernel.

Rules:

- Test lines at most equal production lines. Measure with
  `scripts/loc.sh`.
- No ratchets, no invariant scanners at the start. A gate is added the
  second time a shape breaks (`CLAUDE.md`, regression ⇒ gate).
- A renderer change without a golden change fails review.
- `go vet` and `staticcheck` in CI; nothing else.

## The Operator against a model (#43)

`internal/operator` has three kinds of tests: the loop with scripted
replies (text tool calls end red, invalid arguments never run, the prompt
names only the tools the hand has, the context is budgeted), the eval
harness over `werkstatt/evals/incidents.json` (zero forbidden tool calls,
every scripted incident passes), and — on demand — the same harness
against the household model (`OPERATOR_EVAL_MODEL_URL`), whose report is
checked into `werkstatt/evals/results/`. Verbs write to the writer the
`cliEnv` carries; MCP and `escalate` pass a buffer, the process stdout is
never swapped.
