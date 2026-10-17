package operator

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// #286: the interface follows the model — a window above 32k is a capable
// model and gets the full surface; OPERATOR_INTERFACE still decides when set.
func TestTheInterfaceFollowsTheWindow(t *testing.T) {
	for _, c := range []struct {
		env    string
		window int
		typed  bool
	}{{"", 131072, false}, {"", 32768, true}, {"", 0, true}, {"typed", 131072, true}, {"cli", 0, false}} {
		if got := SmallModel(c.env, c.window); got != c.typed {
			t.Errorf("OPERATOR_INTERFACE=%q window %d: small=%v, want %v", c.env, c.window, got, c.typed)
		}
	}
}

// #286: a skill's `more` tools are for a capable model only.
func TestACapableModelGetsTheSkillsWiderTools(t *testing.T) {
	sk := skillFor(t, Trigger{Kind: "event"}, "vorfall")
	if len(sk.More) == 0 {
		t.Fatal("vorfall names no tools for a capable model")
	}
	for _, typed := range []bool{true, false} {
		mcp, _ := fakeMCP(t, nil)
		var offered []ToolDef
		llm := fakeLLM(t, []Message{{Role: "assistant", ToolCalls: []ToolCall{tcall("1", "laib_note", `{"kind":"incident","text":"x"}`)}}, {Role: "assistant", Content: "fertig"}})
		op := &Operator{MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL, Model: "test"}, Home: t.TempDir(), Typed: typed}
		op.LLM.Client = &http.Client{Transport: tools(func(d []ToolDef) { offered = d })}
		tr := Trigger{Kind: "event", Service: "whoami"}
		if _, err := op.Run(context.Background(), sk, tr); err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, d := range offered {
			names = append(names, d.Function.Name)
		}
		has := strings.Contains(strings.Join(names, ","), "laib_look")
		if has == typed {
			t.Fatalf("typed=%v: menu %v", typed, names)
		}
	}
}

// #286: a capable model's answer that starts with a tool's name is no text call.
func TestACapableModelsAnswerIsNotATextCall(t *testing.T) {
	mcp, _ := fakeMCP(t, nil)
	llm := fakeLLM(t, []Message{{Role: "assistant", Content: "restart war nicht nötig, whoami ist seit 03:14 grün."}})
	op := &Operator{MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL, Model: "test"}, Home: t.TempDir(), Typed: false}
	tr := Trigger{Kind: "event", Service: "whoami"}
	s, err := op.Run(context.Background(), skillFor(t, tr, ""), tr)
	if err != nil || s.Outcome != "green" || !strings.Contains(s.Note, "seit 03:14") {
		t.Fatalf("%+v %v", s, err)
	}
}
