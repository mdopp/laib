package operator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
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

// #861: a lesson was the session's note transcribed. The runner lets lehre's
// script ask the model one short question, without tools, counted on the
// session; only a skill whose model judges may ask.
func TestALessonIsCondensedByOneShortQuestion(t *testing.T) {
	home := t.TempDir()
	incident, _ := json.Marshal(Session{Skill: "vorfall", Trigger: Trigger{Kind: "event", Service: "whoami"}, Note: "whoami rot: das Image fehlt.", Proposed: []int{12}})
	os.WriteFile(filepath.Join(home, "incidents.jsonl"), append(incident, '\n'), 0o644)
	fixed := opsPast(12, "effective", "", time.Hour)
	fixed.Reason = "whoami zurück auf v1.10"
	mcp, calls := fakeMCP(t, map[string]string{"laib_inbox": inboxJSON(t, fixed), "laib_propose": `{"id":13}`})
	asked := 0
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Tools []ToolDef
		}
		json.NewDecoder(r.Body).Decode(&req)
		asked++
		if len(req.Tools) > 0 {
			http.Error(w, "a question has no tools", 400)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": Message{Role: "assistant", Content: "Ursache: ein Tag, den es nicht gibt.\nZeichen: manifest unknown beim Pull.\nLösung: zurück auf die Version, die lief.\nNicht anwenden, wenn: die Registry nicht erreichbar ist."}}},
			"usage": map[string]int{"prompt_tokens": 300, "completion_tokens": 60}})
	}))
	t.Cleanup(llm.Close)
	op := &Operator{Skills: "../skills", MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL, Model: "test"}, Home: home}
	sk := skillFor(t, Trigger{Kind: "slot"}, "lehre")
	s, err := op.Run(context.Background(), sk, Trigger{Kind: "manual"})
	joined := strings.Join(*calls, "\n")
	if err != nil || asked != 1 || s.In != 300 || s.Out != 60 || countPrefix(s.Script, "frage") != 1 || !strings.Contains(joined, `+- Zeichen: manifest unknown beim Pull.`) || strings.Contains(s.Note, "nicht verdichtet") {
		t.Fatalf("the lesson was not condensed by one counted question: %v %d %+v %s", err, asked, s, joined)
	}
	sk.Modell = "nein" // a script that works alone asks no model
	os.WriteFile(filepath.Join(home, "incidents.jsonl"), append(incident, '\n'), 0o644)
	if s, _ := op.Run(context.Background(), sk, Trigger{Kind: "manual"}); asked != 1 || !strings.Contains(s.Note, "nicht verdichtet: das Modell antwortete nicht") {
		t.Fatalf("modell: nein asked the model: %d %q", asked, s.Note)
	}
}

// #861: nothing recorded which lesson a session saw or whether its finding
// came back. A session's record names the lessons its prompt carried, and
// an incident a look opened names its findings, so the morning report can
// count both (skills/morgenbericht/test_run.py).
func TestTheRecordNamesTheLessonsOfferedAndTheFindings(t *testing.T) {
	box := `{"ready":{"state":"bereit"},"lessons":["# Lehre: unit whoami\n\n- Ursache: ein Tag, den es nicht gibt."]}`
	mcp, _ := fakeMCP(t, map[string]string{"laib_context": box, "laib_diag": "[" + redBote + "]", "laib_logs": boteLogs})
	llm := fakeLLM(t, []Message{{Role: "assistant", Content: "fertig"}})
	home := t.TempDir()
	op := &Operator{Skills: "../skills", MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL, Model: "test"}, Home: home}
	tr := Trigger{Kind: "event", Service: "whoami", Detail: "whoami: unit failed"}
	if s, err := op.Run(context.Background(), skillFor(t, tr, ""), tr); err != nil || !slices.Equal(s.Lessons, []string{"unit whoami"}) {
		t.Fatalf("the session does not name the lesson it was offered: %v %+v", err, s)
	}
	look, _ := op.Run(context.Background(), skillFor(t, Trigger{Kind: "tick"}, "routineblick"), Trigger{Kind: "tick"})
	if len(look.opened) != 1 || !slices.Equal(look.opened[0].Finding, []string{"unit bote"}) || look.Lessons != nil {
		t.Fatalf("the incident does not name its finding, or a look claims a lesson: %+v", look)
	}
	if ss := op.sessions("vorfall", time.Time{}); len(ss) != 1 || ss[0].Lessons[0] != "unit whoami" {
		t.Fatalf("the record lacks the lesson: %+v", ss)
	}
}
