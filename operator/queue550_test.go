package operator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// #550: on the Kraftwerk the look still journalled „Alle 23 Dienste sind
// grün, keine neuen Befunde“ — a finding had gone away, the set differed
// from the last, and the model was asked to say nothing. A look that finds
// nothing new asks no model and writes no note; what went away is
// remembered, so its return is news.
func TestALookThatFindsNothingNewWritesNoNote(t *testing.T) {
	replies := map[string]string{"laib_diag": "[" + restoreTest + "," + watchdog + "]", "laib_repair": "Vorschlag #7 angelegt"}
	mcp, calls := fakeMCP(t, replies)
	llm, asked := recLLM(t, []Message{
		{Role: "assistant", ToolCalls: []ToolCall{tcall("1", "laib_note", `{"kind":"watch","text":"gelb"}`)}},
		{Role: "assistant", Content: "fertig"},
	})
	op := &Operator{Skills: "../skills", MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL, Model: "test"}, Home: t.TempDir(), Typed: true}
	look := skillFor(t, Trigger{Kind: "tick"}, "routineblick")
	run := func() Session {
		t.Helper()
		s, err := op.Run(context.Background(), look, Trigger{Kind: "tick"})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	run()
	for _, diag := range []string{"[" + watchdog + "]", "[]"} {
		replies["laib_diag"] = diag
		before, notes := len(*asked), countPrefix(*calls, "laib_note")
		if s := run(); s.Outcome != "unchanged" || len(*asked) != before || countPrefix(*calls, "laib_note") != notes {
			t.Fatalf("diag %s: only gone findings asked the model or wrote a note: %+v", diag, s)
		}
	}
	replies["laib_diag"] = "[" + restoreTest + "]"
	before := len(*asked)
	if s := run(); s.Outcome == "unchanged" || len(*asked) == before {
		t.Fatalf("a finding back after it went away is news: %+v", s)
	}
}

// queued is what waits in the queue's folder under OPERATOR_HOME.
func queued(t *testing.T, home string) []string {
	t.Helper()
	es, _ := os.ReadDir(filepath.Join(home, "queue"))
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

// Owner, 2026-10-24 (#550): an incident that comes while the graphics card
// is lent waits in a queue and runs afterwards, instead of being skipped.
// The queue is a folder under OPERATOR_HOME, so a restart keeps it; the
// same finding waits once; when the lease frees, the next step runs it and
// the note says what waited and how long.
func TestAnIncidentWaitsForTheGraphicsCardAndRunsAfterwards(t *testing.T) {
	lease, catalogue := werkstatt, loaded
	llm, asked := fakeLease(t, &lease, &catalogue, []Message{
		{Role: "assistant", ToolCalls: []ToolCall{tcall("1", "laib_note", `{"kind":"incident","text":"jellyfin neu gestartet."}`)}},
		{Role: "assistant", Content: "fertig"},
	})
	mcp, calls := fakeMCP(t, nil)
	home := t.TempDir()
	clock := time.Date(2026, 10, 24, 20, 0, 0, 0, time.Local)
	op := &Operator{Skills: "../skills", MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL + "/v1", Model: "qwen"}, Home: home, Now: func() time.Time { return clock }}
	vorfall := skillFor(t, Trigger{Kind: "event"}, "")
	r := &Runner{Op: op}
	for _, d := range []string{"jellyfin: apply: unit failed after 3 s", "jellyfin: apply: unit failed after 7 s"} {
		if ss := r.Event(context.Background(), []Skill{vorfall}, Trigger{Kind: "event", Service: "jellyfin", Detail: d}); len(ss) != 1 || ss[0].Outcome != "skipped" || !strings.Contains(ss[0].Line(), "wartet") {
			t.Fatalf("%+v", ss)
		}
		clock = clock.Add(10 * time.Minute)
	}
	r.Event(context.Background(), []Skill{vorfall}, Trigger{Kind: "event", Service: "immich", Detail: "immich: unit failed"})
	if q := queued(t, home); len(q) != 2 {
		t.Fatalf("want jellyfin once and immich: %v", q)
	}

	// a restart: a new runner over the same folder; the card is still lent
	r = &Runner{Op: &Operator{Skills: "../skills", MCP: op.MCP, LLM: op.LLM, Home: home, Now: op.Now}}
	r.LoadLast()
	clock = clock.Add(time.Hour)
	if ss := r.Step(context.Background(), clock, []Skill{vorfall}); len(ss) != 0 || *asked != 0 || len(queued(t, home)) != 2 {
		t.Fatalf("the queue ran while the card is lent: %+v", ss)
	}
	lease = household
	ss := r.Step(context.Background(), clock, []Skill{vorfall})
	if len(ss) != 2 || ss[0].Trigger.Service != "jellyfin" || ss[0].Outcome != "green" || len(queued(t, home)) != 0 {
		t.Fatalf("the freed lease did not run the queue, oldest first: %+v %v", ss, queued(t, home))
	}
	notes := strings.Join(*calls, "\n")
	if !strings.Contains(notes, "wartete 1h20m auf die Grafikkarte (seit 20:00, 2× gemeldet)") {
		t.Fatalf("the note does not say what waited and how long: %s", notes)
	}
}

// The queue is bounded: at most queueMax incidents, the oldest gives way;
// one older than queueAge is dropped, not run. Each drop is journalled.
func TestTheQueueIsBounded(t *testing.T) {
	lease, catalogue := werkstatt, loaded
	llm, asked := fakeLease(t, &lease, &catalogue, []Message{{Role: "assistant", Content: "erledigt"}})
	mcp, calls := fakeMCP(t, nil)
	home := t.TempDir()
	clock := time.Date(2026, 10, 24, 8, 0, 0, 0, time.Local)
	op := &Operator{Skills: "../skills", MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL + "/v1", Model: "qwen"}, Home: home, Now: func() time.Time { return clock }}
	vorfall := skillFor(t, Trigger{Kind: "event"}, "")
	r := &Runner{Op: op}
	for i := range queueMax + 3 {
		svc := string(rune('a'+i)) + "-dienst"
		r.Event(context.Background(), []Skill{vorfall}, Trigger{Kind: "event", Service: svc, Detail: svc + ": unit failed"})
		clock = clock.Add(time.Minute)
	}
	q := queued(t, home)
	if len(q) != queueMax || strings.Contains(strings.Join(q, " "), "a-dienst") {
		t.Fatalf("want %d, the oldest gone: %v", queueMax, q)
	}
	if !strings.Contains(strings.Join(*calls, "\n"), "a-dienst") {
		t.Fatalf("a dropped incident is not journalled: %v", *calls)
	}
	lease, clock = household, clock.Add(queueAge)
	r.LoadLast()
	if ss := r.Step(context.Background(), clock, []Skill{vorfall}); len(ss) != 0 || *asked != 0 || len(queued(t, home)) != 0 {
		t.Fatalf("stale incidents ran: %+v", ss)
	}
	if n := countPrefix(*calls, "laib_note"); n < queueMax {
		t.Fatalf("stale drops not journalled: %d notes", n)
	}
}
