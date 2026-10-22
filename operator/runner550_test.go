package operator

import (
	"context"
	"strings"
	"testing"
)

// #550: on 2026-10-22 03:11 the same note stood five times in the journal
// within 14 s. The runner carried out every laib_note a model asked for, in
// one reply or over passes; a session writes its note once.
func TestASessionWritesItsNoteOnce(t *testing.T) {
	note := `{"kind":"incident","text":"whoami war rot, Neustart um 03:11, jetzt grün."}`
	mcp, calls := fakeMCP(t, nil)
	llm, asked := recLLM(t, []Message{
		{Role: "assistant", ToolCalls: []ToolCall{tcall("1", "laib_note", note), tcall("2", "laib_note", note)}},
		{Role: "assistant", ToolCalls: []ToolCall{tcall("3", "laib_note", note)}},
		{Role: "assistant", ToolCalls: []ToolCall{tcall("4", "laib_note", note)}},
		{Role: "assistant", Content: "fertig"},
	})
	op := &Operator{MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL, Model: "test"}, Home: t.TempDir()}
	tr := Trigger{Kind: "event", Service: "whoami", Detail: "whoami: health red"}
	s, err := op.Run(context.Background(), skillFor(t, tr, ""), tr)
	if err != nil {
		t.Fatal(err)
	}
	if n := countPrefix(*calls, "laib_note"); n != 1 {
		t.Fatalf("the note was written %d times:\n%s", n, strings.Join(*calls, "\n"))
	}
	if s.Outcome != "green" || countPrefix(s.Calls, "laib_note") != 1 {
		t.Fatalf("%+v", s)
	}
	last := (*asked)[len(*asked)-1]
	if !strings.Contains(last[len(last)-1].Content, "Notiz") {
		t.Fatalf("the model was not told its note stands: %+v", last[len(last)-1])
	}
}
