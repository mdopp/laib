package operator

import (
	"context"
	"strings"
	"testing"
	"time"
)

// #907, 2026-10-29 15:52: the owner approved paperless' template update;
// the look in between saw paperless' health red while the apply restarted
// it, and the Operator snapshotted, restarted it a second time and wrote
// „Vorfall“ — inside the apply's 58 seconds. diag now says such a red is
// expected (info, wartet: the apply); the look leaves it alone: no note,
// no model, no repair. The same red once the apply let go is an incident
// as before.
const (
	heldPaperless = `{"severity":"info","probe":"health","service":"paperless","detail":"Get \"http://127.0.0.1:8000/accounts/login/\": connection reset by peer — wartet: ein Apply richtet paperless gerade ein (apply:human:mdopp)","wartet":"ein Apply richtet paperless gerade ein (apply:human:mdopp)"}`
	redPaperless  = `{"severity":"rot","probe":"health","service":"paperless","detail":"Get \"http://127.0.0.1:8000/accounts/login/\": connection reset by peer","fix":"laib logs paperless","repair":["restart","paperless"]}`
)

func TestARedAnApplyCausesIsNoIncidentAndItsReturnIs(t *testing.T) {
	lease, catalogue := household, loaded
	llm, asked := fakeLease(t, &lease, &catalogue, []Message{
		{Role: "assistant", ToolCalls: []ToolCall{tcall("1", "laib_note", `{"kind":"watch","text":"Alles grün."}`)}},
		{Role: "assistant", Content: "fertig"},
	})
	replies := map[string]string{"laib_diag": "[]", "laib_restart": "grün: paperless"}
	mcp, calls := fakeMCP(t, replies)
	clock := time.Date(2026, 10, 29, 15, 42, 0, 0, time.Local)
	op := &Operator{Skills: "../skills", MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL + "/v1", Model: "qwen"}, Home: t.TempDir(), Now: func() time.Time { return clock }}
	skills := []Skill{skillFor(t, Trigger{Kind: "tick"}, "routineblick"), skillFor(t, Trigger{Kind: "event"}, "")}
	r := &Runner{Op: op}
	r.LoadLast()
	r.Last["routineblick"] = clock.Add(-time.Hour)
	r.Step(context.Background(), clock, skills) // all green: the look's baseline

	n, before := len(*calls), *asked
	replies["laib_diag"] = "[" + heldPaperless + "]"
	clock = clock.Add(10 * time.Minute)
	r.Step(context.Background(), clock, skills)
	for _, c := range (*calls)[n:] {
		if !strings.HasPrefix(c, "laib_diag") {
			t.Fatalf("the look acted on a red the apply causes: %s", strings.Join((*calls)[n:], "\n"))
		}
	}
	if *asked != before {
		t.Fatal("a red the apply causes woke the model")
	}

	replies["laib_diag"] = "[" + redPaperless + "]"
	clock = clock.Add(10 * time.Minute)
	r.Step(context.Background(), clock, skills)
	if countPrefix(*calls, "laib_restart") != 1 || !strings.Contains(strings.Join(*calls, "\n"), "Vorfall") {
		t.Fatalf("the red that outlived the apply was not repaired as before: %s", strings.Join(*calls, "\n"))
	}
}

// #908, 2026-10-29: a Werkstatt session's own `laib exec scan` exited 1 (its
// quoting); the Operator took the red exec line for scan's fault and
// restarted scan mid-stack — the owner's stack was lost. That evening the
// budget refused the session's execs on paperless, and the Operator read
// the refusals as its own red task: a restart into the same budget, an
// escalation to the owner. A hand's exec or write is the hand's command and
// a refused action did nothing: neither opens an incident. The service's
// own probes reach the Operator through its look.
func TestAHandsExecOrARefusalOpensNoIncident(t *testing.T) {
	for _, c := range []struct {
		verb, actor, result, detail string
		want                        bool
	}{
		{"exec", "claude", "red", "exit status 1\nmd5sum: '…': No such file or directory", false},
		{"exec", "claude", "red", "Budget erschöpft (8/h). Ein Mensch muss ran: python3 manage.py shell", false},
		{"restart", "claude", "red", "Budget erschöpft (8/h). Ein Mensch muss ran: restart", false},
		{"write", "pi", "red", "open /x: permission denied", false},
		{"exec", "claude", "info", "", false},
		{"restart", "ops", "red", "health rot", false}, // our own
		{"escalate", "claude", "red", "scan …", false}, // the next hand's, not ours
		{"restart", "human:mdopp", "red", "health 502 Bad Gateway", true},
		{"apply", "human:mdopp", "red", "paperless: health 502", true},
		{"backup", "laib", "red", "restic: repository locked", true},
		{"converge", "laib", "red", "Budget erschöpft (6/h): 3 Dateien Drift, nicht aktiv: scan", true},
	} {
		if got := Opens(c.verb, c.actor, c.result, c.detail); got != c.want {
			t.Errorf("%s %s %s %q opens an incident: %v, want %v", c.actor, c.verb, c.result, c.detail, got, c.want)
		}
	}
}
