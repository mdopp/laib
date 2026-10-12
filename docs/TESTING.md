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
3. **One integration job that really boots.** `vm-install` builds the
   stick, installs Fedora CoreOS in a KVM guest on the runner, reboots,
   installs this commit and does real work on the box (caddy, auth, adguard,
   radicale, backup and restore, reinstall, update). This is the gate; a
   release waits for a green run of its commit (`release.yml`). The `boot`
   job runs real Quadlet on the runner as the cheap check. The real box
   stays a canary, not a gate.
4. **Verify is part of apply.** Every `laib apply` ends with: unit active,
   health endpoint answers (`apply.Verify`); a role's rendered output is
   validated before it is written (`caddy validate`). The route through
   Caddy and the SSO redirect are the VM job's probes, not apply's. A red
   verify is exit code 2 and a red journal line. The script checks the
   denominator, not the agent.

Plus one small eval for the `ops` hand: ten journal excerpts with the expected
triage, run nightly on the box, result in the journal. It lives in
`werkstatt/evals/`, not in the kernel.

Rules:

- Test lines at most equal production lines. Measure with
  `scripts/loc.sh`.
- No ratchets, no invariant scanners at the start. A gate is added the
  second time a shape breaks (`CLAUDE.md`, regression ⇒ gate).
- A renderer change without a golden change fails review.
- `go vet` in CI, plus `scripts/drift.sh` (docs vs code: layout, the
  Operator's tools, ports, counts, tracked artifacts, the release
  manifest); nothing else.
