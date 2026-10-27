"""lehre: a solved incident becomes a proposed lesson (#544), no model.

Moved out of the Operator's Go (operator/lessons.go, #839). The runner
hands the inbox (data.history) and vorfall's sessions (data.sessions); for
each incident session whose proposal laib measured as effective, the script
proposes lessons/<probe>-<service>.md once per finding: the finding, the
cause the session saw, what fixed it. A lesson is a file in the state repo,
class human: only the owner lets it in, and then laib context shows it to
every session. A finding with a lesson proposed once, taken or not, gets
none again.
"""

import laibskill
from laibskill import field, first_line, one_line


def lesson_finding(p):
    """The finding a lesson proposal is about: its reason is "Lehre: <finding> — <fix>"."""
    r = field(p, "reason")
    if r.startswith("Lehre: "):
        r = r[len("Lehre: "):]
    return r.split(" — ", 1)[0]


def run(s):
    history = s.data.get("history") or []
    by_id = {field(p, "id", 0): p for p in history}
    had = {lesson_finding(p) for p in history if field(p, "reason").startswith("Lehre: ")}
    said = []
    for sess in s.data.get("sessions") or []:
        for pid in field(sess, "proposed", []) or []:
            p = by_id.get(pid)
            finding = field(p, "finding")
            if p is None or field(p, "outcome") != "effective" or not finding or finding in had:
                continue
            had.add(finding)
            name = "lessons/" + finding.replace(" ", "-") + ".md"
            body = ["# Lehre: " + finding, "", "- Befund: " + finding, "- Ursache, wie der Vorfall sie sah: " + one_line(field(sess, "note")),
                    f"- Was half: {one_line(field(p, 'reason'))} (Vorschlag #{pid}, wirksam: der Befund war danach weg)"]
            patch = f"--- /dev/null\n+++ b/{name}\n@@ -0,0 +1,{len(body)} @@\n+" + "\n+".join(body) + "\n"
            r = s.call("laib_propose", reason="Lehre: " + finding + " — " + one_line(field(p, "reason")), patch=patch)
            if r.error:
                said.append(f"Lehre zu {finding} ließ sich nicht vorschlagen: {first_line(r.text)}.")
            else:
                said.append(f"Lehre zu {finding} vorgeschlagen ({name}), für einen Menschen.")
    note = " ".join(said) or "Keine neue Lehre: kein Vorfall, dessen Befund seitdem weg ist."
    s.call("laib_note", kind="watch", text=note)
    return laibskill.done(outcome="idle", note=note)
