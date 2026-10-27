package operator

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// #544: after an incident whose proposal made its finding go away, the
// runner proposes the lesson — the finding, the cause the session saw,
// what fixed it — as a file in the state repo a human lets in. No model
// runs, and a finding gets its lesson proposed once.
func TestASolvedIncidentBecomesALessonOnce(t *testing.T) {
	home := t.TempDir()
	incident, _ := json.Marshal(Session{Skill: "vorfall", Trigger: Trigger{Kind: "event", Service: "whoami"}, Note: "whoami rot: das Image v1.10.9 gibt es nicht.\nZurück auf v1.10.", Proposed: []int{12}})
	other, _ := json.Marshal(Session{Skill: "routineblick", Proposed: []int{11}})
	os.WriteFile(filepath.Join(home, "incidents.jsonl"), append(append(incident, '\n'), append(other, '\n')...), 0o644)
	fixed := opsPast(12, "effective", "", time.Hour)
	fixed.Reason = "whoami zurück auf v1.10"
	repaired := opsPast(11, "effective", "", time.Hour)
	repaired.Finding = "restore-test"
	replies := map[string]string{"laib_inbox": inboxJSON(t, fixed, repaired), "laib_propose": `{"id":13}`}
	mcp, calls := fakeMCP(t, replies)
	// no model: a call to this one fails the session
	op := &Operator{Skills: "../skills", MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: "http://127.0.0.1:1", Model: "none"}, Home: home}
	sk := skillFor(t, Trigger{Kind: "tick"}, "lehre")
	s, err := op.Run(context.Background(), sk, Trigger{Kind: "tick"})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(*calls, "\n")
	if countPrefix(*calls, "laib_propose") != 1 || !strings.Contains(joined, `+++ b/lessons/unit-whoami.md`) || !strings.Contains(joined, `Lehre: unit whoami — whoami zurück auf v1.10`) ||
		!strings.Contains(joined, "Ursache, wie der Vorfall sie sah: whoami rot: das Image v1.10.9 gibt es nicht. Zurück auf v1.10.") {
		t.Fatalf("want one lesson for the incident, none for the look's repair: %s", joined)
	}
	if !strings.Contains(s.Note, "Lehre zu unit whoami vorgeschlagen") || countPrefix(*calls, "laib_note") != 1 {
		t.Fatalf("the note names the lesson: %+v", s)
	}
	// the lesson is in the inbox now (open, or decided): not again
	lesson := past{ID: 13, Actor: "ops", Reason: "Lehre: unit whoami — whoami zurück auf v1.10", Status: "rejected", Files: []string{"lessons/unit-whoami.md"}, Outcome: "rejected"}
	replies["laib_inbox"] = inboxJSON(t, lesson, fixed, repaired)
	if s, _ := op.Run(context.Background(), sk, Trigger{Kind: "tick"}); countPrefix(*calls, "laib_propose") != 1 || !strings.Contains(s.Note, "Keine neue Lehre") {
		t.Fatalf("a lesson proposed twice: %v / %q", *calls, s.Note)
	}
	// a lesson is no proposal of the record: it is not counted
	if r := Record([]past{lesson}); r != "" {
		t.Fatalf("a lesson in the record: %q", r)
	}
}

// #544: a lesson whose finding the Operator keeps failing on after the
// owner let it in is named in the morning report for removal.
func TestALessonThatKeepsFailingIsNamedForRemoval(t *testing.T) {
	lesson := past{ID: 20, Actor: "ops", Reason: "Lehre: unit whoami — zurück auf v1.10", Status: "approved", Decided: day.Add(-10 * 24 * time.Hour), Files: []string{"lessons/unit-whoami.md"}}
	before := opsPast(19, "no-effect", "", 11*24*time.Hour) // before the lesson: not its fault
	a, b := opsPast(21, "no-effect", "", 5*24*time.Hour), opsPast(22, "rejected", "wieder dasselbe", 2*24*time.Hour)
	ok := opsPast(23, "effective", "", time.Hour)
	// the cases themselves are skills/morgenbericht/test_run.py (#839); this is the runner
	mcp, _ := fakeMCP(t, map[string]string{"laib_inbox": inboxJSON(t, ok, b, a, lesson, before)})
	llm, _ := recLLM(t, []Message{{Role: "assistant", Content: "Ruhige Nacht."}})
	op := &Operator{Skills: "../skills", MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL, Model: "test"}, Home: t.TempDir(), Now: func() time.Time { return day }}
	s, _ := op.Run(context.Background(), skillFor(t, Trigger{Kind: "report"}, "morgenbericht"), Trigger{Kind: "report"})
	if !strings.Contains(s.Note, "Lehre lessons/unit-whoami.md hilft nicht") {
		t.Fatalf("the morning report does not name the lesson: %q", s.Note)
	}
}
