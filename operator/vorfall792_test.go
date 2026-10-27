package operator

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// #792, the box on 2026-10-27: bote-check, frage-check and immich-check
// failed at 10:30 and stayed red for hours. A failed oneshot writes no red
// journal line, so no event reached the Operator; the look saw the reds,
// but its session — and any incident — was skipped all day, because the
// household model was unloaded and a session never makes the cold start.
// Nothing reached the journal, and /ready still said „läuft“.

const (
	redBote  = `{"severity":"rot","probe":"unit","service":"bote","detail":"bote-check.service ist failed","fix":"laib logs bote","repair":["restart","bote"]}`
	boteLogs = "Okt 27 10:30:01 box systemd[1]: Starting bote-check.service\nOkt 27 10:30:02 box bote-check[812]: Error: joining network namespace of container bote: exit status 125"
)

// A red the look sees becomes an incident: logs, the restart diag offers,
// a look again, a note in German — and, still red, a model session, which
// may wake an unloaded model. The same red opens no second incident.
func TestARedFindingInALookBecomesAnIncidentTheOperatorWorks(t *testing.T) {
	lease, catalogue := household, unloaded
	llm, asked := fakeLease(t, &lease, &catalogue, []Message{
		{Role: "assistant", ToolCalls: []ToolCall{tcall("1", "laib_note", `{"kind":"incident","text":"bote-check scheitert beim Netz des Pods."}`)}},
		{Role: "assistant", Content: "fertig"},
	})
	mcp, calls := fakeMCP(t, map[string]string{"laib_diag": "[" + redBote + "]", "laib_logs": boteLogs, "laib_restart": "rot: bote-check.service failed"})
	clock := time.Date(2026, 10, 27, 10, 31, 0, 0, time.Local)
	op := &Operator{Skills: "../skills", MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL + "/v1", Model: "qwen"}, Home: t.TempDir(), Now: func() time.Time { return clock }}
	skills := []Skill{skillFor(t, Trigger{Kind: "tick"}, "routineblick"), skillFor(t, Trigger{Kind: "event"}, "")}
	r := &Runner{Op: op}
	r.LoadLast()
	r.Last["routineblick"] = clock.Add(-time.Hour)

	ss := r.Step(context.Background(), clock, skills)
	all := strings.Join(*calls, "\n")
	for _, want := range []string{`laib_logs {"service":"bote"`, `laib_restart {"service":"bote"}`} {
		if !strings.Contains(all, want) {
			t.Fatalf("the incident did not %s: %s", want, all)
		}
	}
	if !strings.Contains(all, "laib_note") || !strings.Contains(all, "neu gestartet") || !strings.Contains(all, "exit status 125") || !strings.Contains(all, "eskaliere ich") {
		t.Fatalf("no German note says what it saw and did: %s", all)
	}
	if *asked == 0 || len(ss) < 2 || ss[len(ss)-1].Skill != "vorfall" || ss[len(ss)-1].Outcome == "skipped" {
		t.Fatalf("a red that stays red after the restart reaches no model session (an unloaded model may load for it): %d %+v", *asked, ss)
	}

	clock = clock.Add(10 * time.Minute)
	r.Step(context.Background(), clock, skills)
	if n := countPrefix(*calls, "laib_restart"); n != 1 {
		t.Fatalf("the same red opened another incident: %d restarts", n)
	}
}

// When the model cannot be had — the card lent — the incident waits, and
// /ready says why; after half an hour it is escalated once with what was
// seen, never left waiting a day, and still waits for the model.
func TestAnIncidentThatCannotReachTheModelIsEscalatedInBoundedTime(t *testing.T) {
	lease, catalogue := werkstatt, loaded
	llm, asked := fakeLease(t, &lease, &catalogue, []Message{{Role: "assistant", Content: "fertig"}})
	mcp, calls := fakeMCP(t, map[string]string{"laib_diag": "[" + redBote + "]", "laib_logs": boteLogs})
	clock := time.Date(2026, 10, 27, 10, 31, 0, 0, time.Local)
	op := &Operator{Skills: "../skills", MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL + "/v1", Model: "qwen"}, Home: t.TempDir(), Now: func() time.Time { return clock }}
	skills := []Skill{skillFor(t, Trigger{Kind: "tick"}, "routineblick"), skillFor(t, Trigger{Kind: "event"}, "")}
	r := &Runner{Op: op}
	r.LoadLast()
	r.Last["routineblick"] = clock.Add(-time.Hour)

	r.Step(context.Background(), clock, skills)
	w := op.Work()
	if w.Queued != 1 || !strings.Contains(w.Waiting, "werkstatt") || !strings.Contains(w.Incident, "bote") || w.Since.IsZero() {
		t.Fatalf("/ready does not say what waits and why: %+v", w)
	}
	clock = clock.Add(20 * time.Minute)
	r.Step(context.Background(), clock, skills)
	if countPrefix(*calls, "laib_escalate") != 0 {
		t.Fatalf("escalated before the bound: %v", *calls)
	}
	clock = clock.Add(11 * time.Minute)
	r.Step(context.Background(), clock, skills)
	all := strings.Join(*calls, "\n")
	if countPrefix(*calls, "laib_escalate") != 1 || !strings.Contains(all, `"service":"bote"`) || !strings.Contains(all, "exit status 125") || !strings.Contains(all, "werkstatt") {
		t.Fatalf("the waiting incident was not escalated with what it saw and why it waited: %s", all)
	}
	clock = clock.Add(10 * time.Minute)
	r.Step(context.Background(), clock, skills)
	if countPrefix(*calls, "laib_escalate") != 1 || *asked != 0 || op.Work().Queued != 1 {
		t.Fatalf("escalated again, asked the model (%d) or dropped the incident: %+v", *asked, op.Work())
	}
	if n := countPrefix(*calls, "laib_note"); n != 1 || !strings.Contains(strings.Join(*calls, "\n"), "eskaliere ich") {
		t.Fatalf("the waiting incident's note is not there once: %d", n)
	}
}

// A restart that clears the red ends the incident without a model: one
// note says what was seen and done.
func TestARestartThatClearsTheRedNeedsNoModel(t *testing.T) {
	lease, catalogue := household, loaded
	llm, asked := fakeLease(t, &lease, &catalogue, []Message{{Role: "assistant", Content: "fertig"}})
	replies := map[string]string{"laib_diag": "[" + redBote + "]", "laib_logs": boteLogs, "laib_restart": "grün: bote"}
	mcp, calls := fakeMCP(t, replies)
	inner := mcp.Config.Handler
	mcp.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(b))
		inner.ServeHTTP(w, r)
		if bytes.Contains(b, []byte(`"laib_restart"`)) {
			replies["laib_diag"] = "[]"
		}
	})
	op := &Operator{Skills: "../skills", MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL + "/v1", Model: "qwen"}, Home: t.TempDir()}
	tr := Trigger{Kind: "event", Service: "bote", Detail: "bote: unit bote-check.service ist failed", Restart: []string{"bote"}, Opened: true}
	s, err := op.Run(context.Background(), skillFor(t, tr, ""), tr)
	if err != nil || s.Outcome != "green" || *asked != 0 || countPrefix(*calls, "laib_note") != 1 || !strings.Contains(s.Note, "nichts davon mehr rot") {
		t.Fatalf("%v %d %+v", err, *asked, s)
	}
}
