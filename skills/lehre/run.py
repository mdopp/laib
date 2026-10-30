"""lehre: a solved incident becomes a proposed lesson (#544), condensed by the model (#861).

Moved out of the Operator's Go (operator/lessons.go, #839). The runner
hands the inbox (data.history) and vorfall's sessions (data.sessions); for
each incident session whose proposal laib measured as effective, the script
proposes lessons/<probe>-<service>.md once per finding. A lesson is a file
in the state repo, class human: only the owner lets it in, and then laib
context shows it to every session. A finding with a lesson proposed once,
taken or not, gets none again.

#861: a lesson was the session's note transcribed. Now the model condenses
it in one short question (modell: urteil, s.ask): the cause, the sign, the
fix, and when not to apply it. An answer without those four lines, or no
answer, keeps the transcription, marked as not condensed: a lesson is
never lost to the model.
"""

import laibskill
from laibskill import field, first_line, one_line

LINES = ("Ursache:", "Zeichen:", "Lösung:", "Nicht anwenden, wenn:")
ASK = ("Verdichte diesen gelösten Vorfall zu einer Lehre für den nächsten, der ihn sieht. Antworte mit genau vier Zeilen, "
       "deutsch, je ein Satz, ohne Namen und ohne Zahlen, die nur heute galten:\n" + "\n".join(LINES) + "\n\n")


def lesson_finding(p):
    """The finding a lesson proposal is about: its reason is "Lehre: <finding> — <fix>"."""
    r = field(p, "reason")
    if r.startswith("Lehre: "):
        r = r[len("Lehre: "):]
    return r.split(" — ", 1)[0]


def condensed(answer):
    """The model's four lines, in order, or None when one is missing."""
    got = {}
    for line in (answer or "").splitlines():
        line = line.strip().lstrip("-* ").strip()
        for label in LINES:
            if line.startswith(label) and line[len(label):].strip():
                got.setdefault(label, label + " " + one_line(line[len(label):]))
    return ["- " + got[label] for label in LINES] if len(got) == len(LINES) else None


def body(s, finding, sess, p, pid):
    """The lesson's lines: condensed by the model, else transcribed."""
    seen = [f"Befund: {finding}", f"Notiz der Sitzung: {one_line(field(sess, 'note'))}", f"Was half: {one_line(field(p, 'reason'))}"]
    answer = s.ask(ASK + "\n".join(seen))
    lines = None if answer.error else condensed(answer.text)
    source = f"- Quelle: Vorschlag #{pid}, wirksam: der Befund war danach weg"
    if lines:
        return ["# Lehre: " + finding, ""] + lines + [source], ""
    return (["# Lehre: " + finding, "", "- Befund: " + finding, "- Ursache, wie der Vorfall sie sah: " + one_line(field(sess, "note")),
             f"- Was half: {one_line(field(p, 'reason'))} (Vorschlag #{pid}, wirksam: der Befund war danach weg)"],
            " (nicht verdichtet: " + ("das Modell antwortete nicht" if answer.error else "die Antwort hatte nicht die vier Zeilen") + ")")


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
            lines, raw = body(s, finding, sess, p, pid)
            patch = f"--- /dev/null\n+++ b/{name}\n@@ -0,0 +1,{len(lines)} @@\n+" + "\n+".join(lines) + "\n"
            r = s.call("laib_propose", reason="Lehre: " + finding + " — " + one_line(field(p, "reason")), patch=patch)
            if r.error:
                said.append(f"Lehre zu {finding} ließ sich nicht vorschlagen: {first_line(r.text)}.")
            else:
                said.append(f"Lehre zu {finding} vorgeschlagen ({name}){raw}, für einen Menschen.")
    note = " ".join(said) or "Keine neue Lehre: kein Vorfall, dessen Befund seitdem weg ist."
    s.call("laib_note", kind="watch", text=note)
    return laibskill.done(outcome="idle", note=note)
