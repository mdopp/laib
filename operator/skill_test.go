package operator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// skillMCP is a laib MCP that records, per call, the skill it was made in.
func skillMCP(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		reply := func(res any) {
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": res})
		}
		switch req.Method {
		case "tools/list":
			var tools []map[string]any
			for _, n := range []string{"laib_status", "laib_diag", "laib_restart", "laib_note", "laib_context"} {
				tools = append(tools, map[string]any{"name": n, "inputSchema": map[string]any{"type": "object"}})
			}
			reply(map[string]any{"tools": tools})
		case "tools/call":
			var p struct {
				Name string `json:"name"`
			}
			json.Unmarshal(req.Params, &p)
			mu.Lock()
			calls = append(calls, r.Header.Get("X-Laib-Skill")+" "+p.Name)
			mu.Unlock()
			reply(map[string]any{"content": []map[string]string{{"type": "text", "text": "ok"}}})
		default:
			reply(map[string]any{})
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func noteOnly(t *testing.T) *httptest.Server {
	return fakeLLM(t, []Message{
		{Role: "assistant", ToolCalls: []ToolCall{tcall("1", "note", `{"kind":"watch","text":"Der Garten ist grün."}`)}},
		{Role: "assistant", Content: "fertig"},
	})
}

var garden = Skill{Name: "gartenblick", Source: "state", Status: "active", Description: "Schaut in den Garten.",
	Trigger: SkillTrigger{Every: "5m"}, Tools: []string{"status", "note"}, Granted: []string{"status", "note"},
	Output: "note", Prompt: "Prüfe status. Dann note."}

// ADR 0023: a paused skill never runs — no session, no model call, no
// tool call — however long it has been due.
func TestAPausedSkillNeverRuns(t *testing.T) {
	mcp, calls := skillMCP(t)
	op := &Operator{MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: noteOnly(t).URL, Model: "test"}, Home: t.TempDir(), Typed: true}
	r := &Runner{Op: op, Last: map[string]time.Time{}}
	paused := garden
	paused.Status = "paused"
	now := time.Now()
	r.Last[paused.Name] = now.Add(-24 * time.Hour)
	if got := r.Step(context.Background(), now, []Skill{paused}); len(got) != 0 || len(*calls) != 0 {
		t.Fatalf("a paused skill ran: %+v, calls %v", got, *calls)
	}
	if got := r.Event(context.Background(), []Skill{{Name: "vorfall", Status: "paused", Trigger: SkillTrigger{Event: "red"}}}, Trigger{Kind: "event", Service: "whoami"}); len(got) != 0 {
		t.Fatalf("a paused event skill ran: %+v", got)
	}
}

// A skill file added to the state repo runs on its trigger, in its own
// name: every call of the session carries X-Laib-Skill, so the kernel can
// narrow it. Not before its interval, and not twice in one.
func TestAnAddedSkillRunsOnItsTrigger(t *testing.T) {
	mcp, calls := skillMCP(t)
	op := &Operator{MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: noteOnly(t).URL, Model: "test"}, Home: t.TempDir(), Typed: true}
	r := &Runner{Op: op, Last: map[string]time.Time{}}
	start := time.Date(2026, 10, 16, 9, 0, 0, 0, time.Local)
	if got := r.Step(context.Background(), start, []Skill{garden}); len(got) != 0 {
		t.Fatalf("ran at first sight, before one interval: %+v", got)
	}
	got := r.Step(context.Background(), start.Add(5*time.Minute), []Skill{garden})
	if len(got) != 1 || got[0].Skill != "gartenblick" || got[0].Outcome != "green" || !strings.Contains(got[0].Note, "Garten") {
		t.Fatalf("%+v", got)
	}
	for _, c := range *calls {
		if c != "gartenblick laib_note" && c != " laib_context" { // the starter bundle is read as the hand, before the session
			t.Fatalf("a call not made in the skill's name: %q in %v", c, *calls)
		}
	}
	if !strings.Contains(strings.Join(*calls, "|"), "gartenblick laib_note") {
		t.Fatalf("the session's note is missing: %v", *calls)
	}
	if again := r.Step(context.Background(), start.Add(6*time.Minute), []Skill{garden}); len(again) != 0 {
		t.Fatalf("ran twice in one interval: %+v", again)
	}
}

// The schedules: a daily slot once a day, a weekly one once a week, an
// event never by the clock.
func TestSkillTriggersAreDueOncePerSlot(t *testing.T) {
	mon := time.Date(2026, 10, 12, 8, 30, 0, 0, time.Local) // a Monday
	for _, tc := range []struct {
		trig      SkillTrigger
		now, last time.Time
		want      bool
	}{
		{SkillTrigger{Daily: "07:00"}, mon, time.Time{}, true},
		{SkillTrigger{Daily: "07:00"}, mon, mon.Add(-time.Hour), false},
		{SkillTrigger{Daily: "07:00"}, mon.Add(-2 * time.Hour), time.Time{}, false},
		{SkillTrigger{Daily: "${REPORT_AT}"}, mon, mon.Add(-24 * time.Hour), true},
		{SkillTrigger{Weekly: "Mo 08:00"}, mon, mon.Add(-7 * 24 * time.Hour), true},
		{SkillTrigger{Weekly: "Mo 08:00"}, mon.Add(48 * time.Hour), mon, false},
		{SkillTrigger{Weekly: "Fr 18:00"}, mon, mon.Add(-24 * time.Hour), false},
		{SkillTrigger{Every: "${TICK}"}, mon, mon.Add(-10 * time.Minute), true},
		{SkillTrigger{Every: "${TICK}"}, mon, mon.Add(-9 * time.Minute), false},
		{SkillTrigger{Event: "red"}, mon, time.Time{}, false},
	} {
		if got := Due(tc.trig, tc.now, tc.last, "10m", "07:00"); got != tc.want {
			t.Errorf("%+v now %s last %s: %v, want %v", tc.trig, tc.now.Format("Mon 15:04"), tc.last.Format("Mon 02. 15:04"), got, tc.want)
		}
	}
}
