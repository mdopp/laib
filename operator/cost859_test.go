package operator

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// #859: nothing counted tokens; #550 began with 4 million a day for „all
// green“ and nobody could say today's number. A session now carries what
// the server says its replies cost, and the day's sum is on /ready (the
// Box page's Operator section) and survives a restart.
func TestASessionCountsItsTokensAndTheDaySumsThem(t *testing.T) {
	var asked map[string]any
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		json.NewDecoder(r.Body).Decode(&asked)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Gelb: kein Wächter.\"},\"finish_reason\":\"stop\"}]}\n\n")
		io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":1200,\"completion_tokens\":34}}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(llm.Close)
	mcp, _ := fakeMCP(t, map[string]string{"laib_diag": "[" + watchdog + "]"})
	home := t.TempDir()
	op := &Operator{Skills: "../skills", MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL, Model: "test"}, Home: home, Typed: true}
	s, err := op.Run(context.Background(), skillFor(t, Trigger{Kind: "tick"}, "routineblick"), Trigger{Kind: "tick"})
	if err != nil || s.In != 1200 || s.Out != 34 || s.ModelMs < 1 || !strings.Contains(s.Line(), "1200+34 Token") {
		t.Fatalf("the session does not carry its tokens: %v %+v %s", err, s, s.Line())
	}
	if so, _ := asked["stream_options"].(map[string]any); so["include_usage"] != true {
		t.Fatalf("the request does not ask for the usage: %v", asked)
	}
	m := op.Work().Model
	if m == nil || m.Day != time.Now().Format(time.DateOnly) || m.Sessions != 1 || m.In != 1200 || m.Out != 34 {
		t.Fatalf("the day's sum is not on /ready: %+v", m)
	}
	again := &Operator{Home: home}
	again.Recount(time.Now())
	if m := again.Work().Model; m == nil || m.Sessions != 1 || m.In != 1200 {
		t.Fatalf("a restart lost the day's sum: %+v", m)
	}
}

// #859: a session that ended in error waited in the queue and ran again
// every minute for a day. A failed replay now backs off: 2, 4, 8 minutes …
func TestAFailedReplayBacksOff(t *testing.T) {
	asked := 0
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/model-lease":
			io.WriteString(w, household)
		case "/v1/models":
			io.WriteString(w, loaded)
		default:
			asked++
			http.Error(w, "slot unavailable", http.StatusServiceUnavailable)
		}
	}))
	t.Cleanup(llm.Close)
	mcp, _ := fakeMCP(t, nil)
	clock := time.Date(2026, 10, 30, 9, 0, 0, 0, time.Local)
	op := &Operator{Skills: "../skills", MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL + "/v1", Model: "qwen"}, Home: t.TempDir(), Now: func() time.Time { return clock }}
	vorfall := skillFor(t, Trigger{Kind: "event"}, "")
	r := &Runner{Op: op}
	r.LoadLast()
	if ss := r.Event(context.Background(), []Skill{vorfall}, Trigger{Kind: "event", Service: "bote", Detail: "bote: unit failed"}); ss[0].Outcome != "error" || asked != 1 {
		t.Fatalf("%+v %d", ss, asked)
	}
	want := map[int]int{1: 2, 2: 2, 3: 3, 4: 3, 5: 3, 6: 3, 7: 4} // minute → model calls so far
	for min := 1; min <= 7; min++ {
		clock = time.Date(2026, 10, 30, 9, min, 0, 0, time.Local)
		r.Step(context.Background(), clock, []Skill{vorfall})
		if asked != want[min] {
			t.Fatalf("minute %d: %d model calls, want %d", min, asked, want[min])
		}
	}
}

// #859: the updates skill read the registry, the inbox and diag before it
// asked whether the model was free. A slot that needs the model asks first;
// one whose script works alone (sicherung) runs while the card is lent.
func TestASlotThatNeedsTheModelAsksBeforeItsScriptWorks(t *testing.T) {
	lease, catalogue := werkstatt, loaded
	llm, asked := fakeLease(t, &lease, &catalogue, []Message{{Role: "assistant", Content: "fertig"}})
	mcp, calls := fakeMCP(t, map[string]string{"laib_backup": "grün · ok", "laib_journal": "[]"})
	op := &Operator{Skills: "../skills", MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL + "/v1", Model: "qwen"}, Home: t.TempDir()}
	for _, name := range []string{"durchsicht", "aktualisierung", "nutzung"} {
		s, err := op.Run(context.Background(), skillFor(t, Trigger{Kind: "slot"}, name), Trigger{Kind: "slot"})
		if err != nil || s.Outcome != "skipped" || !strings.Contains(s.Why, "werkstatt") {
			t.Fatalf("%s: %v %+v", name, err, s)
		}
	}
	if len(*calls) != 0 || *asked != 0 {
		t.Fatalf("a slot that waits for the card worked first: %v", *calls)
	}
	if s, _ := op.Run(context.Background(), skillFor(t, Trigger{Kind: "slot"}, "sicherung"), Trigger{Kind: "slot"}); s.Outcome == "skipped" || countPrefix(*calls, "laib_backup") != 1 {
		t.Fatalf("the backup sample needs no model and waited: %+v", s)
	}
}
