package operator

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// #858: what the Operator must always do runs in the runner, not in a
// prompt (CLAUDE.md rule 1). Each test plays a scripted model.

// The comment said a daily slot may wake an unloaded model; every skill but
// the report was a tick, and a tick never wakes it. Durchsicht, Sicherung,
// Aktualisierung and Nutzung could not load the model.
func TestADailySlotMayWakeTheModel(t *testing.T) {
	lease, catalogue := household, unloaded
	llm, asked := fakeLease(t, &lease, &catalogue, []Message{{Role: "assistant", Content: "Alles ruhig."}})
	mcp, _ := fakeMCP(t, nil)
	op := &Operator{Skills: "../skills", MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL + "/v1", Model: "qwen"}, Home: t.TempDir()}
	daily := skillFor(t, Trigger{}, "routineblick")
	daily.Name, daily.Trigger = "taeglich", SkillTrigger{Daily: "06:30"}
	r := &Runner{Op: op}
	r.LoadLast()
	day := time.Date(2026, 10, 22, 0, 0, 0, 0, time.Local)
	r.Last[daily.Name] = day.Add(-17 * time.Hour)
	ss := r.Step(context.Background(), day.Add(6*time.Hour+31*time.Minute), []Skill{daily})
	if len(ss) != 1 || ss[0].Outcome != "green" || *asked != 1 {
		t.Fatalf("a daily slot did not wake the unloaded model: %+v, asked %d", ss, *asked)
	}
}

// A skipped slot wrote no journal line and still moved the heartbeat, so
// diag said „läuft“. Now a skipped or failed slot is one line with its
// reason, once while it keeps failing, and the heartbeat moves only on
// work done.
func TestASkippedSlotIsOneJournalLineAndNoHeartbeat(t *testing.T) {
	lease, catalogue := werkstatt, loaded
	llm, _ := fakeLease(t, &lease, &catalogue, []Message{{Role: "assistant", Content: "Alles ruhig."}})
	mcp, calls := fakeMCP(t, nil)
	op := &Operator{Skills: "../skills", MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL + "/v1", Model: "qwen"}, Home: t.TempDir()}
	daily := skillFor(t, Trigger{}, "routineblick")
	daily.Name, daily.Trigger = "taeglich", SkillTrigger{Daily: "06:30"}
	r := &Runner{Op: op}
	r.LoadLast()
	day := time.Date(2026, 10, 22, 0, 0, 0, 0, time.Local)
	r.Last[daily.Name] = day.Add(-17 * time.Hour)
	for m := range 3 {
		if ss := r.Step(context.Background(), day.Add(6*time.Hour+time.Duration(31+m)*time.Minute), []Skill{daily}); len(ss) != 1 || ss[0].Outcome != "skipped" {
			t.Fatalf("%+v", ss)
		}
	}
	notes := slices.DeleteFunc(slices.Clone(*calls), func(c string) bool { return !strings.HasPrefix(c, "laib_note") })
	if len(notes) != 1 || !strings.Contains(notes[0], "wartet") || !strings.Contains(notes[0], "werkstatt") {
		t.Fatalf("three skips, want one journal line with the reason: %q", notes)
	}
	if op.Looked.Load() != 0 {
		t.Fatal("a skipped slot moved the heartbeat")
	}
	lease = household
	if ss := r.Step(context.Background(), day.Add(6*time.Hour+40*time.Minute), []Skill{daily}); len(ss) != 1 || ss[0].Outcome != "green" || op.Looked.Load() == 0 {
		t.Fatalf("the slot that ran: %+v, heartbeat %d", ss, op.Looked.Load())
	}
	lease = werkstatt // the next day's slot waits again: a new line
	r.Step(context.Background(), day.Add(30*time.Hour+31*time.Minute), []Skill{daily})
	if n := countPrefix(*calls, "laib_note") - 1; n != 2 { // the green run wrote its own
		t.Fatalf("the next skip after a run is a new line: %d notes in %q", n, *calls)
	}
}

