package operator

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Lessons (#544): after an incident whose finding went away, the runner
// proposes what fixed it as a lesson; the owner lets it in or not, and a
// lesson whose later proposals keep failing is named for removal.

// lesson: a proposal whose only file is a lesson (lessons/<name>.md).
func (p past) lesson() bool {
	return len(p.Files) > 0 && !slices.ContainsFunc(p.Files, func(f string) bool { return !strings.HasPrefix(f, "lessons/") })
}

// lessonFinding is the finding a lesson proposal is about: the runner
// writes its reason as "Lehre: <finding> — <fix>".
func lessonFinding(p past) string {
	f, _, _ := strings.Cut(strings.TrimPrefix(p.Reason, "Lehre: "), " — ")
	return f
}

// Stale lists the lessons the owner let in whose finding the Operator has
// since proposed for at least twice without success (#544): candidates to
// remove, for the morning report.
func Stale(all []past) string {
	var out []string
	for _, l := range all {
		if !l.lesson() || l.Status != "approved" || !strings.HasPrefix(l.Reason, "Lehre: ") {
			continue
		}
		var failed []string
		for _, p := range mine(all) {
			if p.Finding == lessonFinding(l) && p.Created.After(l.Decided) && slices.Contains(counted[1:], p.Outcome) {
				failed = append(failed, fmt.Sprintf("#%d", p.ID))
			}
		}
		if len(failed) >= 2 {
			out = append(out, fmt.Sprintf("Lehre %s hilft nicht: %d spätere Vorschläge ohne Erfolg (%s), zum Entfernen.", strings.Join(l.Files, ", "), len(failed), strings.Join(slices.Sorted(slices.Values(failed)), ", ")))
		}
	}
	return strings.Join(out, " ")
}

// lessons files a lesson for every incident session whose proposal went
// away as it should (#544): the finding, what the session saw, what fixed
// it. A lesson is a file in the state repo (lessons/<probe>-<service>.md),
// so it is class human: only the owner lets it in, and then laib context
// shows it to every session. A finding with a lesson proposed once, taken
// or not, gets none again. The result is the note.
func (o *Operator) lessons(ctx context.Context, mcp *MCP, all []past) string {
	byID := map[int]past{}
	for _, p := range all {
		byID[p.ID] = p
	}
	had := map[string]bool{}
	for _, p := range all {
		if strings.HasPrefix(p.Reason, "Lehre: ") {
			had[lessonFinding(p)] = true
		}
	}
	var h []string
	for _, s := range o.sessions() {
		if s.Skill != "vorfall" {
			continue
		}
		for _, id := range s.Proposed {
			p, ok := byID[id]
			if !ok || p.Outcome != "effective" || p.Finding == "" || had[p.Finding] {
				continue
			}
			had[p.Finding] = true
			name := "lessons/" + strings.ReplaceAll(p.Finding, " ", "-") + ".md"
			body := []string{"# Lehre: " + p.Finding, "", "- Befund: " + p.Finding, "- Ursache, wie der Vorfall sie sah: " + oneLine(s.Note),
				fmt.Sprintf("- Was half: %s (Vorschlag #%d, wirksam: der Befund war danach weg)", oneLine(p.Reason), p.ID)}
			patch := fmt.Sprintf("--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n+%s\n", name, len(body), strings.Join(body, "\n+"))
			out, isErr, err := mcp.Call(ctx, "laib_propose", map[string]any{"reason": "Lehre: " + p.Finding + " — " + oneLine(p.Reason), "patch": patch})
			if err != nil || isErr {
				h = append(h, fmt.Sprintf("Lehre zu %s ließ sich nicht vorschlagen: %s.", p.Finding, firstLine(cmp.Or(out, fmt.Sprint(err)))))
				continue
			}
			h = append(h, fmt.Sprintf("Lehre zu %s vorgeschlagen (%s), für einen Menschen.", p.Finding, name))
		}
	}
	return strings.Join(h, " ")
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// sessions reads the Operator's own record of its sessions, oldest first.
func (o *Operator) sessions() []Session {
	if o.Home == "" {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(o.Home, "incidents.jsonl"))
	if err != nil {
		return nil
	}
	var out []Session
	for _, line := range strings.Split(string(b), "\n") {
		var s Session
		if json.Unmarshal([]byte(line), &s) == nil && s.Skill != "" {
			out = append(out, s)
		}
	}
	return out
}
