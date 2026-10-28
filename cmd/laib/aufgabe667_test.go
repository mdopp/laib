package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/aufgaben"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/proposal"
)

type aufgabeOut struct {
	Message  string             `json:"message"`
	Offen    int                `json:"offen"`
	Faellig  int                `json:"faellig"`
	Aufgabe  aufgaben.Aufgabe   `json:"aufgabe"`
	Aufgaben []aufgaben.Aufgabe `json:"aufgaben"`
}

func callAufgabe(t *testing.T, env *cliEnv, hand string, args map[string]any) (aufgabeOut, string, int) {
	t.Helper()
	out, code := mcp.Call(context.Background(), env, mustState(t, env), hand, "laib_aufgabe", args)
	var got aufgabeOut
	if code == cli.ExitOK {
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	return got, out, code
}

// #667: Box-Aufgaben over MCP. ops and claude may add, only a human,
// claude or Haus checks off or defers; none of it is a proposal, every
// change is one commit of aufgaben.yml on main and one journal line.
func TestBoxAufgabenOverMCP(t *testing.T) {
	env, _ := testBox(t)
	t.Setenv("LAIB_DISK", "/nonexistent")
	st := mustState(t, env)
	tools := mcp.Tools(st, "claude")
	i := slices.IndexFunc(tools, func(m map[string]any) bool { return m["name"] == "laib_aufgabe" })
	if i < 0 {
		t.Fatal("claude has no laib_aufgabe")
	}
	schema := tools[i]["inputSchema"].(map[string]any)
	props := schema["properties"].(map[string]any)
	for _, p := range []string{"action", "id", "titel", "warum", "erinnern", "befund", "von", "alle", "faellig"} {
		if props[p] == nil {
			t.Errorf("laib_aufgabe lacks %s", p)
		}
	}
	if e := props["action"].(map[string]any)["enum"]; !slices.Equal(e.([]string), []string{"list", "zeigen", "add", "erledigt", "zurueckstellen"}) {
		t.Errorf("action enum %v", e)
	}
	if !slices.Equal(schema["required"].([]string), []string{"action"}) {
		t.Errorf("required %v", schema["required"])
	}

	got, out, code := callAufgabe(t, env, "ops", map[string]any{"action": "add", "titel": "Wächter von außen", "warum": "fällt die Box aus, erfährt es niemand", "erinnern": "2026-12-01", "befund": "watchdog"})
	if code != cli.ExitOK || got.Aufgabe.ID != 1 || got.Aufgabe.Von != "ops" || got.Aufgabe.FertigWenn != "watchdog" || !strings.Contains(got.Message, "#1") {
		t.Fatalf("ops add: exit %d: %s", code, out)
	}
	if _, out, code := callAufgabe(t, env, "bote", map[string]any{"action": "add", "titel": "x"}); code == cli.ExitOK {
		t.Fatalf("the bote added a task: %s", out)
	}
	for _, bad := range []map[string]any{{"action": "add"}, {"action": "add", "titel": "x", "erinnern": "1.12."}, {"action": "add", "titel": "x", "befund": "Watch Dog!"}} {
		if _, out, code := callAufgabe(t, env, "claude", bad); code == cli.ExitOK {
			t.Errorf("%v was accepted: %s", bad, out)
		}
	}
	if _, out, code := callAufgabe(t, env, "ops", map[string]any{"action": "erledigt", "id": "1"}); code == cli.ExitOK {
		t.Fatalf("ops checked a task off: %s", out)
	}
	got, out, code = callAufgabe(t, env, "claude", map[string]any{"action": "add", "titel": "TOTP für mdopp prüfen"})
	if code != cli.ExitOK || got.Aufgabe.ID != 2 {
		t.Fatalf("claude add: %s", out)
	}
	got, out, code = callAufgabe(t, env, "claude", map[string]any{"action": "zurueckstellen", "id": "2", "erinnern": "2026-11-30"})
	if code != cli.ExitOK || got.Aufgabe.Erinnern != "2026-11-30" {
		t.Fatalf("defer: %s", out)
	}
	got, out, code = callAufgabe(t, env, "claude", map[string]any{"action": "erledigt", "id": "2"})
	if code != cli.ExitOK || got.Aufgabe.Erledigt == nil || got.Aufgabe.Erledigt.Von != "claude" {
		t.Fatalf("done: %s", out)
	}
	got, _, _ = callAufgabe(t, env, "claude", map[string]any{"action": "list"})
	if got.Offen != 1 || len(got.Aufgaben) != 1 || got.Aufgaben[0].ID != 1 {
		t.Fatalf("list shows the open ones: %+v", got)
	}
	got, _, _ = callAufgabe(t, env, "claude", map[string]any{"action": "list", "alle": true})
	if len(got.Aufgaben) != 2 {
		t.Fatalf("list --alle shows the done ones too: %+v", got)
	}
	if ps, _ := proposal.List(cli.InboxDir(env), false); len(ps) != 0 {
		t.Fatalf("bookkeeping made %d proposals", len(ps))
	}
	log, _ := exec.Command("git", "-C", env.Paths.State, "log", "--format=%s", "--", "aufgaben.yml").Output()
	if n := strings.Count(string(log), "\n"); n != 4 {
		t.Fatalf("four changes, four commits of aufgaben.yml: %s", log)
	}
	es, _ := journal.Read(env.Paths.Journal, time.Time{})
	if n := len(slices.DeleteFunc(es, func(e journal.Entry) bool { return e.Verb != "aufgabe" })); n != 4 {
		t.Fatalf("four journal lines, got %d", n)
	}

	// diag: the watchdog finding is deferred to the task
	diag := func() []map[string]any {
		out, code := mcp.Call(context.Background(), env, mustState(t, env), "ops", "laib_diag", map[string]any{})
		var fs []map[string]any
		if code != cli.ExitOK || json.Unmarshal([]byte(out), &fs) != nil {
			t.Fatalf("diag: %d %s", code, out)
		}
		return fs
	}
	find := func(fs []map[string]any, probe string) map[string]any {
		for _, f := range fs {
			if f["probe"] == probe {
				return f
			}
		}
		return nil
	}
	if f := find(diag(), "watchdog"); f == nil || f["severity"] != "info" || f["aufgabe"] != 1.0 || !strings.Contains(f["detail"].(string), "zurückgestellt → Box-Aufgabe #1") {
		t.Fatalf("the watchdog finding is not deferred: %v", f)
	}
	// the raid probe cannot be measured here (no laib-disk): its task stays open
	callAufgabe(t, env, "claude", map[string]any{"action": "add", "titel": "Zweite Datenplatte", "befund": "raid"})
	// the owner sets the watchdog: diag no longer sees it and closes the task
	box := filepath.Join(env.Paths.State, "box.yml")
	b, _ := os.ReadFile(box)
	os.WriteFile(box, append(b, []byte("watchdog:\n  url: https://hc.example/ping/x\n")...), 0o644)
	proposal.CommitDirty(context.Background(), env.Paths.State, "human:test")
	if f := find(diag(), "watchdog"); f != nil {
		t.Fatalf("watchdog still found: %v", f)
	}
	list, _ := aufgaben.Load(env.Paths.State)
	if e := list[0].Erledigt; e == nil || e.Von != "laib" || e.Wie != "Befund weg" {
		t.Fatalf("the watchdog task is not closed by laib: %+v", list[0])
	}
	if list[2].Erledigt != nil {
		t.Fatalf("the raid task closed while laib-disk could not measure: %+v", list[2])
	}

	// readiness carries the open count
	out, code = mcp.Call(context.Background(), env, mustState(t, env), "haus", "laib_status", map[string]any{"fresh": true})
	var rd struct {
		Ready struct {
			Aufgaben int `json:"aufgaben"`
		} `json:"ready"`
	}
	if code != cli.ExitOK || json.Unmarshal([]byte(out), &rd) != nil || rd.Ready.Aufgaben != 1 {
		t.Fatalf("readiness: one open task (raid): %d %s", code, out)
	}

	// laib rollback without a commit reverts the last change of the box,
	// not the bookkeeping on top of it
	head, _ := exec.Command("git", "-C", env.Paths.State, "log", "-1", "--format=%s").Output()
	if !strings.HasPrefix(string(head), "aufgabe") {
		t.Fatalf("HEAD is %s", head)
	}
	human := *env
	inbox.Rollback(context.Background(), &human, nil)
	if b, _ := os.ReadFile(box); strings.Contains(string(b), "watchdog") {
		t.Fatal("rollback did not revert the box.yml change under the aufgaben commits")
	}
	if l, _ := aufgaben.Load(env.Paths.State); len(l) != 3 {
		t.Fatalf("rollback reverted the bookkeeping: %d tasks", len(l))
	}
}
