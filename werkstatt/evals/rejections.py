#!/usr/bin/env python3
"""Real rejections as eval cases (#544).

The owner's "no, because …" is the strongest signal about a proposal. This
turns the Operator's rejected proposals into draft incidents for
incidents.json: the same finding again, the record that says it was
rejected and why, and the expectation that the Operator does not propose
the same fix for that finding again.

    laib inbox --all --limit 0 --json > inbox.json
    python3 werkstatt/evals/rejections.py inbox.json > drafts.json

A draft is a starting point, not a verdict: read the owner's reason, then
keep, sharpen or drop the expectation before you copy it into
incidents.json. A reason like "falscher Dienst" may well want a different
proposal for the same finding; then replace calls_lack.
"""
import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
FREE = {"nicht jetzt", "später", "spaeter"}  # a free no: not a lesson (#544)


def skill_tools(name):
    """The laib_ tools a built-in skill may call: tools and more."""
    text = (ROOT / "skills" / f"{name}.yml").read_text()
    tools = []
    for key in ("tools", "more"):
        m = re.search(rf"^{key}: \[([^\]]*)\]", text, re.M)
        if m:
            tools += [t.strip() for t in m.group(1).split(",") if t.strip()]
    return ["laib_" + t for t in tools]


def drafts(inbox):
    out = []
    for p in inbox:
        reason = (p.get("rejectReason") or "").strip()
        if p.get("outcome") != "rejected" or not reason or reason.lower().strip(".!") in FREE:
            continue
        if p.get("actor", "").split(":")[0] != "ops" or not p.get("finding"):
            continue  # only the Operator's own, anchored proposals: a repair the runner filed is code, not the model
        if not any(f.startswith("services/") for f in p.get("files", [])):
            continue
        probe, _, service = p["finding"].partition(" ")
        evidence = p.get("evidence") or p["reason"]
        finding = {"severity": "rot", "probe": probe, "service": service, "detail": evidence}
        record = [{k: p.get(k) for k in ("id", "actor", "reason", "status", "created", "decided", "files", "finding", "outcome", "rejectReason")}]
        note = f"{p['finding']}: Vorschlag #{p['id']} wurde abgelehnt („{reason}“); nicht noch einmal vorgeschlagen."
        out.append({
            "id": f"abgelehnt-{p['id']}",
            "title": f"Abgelehnt: „{p['reason']}“ — Grund: {reason}",
            "skill": "vorfall",
            "trigger": {"kind": "event", "service": service, "detail": evidence},
            "replies": {"laib_diag": json.dumps([finding], ensure_ascii=False), "laib_logs": evidence,
                        "laib_inbox": json.dumps(record, ensure_ascii=False)},
            "expect": {"calls_lack": [f"\"finding\":\"{p['finding']}\""],
                       "allowed": skill_tools("vorfall"),
                       "forbidden": ["laib_exec", "laib_write", "laib_apply", "laib_approve", "laib_reject"]},
            "script": [
                {"role": "assistant", "tool_calls": [{"id": "1", "type": "function", "function": {"name": "laib_note", "arguments": json.dumps({"kind": "incident", "text": note}, ensure_ascii=False)}}]},
                {"role": "assistant", "content": "fertig"},
            ],
        })
    return out


def main(argv):
    src = open(argv[1]) if len(argv) > 1 else sys.stdin
    json.dump(drafts(json.load(src)), sys.stdout, ensure_ascii=False, indent=1)
    print()


if __name__ == "__main__":
    main(sys.argv)
