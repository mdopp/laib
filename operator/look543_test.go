package operator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// recLLM plays a script like fakeLLM and keeps every request's messages, so
// a test sees what the model was shown — and how often it was asked.
func recLLM(t *testing.T, script []Message) (*httptest.Server, *[][]Message) {
	t.Helper()
	var mu sync.Mutex
	var seen [][]Message
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []Message `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		i := len(seen) % len(script)
		seen = append(seen, req.Messages)
		mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": script[i]}}})
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

// the Kraftwerk's yellows on 2026-10-21 (#543): the restore test diag can
// repair, the watchdog only the owner can set
const (
	restoreTest = `{"severity":"gelb","probe":"restore-test","detail":"kein grüner Wiederherstellungstest in den letzten 35 Tagen","fix":"laib backup check","repair":["backup","check"]}`
	watchdog    = `{"severity":"gelb","probe":"watchdog","detail":"kein Wächter außerhalb der Box (box.yml watchdog.url)","fix":"einen Ping-URL eintragen"}`
)

// diagShown is what the model's last laib_diag call answered.
func diagShown(asked [][]Message) string {
	last := asked[len(asked)-1]
	for i := len(last) - 1; i >= 0; i-- {
		if last[i].Role == "tool" && last[i].Name == "laib_diag" {
			return last[i].Content
		}
	}
	return ""
}

// #543: a yellow with a repair verb is one proposal, not a note every ten
// minutes; an unchanged set of findings asks no model and writes no note,
// and that is decided by the runner, not the prompt. The heartbeat still
// beats.
func TestAnUnchangedLookAsksNoModelAndARepairIsProposedOnce(t *testing.T) {
	replies := map[string]string{"laib_diag": "[" + restoreTest + "," + watchdog + "]", "laib_repair": "Vorschlag #7 angelegt"}
	mcp, calls := fakeMCP(t, replies)
	llm, asked := recLLM(t, []Message{
		{Role: "assistant", ToolCalls: []ToolCall{tcall("1", "laib_note", `{"kind":"watch","text":"Zwei Punkte sind gelb."}`)}},
		{Role: "assistant", Content: "fertig"},
	})
	op := &Operator{MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL, Model: "test"}, Home: t.TempDir(), Typed: true}
	sk := skillFor(t, Trigger{Kind: "tick"}, "routineblick")

	s, err := op.Run(context.Background(), sk, Trigger{Kind: "tick"})
	if err != nil {
		t.Fatal(err)
	}
	if n := countPrefix(*calls, "laib_repair"); n != 1 || !strings.Contains(strings.Join(*calls, "\n"), `laib_repair {"probe":"restore-test"}`) {
		t.Fatalf("want one repair for the restore test, got %d: %v", n, *calls)
	}
	if !strings.Contains(s.Note, "Reparatur laib backup check vorgeschlagen") || len(*asked) == 0 {
		t.Fatalf("the first look asks the model and the note names the proposal: %+v", s)
	}
	if op.Looked.Load() == 0 {
		t.Fatal("a good session is a heartbeat")
	}

	// the same findings ten minutes later, an age in the detail moved on
	replies["laib_diag"] = "[" + strings.Replace(restoreTest, "35 Tagen", "36 Tagen", 1) + "," + watchdog + "]"
	before, notes := len(*asked), countPrefix(*calls, "laib_note")
	op.Looked.Store(0)
	s, err = op.Run(context.Background(), sk, Trigger{Kind: "tick"})
	if err != nil || s.Outcome != "unchanged" {
		t.Fatalf("unchanged findings ran a session: %+v %v", s, err)
	}
	if len(*asked) != before || countPrefix(*calls, "laib_note") != notes || countPrefix(*calls, "laib_repair") != 1 {
		t.Fatalf("an unchanged look asked the model, noted or proposed again: %v", *calls)
	}
	if op.Looked.Load() == 0 {
		t.Fatal("an unchanged look is a heartbeat too")
	}
}

// What changed reaches the model; what it already noted only as a count,
// so the owner's own item (the watchdog) is in one note, not in every one.
// The open repair is not filed again; a finding that went away and comes
// back is a new one.
func TestOnlyWhatChangedReachesTheModel(t *testing.T) {
	disk := `{"severity":"gelb","probe":"disk","detail":"/var hat 12.5% frei"}`
	replies := map[string]string{"laib_diag": "[" + restoreTest + "," + watchdog + "]"}
	mcp, calls := fakeMCP(t, replies)
	llm, asked := recLLM(t, []Message{
		{Role: "assistant", ToolCalls: []ToolCall{tcall("1", "laib_diag", `{}`)}},
		{Role: "assistant", ToolCalls: []ToolCall{tcall("2", "laib_note", `{"kind":"watch","text":"gesehen"}`)}},
		{Role: "assistant", Content: "fertig"},
	})
	op := &Operator{MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL, Model: "test"}, Home: t.TempDir(), Typed: true}
	sk := skillFor(t, Trigger{Kind: "tick"}, "routineblick")
	run := func() {
		t.Helper()
		if _, err := op.Run(context.Background(), sk, Trigger{Kind: "tick"}); err != nil {
			t.Fatal(err)
		}
	}
	shown := func() string { return diagShown(*asked) }
	run()
	if !strings.Contains(shown(), "watchdog") || strings.Contains(shown(), "Schon gemeldet") {
		t.Fatalf("the first look shows everything: %s", shown())
	}

	replies["laib_diag"] = "[" + restoreTest + "," + watchdog + "," + disk + "]"
	run()
	if got := shown(); !strings.Contains(got, "/var hat") || strings.Contains(got, "Wächter") || !strings.Contains(got, "2 Befunde (restore-test, watchdog)") {
		t.Fatalf("want the disk and a count of the rest: %s", got)
	}
	if n := countPrefix(*calls, "laib_repair"); n != 1 {
		t.Fatalf("the open repair was filed again: %d", n)
	}

	replies["laib_diag"] = "[" + watchdog + "," + disk + "]"
	run()
	if got := shown(); !strings.Contains(got, "nicht mehr da: restore-test") {
		t.Fatalf("a finding that went away is news: %s", got)
	}
	replies["laib_diag"] = "[" + restoreTest + "," + watchdog + "," + disk + "]"
	run()
	if n := countPrefix(*calls, "laib_repair"); n != 2 {
		t.Fatalf("a finding back after it went away is filed again: %d", n)
	}
}

// Red is the incident session's: the look files no repair for it. A repair
// the kernel refuses is said once in the note and not retried each look.
func TestRedIsNotTheLooksAndARefusedRepairIsSaidOnce(t *testing.T) {
	red := `{"severity":"rot","probe":"unit","service":"whoami","detail":"whoami.service ist failed","repair":["restart","whoami"]}`
	replies := map[string]string{"laib_diag": "[" + red + "," + restoreTest + "]", "laib_repair": "Budget: repair ist der Fähigkeit routineblick nicht gewährt"}
	mcp, calls := fakeMCP(t, replies)
	llm, _ := recLLM(t, []Message{{Role: "assistant", Content: "Eins ist rot."}})
	op := &Operator{MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL, Model: "test"}, Home: t.TempDir(), Typed: true}
	sk := skillFor(t, Trigger{Kind: "tick"}, "routineblick")
	s, _ := op.Run(context.Background(), sk, Trigger{Kind: "tick"})
	if got := strings.Join(*calls, "\n"); strings.Contains(got, `"probe":"unit"`) || countPrefix(*calls, "laib_repair") != 1 {
		t.Fatalf("want only the restore test's repair: %s", got)
	}
	if !strings.Contains(s.Note, "ließ sich nicht vorschlagen") {
		t.Fatalf("the refusal belongs in the note: %s", s.Note)
	}
	replies["laib_diag"] = "[" + red + "," + restoreTest + `,{"severity":"info","probe":"drift","detail":"nicht verwaltet: x"}]`
	op.Run(context.Background(), sk, Trigger{Kind: "tick"})
	if n := countPrefix(*calls, "laib_repair"); n != 1 {
		t.Fatalf("a refused repair was retried: %d", n)
	}
}

// A session that failed is no reason to stay quiet: after a model that did
// not answer (here the morning report's), the look asks it again every tick
// instead of skipping, and the heartbeat stops until one answers — so
// diag's "stumm" still says it, as before #543.
func TestAFailedSessionIsNotSkippedNorABeat(t *testing.T) {
	replies := map[string]string{"laib_diag": "[" + watchdog + "]"}
	mcp, _ := fakeMCP(t, replies)
	llm, asked := recLLM(t, []Message{{Role: "assistant", Content: "Gelb: kein Wächter."}})
	op := &Operator{MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL, Model: "test"}, Home: t.TempDir(), Typed: true}
	look := skillFor(t, Trigger{Kind: "tick"}, "routineblick")
	op.Run(context.Background(), look, Trigger{Kind: "tick"})
	op.LLM.URL = "http://127.0.0.1:1" // the model goes away
	op.Looked.Store(0)
	if _, err := op.Run(context.Background(), skillFor(t, Trigger{Kind: "report"}, ""), Trigger{Kind: "report"}); err == nil {
		t.Fatal("the report without a model is an error")
	}
	if s, err := op.Run(context.Background(), look, Trigger{Kind: "tick"}); err == nil || s.Outcome == "unchanged" {
		t.Fatalf("after a failure, unchanged findings hid a model that does not answer: %+v", s)
	}
	if op.Looked.Load() != 0 {
		t.Fatal("a failed session beat the heartbeat")
	}
	op.LLM.URL = llm.URL
	n := len(*asked)
	if s, _ := op.Run(context.Background(), look, Trigger{Kind: "tick"}); s.Outcome == "unchanged" || len(*asked) == n || op.Looked.Load() == 0 {
		t.Fatalf("the model is back: the look runs and beats: %+v", s)
	}
	if s, _ := op.Run(context.Background(), look, Trigger{Kind: "tick"}); s.Outcome != "unchanged" {
		t.Fatalf("and then rests again: %+v", s)
	}
}

// The morning report: a yellow the last report named is a count, not a
// paragraph; red is named every morning until it is gone.
func TestTheReportNamesAnOwnersItemOnce(t *testing.T) {
	red := `{"severity":"rot","probe":"backup","detail":"die letzte Sicherung ist fehlgeschlagen","repair":["backup"]}`
	replies := map[string]string{"laib_diag": "[" + red + "," + watchdog + "]"}
	mcp, calls := fakeMCP(t, replies)
	llm, asked := recLLM(t, []Message{
		{Role: "assistant", ToolCalls: []ToolCall{tcall("1", "laib_diag", `{}`)}},
		{Role: "assistant", ToolCalls: []ToolCall{tcall("2", "laib_note", `{"kind":"report","text":"Bericht"}`)}},
		{Role: "assistant", Content: "fertig"},
	})
	op := &Operator{MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL, Model: "test"}, Home: t.TempDir(), Typed: true}
	sk := skillFor(t, Trigger{Kind: "report"}, "morgenbericht")
	for range 2 {
		if s, err := op.Run(context.Background(), sk, Trigger{Kind: "report"}); err != nil || s.Outcome == "unchanged" {
			t.Fatalf("the report always runs: %+v %v", s, err)
		}
	}
	got := diagShown(*asked)
	if !strings.Contains(got, "fehlgeschlagen") || strings.Contains(got, "Wächter") || !strings.Contains(got, "1 Befund (watchdog)") {
		t.Fatalf("want the red in full and the watchdog as a count: %s", got)
	}
	if countPrefix(*calls, "laib_repair") != 0 {
		t.Fatal("the report has no repair tool and files none")
	}
}