// brokenModel answers every chat with a 500.
func brokenModel(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.Error(w, "out of memory", 500)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// A failing morning report consumed its slot with no report. Now its
// script prepares a report from status and journal, and the runner writes
// it when the model fails.
func TestAFailingMorningReportFallsBackToOneWithoutTheModel(t *testing.T) {
	status := `{"ready":{"state":"bereit","services":3,"green":2,"red":1},"services":[{"name":"jellyfin","state":"rot","detail":"unit failed"},{"name":"immich","state":"grün"},{"name":"auth","state":"grün"}]}`
	journal := `[{"t":"2026-10-22T03:00:00Z","actor":"laib","verb":"backup","result":"green","detail":"restic ok"},{"t":"2026-10-22T04:10:00Z","actor":"laib","verb":"apply","service":"jellyfin","result":"red","detail":"unit failed"}]`
	mcp, calls := fakeMCP(t, map[string]string{"laib_status": status, "laib_journal": journal})
	op := &Operator{Skills: "../skills", MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: brokenModel(t).URL, Model: "qwen"}, Home: t.TempDir()}
	report := skillFor(t, Trigger{Kind: "report"}, "")
	r := &Runner{Op: op}
	r.LoadLast()
	day := time.Date(2026, 10, 22, 0, 0, 0, 0, time.Local)
	r.Last[report.Name] = day.Add(-17 * time.Hour)
	ss := r.Step(context.Background(), day.Add(7*time.Hour+time.Minute), []Skill{report})
	if len(ss) != 1 || ss[0].Outcome != "fallback" {
		t.Fatalf("%+v", ss)
	}
	var note string
	for _, c := range *calls {
		if strings.HasPrefix(c, "laib_note") {
			note = c
		}
	}
	for _, want := range []string{`"kind":"report"`, "jellyfin", "ohne Modell", "500"} {
		if !strings.Contains(note, want) {
			t.Errorf("the fallback report lacks %q: %s", want, note)
		}
	}
	if countPrefix(*calls, "laib_note") != 1 {
		t.Errorf("one note, the report: %q", *calls)
	}
}

// The weekly backup sample ran only if the model called backup check. Now
// its script calls it, every week, with no model; a red check or a stale
// backup is an incident note the morning report reads.
func TestTheBackupSampleRunsWithoutTheModel(t *testing.T) {
	now := time.Now().UTC()
	green := `[{"t":"` + now.Add(-3*time.Hour).Format(time.RFC3339) + `","actor":"laib","verb":"backup","result":"green","detail":"restic ok"}]`
	stale := `[{"t":"` + now.Add(-72*time.Hour).Format(time.RFC3339) + `","actor":"laib","verb":"backup","result":"green","detail":"restic ok"}]`
	for _, c := range []struct {
		name, check, journal, kind, want string
	}{
		{"green", "grün · repo: restic check 5% ok; 12 Dateien gleich", green, "watch", "grün"},
		{"red check", "restic check: Fatal: pack 3f2a damaged", green, "incident", "damaged"},
		{"stale", "grün · repo: restic check 5% ok; 12 Dateien gleich", stale, "incident", "seit"},
	} {
		mcp, calls := fakeMCP(t, map[string]string{"laib_backup": c.check, "laib_journal": c.journal})
		llm, asked := recLLM(t, []Message{{Role: "assistant", Content: "x"}})
		op := &Operator{Skills: "../skills", MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL, Model: "qwen"}, Home: t.TempDir()}
		s, err := op.Run(context.Background(), skillFor(t, Trigger{}, "sicherung"), Trigger{Kind: "slot"})
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !slices.Contains(s.Script, `laib_backup {"action":"check"}`) || len(*asked) != 0 {
			t.Fatalf("%s: the check is the script's, no model: %q, model asked %d", c.name, *calls, len(*asked))
		}
		note := (*calls)[len(*calls)-1]
		if !strings.HasPrefix(note, "laib_note") || !strings.Contains(note, `"kind":"`+c.kind+`"`) || !strings.Contains(note, c.want) {
			t.Errorf("%s: %s, outcome %s", c.name, note, s.Outcome)
		}
	}
}

