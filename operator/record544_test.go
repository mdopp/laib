package operator

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

var day = time.Date(2026, 10, 21, 7, 0, 0, 0, time.UTC)

// inboxJSON is laib inbox --all --json as the record reads it.
func inboxJSON(t *testing.T, ps ...past) string {
	t.Helper()
	b, err := json.Marshal(ps)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func opsPast(id int, outcome, why string, age time.Duration) past {
	return past{ID: id, Actor: "ops", Reason: "Vorschlag " + strings.Repeat("x", id%3), Status: "approved", Created: day.Add(-age), Decided: day.Add(-age),
		Files: []string{"services/whoami/values.yml"}, Finding: "unit whoami", Outcome: outcome, RejectReason: why}
}

// #544: every session hears what came of its last proposals, before the
// box's context; the morning report's note ends with the month's rate.
// What a human proposed, what is still open and a free "nicht jetzt" do
// not count.
func TestEverySessionHearsItsRecordAndTheReportItsRate(t *testing.T) {
	record := inboxJSON(t,
		opsPast(9, "effective", "", time.Hour),
		opsPast(8, "effective", "", 2*time.Hour),
		opsPast(7, "rolled-back", "", 3*time.Hour),
		opsPast(6, "rejected", "der falsche Dienst", 4*time.Hour),
		opsPast(5, "later", "nicht jetzt", 5*time.Hour),
		opsPast(4, "", "", 6*time.Hour),
		past{ID: 3, Actor: "human:mdopp", Outcome: "rejected", RejectReason: "nein"},
		opsPast(2, "no-effect", "", 40*24*time.Hour), // older than the month: in the record, not in the rate
	)
	mcp, calls := fakeMCP(t, map[string]string{"laib_inbox": record})
	llm, asked := recLLM(t, []Message{{Role: "assistant", Content: "Nachts war es ruhig."}})
	op := &Operator{Skills: "../skills", MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL, Model: "test"}, Home: t.TempDir(), Typed: true, Now: func() time.Time { return day }}
	s, err := op.Run(context.Background(), skillFor(t, Trigger{Kind: "report"}, "morgenbericht"), Trigger{Kind: "report"})
	if err != nil {
		t.Fatal(err)
	}
	user := (*asked)[0][1].Content
	if !strings.Contains(user, "Deine letzten 5 Vorschläge: 2 wirksam, 1 ohne Wirkung, 1 zurückgenommen, 1 abgelehnt.") || !strings.Contains(user, "#6 „der falsche Dienst“") {
		t.Fatalf("the session does not hear its record:\n%s", user)
	}
	if !strings.Contains(s.Note, "Vorschläge des Operators, 30 Tage: 2 von 4 wirksam (50 %); 2 wirksam, 1 zurückgenommen, 1 abgelehnt.") {
		t.Fatalf("the report lacks the month's rate: %q", s.Note)
	}
	if !slices.ContainsFunc(*calls, func(c string) bool { return c == "laib_inbox --all --limit 0" }) {
		t.Fatalf("the record is read as laib inbox --all --limit 0 (every entry, #579): %v", *calls)
	}
}

func TestNoRecordNoLine(t *testing.T) {
	if r := Record(nil); r != "" {
		t.Fatalf("%q", r)
	}
}

// #544: in a session that reads diag, a model's proposal names the finding
// it answers, what it saw and the way back — or it is not filed. The
// number of what was filed is kept with the session, for its lesson later.
func TestAProposalWithoutItsAnchorIsNotFiled(t *testing.T) {
	mcp, calls := fakeMCP(t, map[string]string{"laib_propose": `{"id":12,"status":"open"}`})
	llm := fakeLLM(t, []Message{
		{Role: "assistant", ToolCalls: []ToolCall{tcall("1", "laib_propose", `{"reason":"zurück auf v1.10","service":"whoami","key":"IMAGE","value":"v1.10"}`)}},
		{Role: "assistant", ToolCalls: []ToolCall{tcall("2", "laib_propose", `{"reason":"zurück auf v1.10","service":"whoami","key":"IMAGE","value":"v1.10","finding":"unit whoami","evidence":"manifest unknown","undo":"laib rollback #12"}`)}},
		{Role: "assistant", ToolCalls: []ToolCall{tcall("3", "laib_note", `{"kind":"incident","text":"whoami: #12 vorgeschlagen"}`)}},
		{Role: "assistant", Content: "fertig"},
	})
	op := &Operator{Skills: "../skills", MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL, Model: "test"}, Home: t.TempDir()}
	tr := Trigger{Kind: "event", Service: "whoami", Detail: "unit failed"}
	s, err := op.Run(context.Background(), skillFor(t, tr, ""), tr)
	if err != nil {
		t.Fatal(err)
	}
	if n := countPrefix(*calls, "laib_propose"); n != 1 || !strings.Contains(strings.Join(*calls, "\n"), `"finding":"unit whoami"`) {
		t.Fatalf("want only the anchored proposal filed: %v", *calls)
	}
	if !slices.Equal(s.Proposed, []int{12}) {
		t.Fatalf("the session does not keep what it filed: %v", s.Proposed)
	}
	// the updates skill's patch versions answer an update, not a finding
	if why := unanchored(skillFor(t, Trigger{}, "aktualisierung"), "laib_propose", map[string]any{}); why != "" {
		t.Fatalf("a patch version needs no finding: %s", why)
	}
}
