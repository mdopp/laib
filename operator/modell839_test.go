package operator

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// menus is a transport that keeps the tool names of every chat request.
type menus struct{ seen *[][]string }

func (m menus) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method == http.MethodPost {
		b, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(b))
		var req struct{ Tools []ToolDef }
		json.Unmarshal(b, &req)
		var names []string
		for _, t := range req.Tools {
			names = append(names, t.Function.Name)
		}
		*m.seen = append(*m.seen, names)
	}
	return http.DefaultTransport.RoundTrip(r)
}

// #839, the owner: „ich möchte, dass wir die Problemlösungskompetenzen eines
// LLMs behalten.“ Each skill says where the model decides. The incident and
// the looks keep the agentic loop with every granted tool; a script only
// prepares a judgement or, for lehre as before, works alone.
func TestEachSkillSaysWhereTheModelDecides(t *testing.T) {
	for name, want := range map[string]string{"vorfall": "frei", "routineblick": "frei", "morgenbericht": "frei", "sicherung": "frei",
		"durchsicht": "urteil", "nutzung": "urteil", "aktualisierung": "urteil", "lehre": "nein"} {
		sk := skillFor(t, Trigger{}, name)
		if got := cmpOr(sk.Modell, "frei"); got != want {
			t.Errorf("%s: modell %s, want %s", name, got, want)
		}
		if want == "frei" && name == "vorfall" && sk.Run != "" {
			t.Errorf("vorfall's loop is the model's, no script ahead of it: %+v", sk)
		}
	}
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// The incident stays the model's (#792, #839): it is offered every granted
// tool, picks one, reads the answer, tries a next step when the first one
// fails and decides to escalate — the loop, not a pipeline.
func TestVorfallLetsTheModelPickToolsInALoop(t *testing.T) {
	mcp, calls := fakeMCP(t, map[string]string{"laib_logs": "bind: address already in use :8080", "laib_restart": "Budget erschöpft für whoami"})
	llm := fakeLLM(t, []Message{
		{Role: "assistant", ToolCalls: []ToolCall{call("1", "laib_logs", "whoami")}},
		{Role: "assistant", ToolCalls: []ToolCall{call("2", "laib_inspect", "whoami")}},
		{Role: "assistant", ToolCalls: []ToolCall{call("3", "laib_restart", "whoami")}},
		{Role: "assistant", ToolCalls: []ToolCall{call("4", "laib_escalate", "whoami Port 8080 belegt, Neustart im Budget nicht möglich")}},
		{Role: "assistant", ToolCalls: []ToolCall{call("5", "laib_note", `--kind incident "whoami: Port belegt; eskaliert."`)}},
		{Role: "assistant", Content: "fertig"},
	})
	var seen [][]string
	op := &Operator{Skills: "../skills", MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL, Model: "test", Client: &http.Client{Transport: menus{&seen}}}, Home: t.TempDir()}
	tr := Trigger{Kind: "event", Service: "whoami", Detail: "health red"}
	s, err := op.Run(context.Background(), skillFor(t, tr, "vorfall"), tr)
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 6 {
		t.Fatalf("the model was asked %d times, want a pass per step: %v", len(seen), seen)
	}
	for _, tool := range []string{"laib_diag", "laib_logs", "laib_inspect", "laib_restart", "laib_propose", "laib_escalate", "laib_note", "laib_look", "laib_plan", "laib_apply"} {
		if !slices.Contains(seen[0], tool) {
			t.Errorf("the incident's model is not offered %s: %v", tool, seen[0])
		}
	}
	if s.Outcome != "escalated" || len(s.Calls) != 5 || len(s.Script) != 0 || !strings.Contains(strings.Join(*calls, "\n"), "laib_escalate") {
		t.Fatalf("the model's own steps did not run as it chose them: %+v", s)
	}
}

// A judgement (modell: urteil) gets what the script prepared and only the
// tools that write its verdict: durchsicht's model is offered wish and note,
// not the logs its script already read.
func TestAJudgementIsOfferedOnlyItsVerdicts(t *testing.T) {
	day := kraftwerkDay(40)
	mcp, _ := reviewMCP(t, &day)
	llm := fakeLLM(t, []Message{{Role: "assistant", Content: "AdGuard: 40 Timeouts."}})
	var seen [][]string
	op := &Operator{Skills: "../skills", MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL, Model: "test", Client: &http.Client{Transport: menus{&seen}}}, Home: t.TempDir(), Typed: true}
	if _, err := op.Run(context.Background(), skillFor(t, Trigger{Kind: "tick"}, "durchsicht"), Trigger{Kind: "tick"}); err != nil {
		t.Fatal(err)
	}
	if len(seen) == 0 || !slices.Equal(slices.Sorted(slices.Values(seen[0])), []string{"laib_note", "laib_wish"}) {
		t.Fatalf("a judgement's menu: %v", seen)
	}
}