// A vorfall that ended red (not error) was neither queued nor escalated:
// escalating was rule 4 of the prompt. The runner escalates it, once; the
// session stays red and its line says „eskaliert“.
func TestARedIncidentIsEscalatedByTheRunnerOnce(t *testing.T) {
	for _, c := range []struct {
		name   string
		script []Message
		want   int
	}{
		{"red, the model did not escalate", []Message{{Role: "assistant", ToolCalls: []ToolCall{tcall("1", "laib_diag", `{}`)}}}, 1},
		{"the model escalated itself", []Message{
			{Role: "assistant", ToolCalls: []ToolCall{tcall("1", "laib_escalate", `{"service":"jellyfin","summary":"weiß nicht weiter"}`)}},
			{Role: "assistant", ToolCalls: []ToolCall{tcall("2", "laib_diag", `{}`)}}}, 1},
	} {
		mcp, calls := fakeMCP(t, nil)
		op := &Operator{Skills: "../skills", MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: fakeLLM(t, c.script).URL, Model: "test"}, Home: t.TempDir(), MaxCalls: 3}
		tr := Trigger{Kind: "event", Service: "jellyfin", Detail: "jellyfin: apply: unit failed"}
		s, err := op.Run(context.Background(), skillFor(t, tr, ""), tr)
		if err != nil {
			t.Fatal(err)
		}
		if n := countPrefix(*calls, "laib_escalate"); n != c.want || s.Outcome != "red" || !strings.Contains(s.Line(), "eskaliert") {
			t.Fatalf("%s: %d escalations, outcome %s, line %s: %q", c.name, n, s.Outcome, s.Line(), *calls)
		}
		for _, call := range *calls {
			if strings.HasPrefix(call, "laib_escalate") && !strings.Contains(call, "jellyfin") {
				t.Errorf("%s: the escalation names no service: %s", c.name, call)
			}
		}
	}
}

// A prompt named set_value (gone since #290), repair for a small model
// (only in more) and suggest (the tool is wish). Every tool a prompt names
// is on the menu of each model class that reads it: the menu the runner
// builds, from every built-in skill.yml, against the kernel's verb table.
func TestEveryToolAPromptNamesIsOnItsMenu(t *testing.T) {
	table, err := os.ReadFile("../cmd/laib/main.go")
	if err != nil {
		t.Fatal(err)
	}
	verbs := map[string]bool{}
	for _, m := range regexp.MustCompile(`\{Name: "([a-z_]+)", Tier: "[a-z]+", MCP: true,`).FindAllStringSubmatch(string(table), -1) {
		verbs[m[1]] = true
	}
	if len(verbs) < 30 {
		t.Fatalf("the verb table: %d MCP verbs read", len(verbs))
	}
	// names a prompt has used for a tool that is none: a word in German
	// prose that the model reads as a tool
	notTools := map[string]string{"diagnose": "diag", "suggest": "wish"}
	word := regexp.MustCompile(`\b[a-z][a-z_]*\b`)
	dirs, _ := filepath.Glob("../skills/*/skill.yml")
	for _, f := range dirs {
		sk := skillFor(t, Trigger{}, filepath.Base(filepath.Dir(f)))
		if sk.Modell == "nein" {
			continue // no model reads it
		}
		said := map[string]bool{}
		for _, typed := range []bool{false, true} {
			menu := sk.menu(typed)
			for _, w := range word.FindAllString(sk.Prompt, -1) {
				if said[w] {
					continue
				}
				said[w] = true
				switch {
				case notTools[w] != "":
					t.Errorf("%s: the prompt names %q, the tool is %s", sk.Name, w, notTools[w])
				case strings.Contains(w, "_") && !verbs[w] || verbs[w] && !menu["laib_"+w]:
					t.Errorf("%s (small model: %v): the prompt names %q, not on its menu %v", sk.Name, typed, w, menu)
				}
			}
		}
	}
}
