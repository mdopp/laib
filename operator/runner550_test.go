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

// #550: seven red sessions in 40 h, about 100 s and 12 calls each, and the
// log said only "red". A red session's line names what was red, why the
// session ended red and what came of it.
func TestARedSessionsLineSaysWhatWasRedAndWhatCameOfIt(t *testing.T) {
	mcp, _ := fakeMCP(t, map[string]string{"laib_propose": "Vorschlag #12 angelegt"})
	llm := fakeLLM(t, []Message{
		{Role: "assistant", ToolCalls: []ToolCall{tcall("1", "laib_propose", `{"reason":"jellyfin neu","service":"jellyfin","key":"IMAGE","value":"j:1.2","finding":"unit jellyfin","evidence":"unit failed","undo":"IMAGE zurück"}`)}},
		{Role: "assistant", ToolCalls: []ToolCall{tcall("2", "laib_diag", `{}`)}}, // loops until the limit
	})
	op := &Operator{MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL, Model: "test"}, Home: t.TempDir(), MaxCalls: 4}
	tr := Trigger{Kind: "event", Service: "jellyfin", Detail: "jellyfin: apply: unit failed"}
	s, err := op.Run(context.Background(), skillFor(t, tr, ""), tr)
	if err != nil {
		t.Fatal(err)
	}
	line := s.Line()
	for _, want := range []string{"session vorfall/jellyfin: red", "jellyfin: apply: unit failed", "4 Aufrufen", "→ vorgeschlagen #12"} {
		if !strings.Contains(line, want) {
			t.Errorf("the line lacks %q: %s", want, line)
		}
	}
	green := Session{Skill: "routineblick", Outcome: "green", Calls: []string{"laib_note x"}, Duration: 900}
	if l := green.Line(); l != "session routineblick/: green, 1 calls, 900ms" {
		t.Errorf("a green line changed: %s", l)
	}
}
