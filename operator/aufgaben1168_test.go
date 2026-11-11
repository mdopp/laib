package operator

import (
	"context"
	"strings"
	"testing"
)

// #1168: a finding whose status file asks for a person's task becomes that
// task in the look, in code, before anything else: one add per ask, with
// its fertig_wenn on the finding's topic; once diag covers it, it asks no
// more. A refused one is not asked again while the finding stays.
func TestTheLookAddsTheTaskAStatusFindingAsksFor(t *testing.T) {
	ask := `{"severity":"info","probe":"status","service":"vaultwarden","detail":"ein Konto ohne zweiten Faktor","topic":"zweiter-faktor.anna",` +
		`"neue_aufgabe":{"id":0,"titel":"Zweiten Faktor einrichten","warum":"w","seit":"","von":"","fuer":"person:anna","weg":{"seite":"/einrichten/passwoerter"}}}`
	replies := map[string]string{"laib_diag": "[" + ask + "]"}
	mcp, calls := fakeMCP(t, replies)
	llm, _ := recLLM(t, []Message{{Role: "assistant", ToolCalls: []ToolCall{tcall("1", "laib_note", `{"kind":"watch","text":"gesehen"}`)}}, {Role: "assistant", Content: "fertig"}})
	op := &Operator{Skills: "../skills", MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL, Model: "test"}, Home: t.TempDir(), Typed: true}
	sk := skillFor(t, Trigger{Kind: "tick"}, "routineblick")
	if _, err := op.Run(context.Background(), sk, Trigger{Kind: "tick"}); err != nil {
		t.Fatal(err)
	}
	want := `laib_aufgabe {"action":"add","fertig_wenn":"status zweiter-faktor.anna","fuer":"person:anna","titel":"Zweiten Faktor einrichten","warum":"w","weg":"/einrichten/passwoerter"}`
	if n := countPrefix(*calls, "laib_aufgabe"); n != 1 || !strings.Contains(strings.Join(*calls, "\n"), want) {
		t.Fatalf("one add, for the person, closed by the finding's topic: %v", *calls)
	}
	// the next look: diag covers it with the open task and asks no more
	replies["laib_diag"] = `[{"severity":"info","probe":"status","service":"vaultwarden","detail":"ein Konto ohne zweiten Faktor (→ Aufgabe #9 für die Person)","topic":"zweiter-faktor.anna","aufgabe":9}]`
	op.Run(context.Background(), sk, Trigger{Kind: "tick"})
	if n := countPrefix(*calls, "laib_aufgabe"); n != 1 {
		t.Fatalf("a covered finding was added again: %v", *calls)
	}
	// Laib refuses an ask (a person who is no login): asked once, not every look
	replies["laib_diag"] = "[" + strings.Replace(ask, "person:anna", "person:Anna Groß", 1) + "]"
	replies["laib_aufgabe"] = "Budget: refused"
	op.Run(context.Background(), sk, Trigger{Kind: "tick"})
	op.Run(context.Background(), sk, Trigger{Kind: "tick"})
	if n := countPrefix(*calls, "laib_aufgabe"); n != 2 {
		t.Fatalf("a refused ask is asked once: %v", *calls)
	}
}
