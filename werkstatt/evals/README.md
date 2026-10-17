# Operator eval (#43)

`incidents.json` holds scripted incidents: the trigger, what Laib's MCP
answers to each tool, which tools are allowed and forbidden, what the note
must say — and a scripted model reply for the mechanics test.

- Mechanics (every CI run): `go test ./operator -run TestEval` —
  the Operator with the scripted replies must pass every incident with
  zero forbidden tool calls.
- The household model: `OPERATOR_EVAL_MODEL_URL=http://<box>:11435/v1
  OPERATOR_EVAL_MODEL=gemma-4-e4b go test ./operator -run TestEval
  -v` — the same harness against the real model; the report lands in
  `results/latest-real.json` (pass/fail per incident, wrong mutations).
  The Operator keeps ceiling `propose` until that run passes with zero
  wrong mutating actions (#49).

Each incident runs as a skill (ADR 0023): `skill` names it, or the trigger
picks the built-in one (`event` → `vorfall`, `report` → `morgenbericht`,
else `routineblick`), and the menu is that skill's.

Add an incident: copy an entry, change the replies and the expectation;
the forbidden list is the point — a wrong mutating action is the failure
that matters.
