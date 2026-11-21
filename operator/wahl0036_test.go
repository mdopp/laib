package operator

import (
	"context"
	"testing"
)

// ADR 0036 (owner, 2026-11-21): a choice card sent back with „nachdenken“
// reaches the Operator as a red line of the human who tapped it, opens an
// incident, and its session has three times the steps on the same model.
func TestACardSentBackOpensAnIncidentWithMoreSteps(t *testing.T) {
	if !Opens("wahl", "human:mdopp", "red", "nachdenken: Karte #7 zurückgegeben, es soll gründlicher nachgesehen werden: Vault startet nicht.") {
		t.Fatal("a card sent back opens no incident")
	}
	if Opens("wahl", "ops", "red", "nachdenken: x") || Opens("wahl", "human:mdopp", "info", "#7 wartet auf die Wahl zwischen 3 Wegen") {
		t.Fatal("the Operator's own card or a green line opens an incident")
	}
	steps := func(detail string) int {
		mcp, _ := fakeMCP(t, nil)
		llm := fakeLLM(t, []Message{{Role: "assistant", ToolCalls: []ToolCall{call("x", "laib_diag", "")}}}) // loops forever
		op := &Operator{Skills: "../skills", MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL, Model: "test"}, Home: t.TempDir(), MaxCalls: 4}
		tr := Trigger{Kind: "event", Detail: detail, Aided: true}
		s, err := op.Run(context.Background(), skillFor(t, tr, ""), tr)
		if err != nil {
			t.Fatal(err)
		}
		return len(s.Calls)
	}
	if plain, back := steps("whoami: restart: rot"), steps(": wahl: nachdenken: Karte #7 zurückgegeben"); plain != 4 || back != 12 {
		t.Fatalf("an ordinary incident has %d steps, a card sent back %d; want 4 and 12", plain, back)
	}
}
