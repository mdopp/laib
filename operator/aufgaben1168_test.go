package operator

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"
)

// #1168: a finding whose status file asks for a person's task becomes that
// task in the look, in code, before anything else: one add per ask, with
// its fertig_wenn on the finding's topic; once diag covers it, it asks no
// more. A refused one is not asked on every look (#1181): the note names
// the kernel's reason, and a day later it is asked again.
func TestTheLookAddsTheTaskAStatusFindingAsksFor(t *testing.T) {
	ask := `{"severity":"info","probe":"status","service":"vaultwarden","detail":"ein Konto ohne zweiten Faktor","topic":"zweiter-faktor.anna",` +
		`"neue_aufgabe":{"id":0,"titel":"Zweiten Faktor einrichten","warum":"w","seit":"","von":"","fuer":"person:anna","weg":{"seite":"/einrichten/passwoerter"}}}`
	replies := map[string]string{"laib_diag": "[" + ask + "]"}
	mcp, calls := fakeMCP(t, replies)
	llm, _ := recLLM(t, []Message{{Role: "assistant", ToolCalls: []ToolCall{tcall("1", "laib_note", `{"kind":"watch","text":"gesehen"}`)}}, {Role: "assistant", Content: "fertig"}})
	clock := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	op := &Operator{Skills: "../skills", MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL, Model: "test"}, Home: t.TempDir(), Typed: true, Now: func() time.Time { return clock }}
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
	// Laib refuses an ask (the box's policy keeps ops from aufgabe): asked
	// once, not every look, and the note says why — the box, 2026-11-11
	replies["laib_diag"] = "[" + strings.Replace(ask, "person:anna", "person:Anna Groß", 1) + "]"
	replies["laib_aufgabe"] = "Budget: ops may not aufgabe: policy.yml limits it to status, diag"
	op.Run(context.Background(), sk, Trigger{Kind: "tick"})
	clock = clock.Add(time.Hour)
	op.Run(context.Background(), sk, Trigger{Kind: "tick"})
	if n := countPrefix(*calls, "laib_aufgabe"); n != 2 {
		t.Fatalf("a refused ask is asked once: %v", *calls)
	}
	if !slices.ContainsFunc(*calls, func(c string) bool {
		return strings.HasPrefix(c, "laib_note") && strings.Contains(c, "ließ sich nicht anlegen") && strings.Contains(c, "policy.yml limits it")
	}) {
		t.Fatalf("a refusal is silent; the note names it with the kernel's reason: %v", *calls)
	}
	// a day later it is asked again, once
	clock = clock.Add(24 * time.Hour)
	op.Run(context.Background(), sk, Trigger{Kind: "tick"})
	op.Run(context.Background(), sk, Trigger{Kind: "tick"})
	if n := countPrefix(*calls, "laib_aufgabe"); n != 3 {
		t.Fatalf("a refused ask is asked again a day later, once: %v", *calls)
	}
}

// #1181: a policy without aufgabe keeps the look from adding tasks; the
// look does not try: diag's skill finding names the grant (#543).
func TestTheLookAsksNoTaskWithoutTheGrant(t *testing.T) {
	ask := `{"severity":"info","probe":"status","service":"vaultwarden","detail":"d","topic":"t","neue_aufgabe":{"titel":"T","fuer":"person:anna"}}`
	mcp, calls := fakeMCP(t, map[string]string{"laib_diag": "[" + ask + "]"})
	llm, _ := recLLM(t, []Message{{Role: "assistant", ToolCalls: []ToolCall{tcall("1", "laib_note", `{"kind":"watch","text":"gesehen"}`)}}, {Role: "assistant", Content: "fertig"}})
	op := &Operator{Skills: "../skills", MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL, Model: "test"}, Home: t.TempDir(), Typed: true}
	sk := skillFor(t, Trigger{Kind: "tick"}, "routineblick")
	if granted := sk.menu(true); !granted["laib_aufgabe"] {
		t.Fatal("routineblick lists aufgabe among its tools, so diag can say when the policy keeps it")
	}
	sk.Granted = slices.DeleteFunc(sk.Granted, func(g string) bool { return g == "aufgabe" })
	op.Run(context.Background(), sk, Trigger{Kind: "tick"})
	if n := countPrefix(*calls, "laib_aufgabe"); n != 0 {
		t.Fatalf("asked without the grant: %v", *calls)
	}
}
