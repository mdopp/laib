package operator

import (
	"context"
	"strings"
	"testing"
)

// #43 (1): a small model often writes the tool call as text. That is not a
// green session: the Operator asks once for a real tool call, then ends
// red with a note that says so — and never treats the text as done work.
func TestATextToolCallIsNotASuccess(t *testing.T) {
	mcp, calls := fakeMCP(t, map[string]string{"laib_status": "rot: whoami"})
	llm := fakeLLM(t, []Message{
		{Role: "assistant", Content: "laib_restart whoami"},
		{Role: "assistant", Content: `{"name":"laib_restart","arguments":{"args":"whoami"}}`},
	})
	op := &Operator{Skills: "../skills", MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL, Model: "test"}, Home: t.TempDir()}
	s, err := op.Run(context.Background(), skillFor(t, Trigger{Kind: "event", Service: "whoami", Detail: "red"}, ""), Trigger{Kind: "event", Service: "whoami", Detail: "red"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Outcome != "red" {
		t.Fatalf("a text tool call must end red, got %s (note %q)", s.Outcome, s.Note)
	}
	joined := strings.Join(*calls, "\n")
	for _, c := range *calls {
		if strings.HasPrefix(c, "laib_restart") {
			t.Fatal("the text was executed as a tool call")
		}
	}
	if !strings.Contains(joined, "laib_note") || !strings.Contains(strings.ToLower(s.Note), "text") {
		t.Fatalf("the note must say the model answered with text instead of a tool call: %q", s.Note)
	}
}

// #43 / #53: a tool call whose arguments are not a string map is rejected,
// never executed with empty arguments.
func TestInvalidToolArgumentsAreRejectedNotExecuted(t *testing.T) {
	mcp, calls := fakeMCP(t, nil)
	bad := call("1", "laib_restart", "")
	bad.Function.Arguments = `[1,2,3]`
	llm := fakeLLM(t, []Message{
		{Role: "assistant", ToolCalls: []ToolCall{bad}},
		{Role: "assistant", ToolCalls: []ToolCall{call("2", "laib_note", "--kind watch \"nichts\"")}},
		{Role: "assistant", Content: "fertig"},
	})
	op := &Operator{Skills: "../skills", MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL, Model: "test"}, Home: t.TempDir()}
	if _, err := op.Run(context.Background(), skillFor(t, Trigger{Kind: "event", Service: "whoami"}, ""), Trigger{Kind: "event", Service: "whoami"}); err != nil {
		t.Fatal(err)
	}
	for _, c := range *calls {
		if strings.HasPrefix(c, "laib_restart") {
			t.Fatalf("a call with invalid arguments ran: %v", *calls)
		}
	}
}

// #43 (3): the prompt names only tools the hand really has — generated
// from the tool list, not written by hand.
func TestPromptNamesOnlyToolsTheHandHas(t *testing.T) {
	defs := Defs([]Tool{{Name: "laib_status"}, {Name: "laib_note"}, {Name: "laib_escalate"}})
	sys := SystemPrompt(defs)
	if strings.Contains(sys, "laib_diag") || strings.Contains(sys, "laib_restart") {
		t.Fatalf("the prompt names tools the hand does not have:\n%s", sys)
	}
	for _, want := range []string{"laib_status", "laib_note", "laib_escalate"} {
		if !strings.Contains(sys, want) {
			t.Fatalf("the prompt lacks %s:\n%s", want, sys)
		}
	}
}

// #43 (2): the context bundle is budgeted — the latest journal lines
// survive, the prompt never explodes past a small model's window.
func TestContextIsBudgeted(t *testing.T) {
	var huge strings.Builder
	huge.WriteString("# Laib context\n## Journal\n")
	for i := 0; i < 5000; i++ {
		huge.WriteString("- 12:00 ops note whoami info Routineblick nichts zu tun, Zeile " + strconvQuote(strings.Repeat("x", 40)) + "\n")
	}
	huge.WriteString("- 23:59 laib ready red boot: 1 rot LETZTE ZEILE\n")
	op := &Operator{Skills: "../skills"}
	p := op.prompt(Skill{Prompt: "Routineblick."}, Trigger{Kind: "tick"}, huge.String())
	if len(p) > 12000 {
		t.Fatalf("prompt is %d chars; a 32k model cannot take that", len(p))
	}
	if !strings.Contains(p, "LETZTE ZEILE") {
		t.Fatal("the newest journal line must survive the budget")
	}
}
