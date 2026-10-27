package operator

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func tcall(id, name, args string) ToolCall {
	var tc ToolCall
	tc.ID, tc.Type = id, "function"
	tc.Function.Name = name
	tc.Function.Arguments = args
	return tc
}

// #290: the menu is the kernel's typed tools the skill is granted — the
// model calls them with JSON, the kernel sees the same JSON, a tool outside
// the skill is answered to the model and never reaches laib.
func TestStructuredIncidentSessionRunsEndToEnd(t *testing.T) {
	mcp, calls := fakeMCP(t, map[string]string{"laib_logs": "Oct 11 03:12 whoami[1]: bind: address already in use", "laib_restart": "grün: whoami"})
	llm := fakeLLM(t, []Message{
		{Role: "assistant", ToolCalls: []ToolCall{tcall("1", "laib_logs", `{"service":"whoami","since":"30m"}`)}},
		{Role: "assistant", ToolCalls: []ToolCall{tcall("2", "laib_exec", `{"service":"whoami","command":["rm","-rf","/data"]}`)}},
		{Role: "assistant", ToolCalls: []ToolCall{tcall("3", "laib_restart", `{"service":"whoami"}`)}},
		{Role: "assistant", ToolCalls: []ToolCall{tcall("4", "laib_note", `{"kind":"incident","text":"03:12 whoami: Port \"belegt\", Neustart grün."}`)}},
		{Role: "assistant", Content: "fertig"},
	})
	op := &Operator{Skills: "../skills", MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL, Model: "test"}, Home: t.TempDir(), Typed: true}
	tr := Trigger{Kind: "event", Service: "whoami", Detail: "unit whoami.service failed"}
	s, err := op.Run(context.Background(), skillFor(t, tr, ""), tr)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(*calls, "\n")
	for _, want := range []string{`laib_logs {"service":"whoami","since":"30m"}`, `laib_restart {"service":"whoami"}`, `"text":"03:12 whoami: Port \"belegt\", Neustart grün."`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "laib_exec") {
		t.Fatalf("a tool outside the skill reached laib:\n%s", got)
	}
	if s.Outcome != "green" || len(s.Calls) != 3 || !strings.HasPrefix(s.Calls[0], "laib_logs ") || s.Note != `03:12 whoami: Port "belegt", Neustart grün.` {
		t.Fatalf("%+v", s)
	}
}

// #53, #290: the eval runs the structured interface over its incidents.
func TestEvalStructuredIncidents(t *testing.T) {
	incidents, err := LoadIncidents(filepath.Join("..", "werkstatt", "evals", "incidents-typed.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(incidents) < 6 {
		t.Fatalf("%d incidents", len(incidents))
	}
	rep := RunEval(context.Background(), "scripted", incidents, func(ctx context.Context, inc Incident) (Session, error) {
		mcp, _ := fakeMCP(t, inc.Replies)
		srv := fakeLLM(t, inc.Script)
		op := &Operator{Skills: "../skills", MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: srv.URL, Model: "test"}, Home: t.TempDir(), Typed: true}
		if inc.Usage != nil {
			op.Usage = &Usage{URL: fakeUsage(t, inc.Usage).URL, Token: "um-token"}
		}
		return op.Run(ctx, skillFor(t, inc.Trigger, inc.Skill), inc.Trigger)
	})
	if rep.Wrong != 0 || rep.Passed != rep.Total {
		t.Fatalf("%+v", rep)
	}
}
