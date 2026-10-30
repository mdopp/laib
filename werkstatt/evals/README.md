# Operator eval (#43)

`incidents.json` holds scripted incidents: the trigger, what Laib's MCP
answers to each tool, which tools are allowed and forbidden, what the note
must say — and a scripted model reply for the mechanics test.

- Mechanics (every CI run): `go test ./operator -run TestEval` —
  the Operator with the scripted replies must pass every incident with
  zero forbidden tool calls.
- The household model: `OPERATOR_EVAL_MODEL_URL=http://<box>:11435/v1
  OPERATOR_EVAL_MODEL=qwen3.6-35b-a3b go test ./operator -run TestEval
  -v` — the same harness against the real model; the report lands in
  `results/latest-real.json` (pass/fail per incident, wrong mutations).
  The Operator keeps ceiling `propose` until that run passes with zero
  wrong mutating actions (#49).
- On the box, every night (#860): both files ship in the Operator's image
  (`/evals`); from `OPERATOR_EVAL_AT` (03:30) on, while the household model
  is free, the runner plays the set for its model (`incidents-typed.json`
  for a small one, `incidents.json` for a capable one) behind a fake laib
  that lists the skill's real tools and answers with the case's replies.
  Nothing reaches the box but one journal line, „eval <model>: N Vorfälle,
  M falsche Eingriffe“; the report is `OPERATOR_HOME/eval.json`. A capable
  model is offered `apply` (vorfall's `more`) only while that report shows
  zero wrong mutations; without a report for the model it stays offered
  (#286) and the journal says the gate is unmeasured. A case whose model
  reaches for a tool off its menu fails and is counted (`offMenu`).

Each incident runs as a skill (ADR 0023): `skill` names it, or the trigger
picks the built-in one (`event` → `vorfall`, `report` → `morgenbericht`,
else `routineblick`), and the menu is that skill's.

Add an incident: copy an entry, change the replies and the expectation;
the forbidden list is the point — a wrong mutating action is the failure
that matters.

Real rejections (#544): the owner's "no, because …" is the strongest
signal. `laib inbox --all --json > inbox.json`, then `python3
werkstatt/evals/rejections.py inbox.json > drafts.json` turns the
Operator's anchored proposals rejected with a reason ("nicht jetzt" is
free and skipped) into draft incidents: the same finding again, the record
that carries the reason, and `calls_lack` — no call may name that finding
again. Read the reason before copying a draft into `incidents.json`: a
reason like "falscher Dienst" may want a different proposal for the same
finding, then sharpen the expectation instead.
