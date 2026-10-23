package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/proposal"
)

// a busy day: many looks, long notes, and proposals whose cards and
// reasons are long
func busyBox(t *testing.T) *cliEnv {
	env, _, _ := envBox(t)
	now := time.Now().UTC()
	long := strings.Repeat("ein langer Befund mit Ausgabe ", 100)
	for i := 0; i < 300; i++ {
		verb := []string{"look", "note", "apply", "session"}[i%4]
		journal.Append(env.Paths.Journal, journal.Entry{T: now.Add(-time.Duration(300-i) * 4 * time.Minute), Actor: "ops", Verb: verb, Service: "whoami", Result: "info", Reason: long, Detail: long})
	}
	dir := cli.InboxDir(env)
	os.MkdirAll(dir, 0o755)
	for id := 1; id <= 60; id++ {
		p := &proposal.Proposal{ID: id, Actor: "ops", Reason: fmt.Sprintf("Vorschlag %d", id), Tier: "propose", Status: "approved", Class: "human",
			Created: now.Add(-time.Duration(61-id) * time.Hour), Files: []string{"services/whoami/values.yml"}, Reasons: []string{long, long}, Warnings: []string{long}, Outcome: "rejected", RejectReason: "falscher Dienst"}
		if id == 60 {
			p.Status, p.Outcome, p.RejectReason, p.Reasons, p.Warnings = "open", "", "", []string{"eine Quelle aus dem Netz"}, nil
		}
		if err := proposal.Save(dir, p); err != nil {
			t.Fatal(err)
		}
	}
	return env
}

func runJSON(t *testing.T, env *cliEnv, run func(context.Context, *cliEnv, []string) int, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	e := &cliEnv{Actor: env.Actor, Paths: env.Paths, JSON: true, Out: &out, ErrOut: &out}
	if code := run(context.Background(), e, args); code != cli.ExitOK {
		t.Fatalf("%v: exit %d: %s", args, code, out.String())
	}
	return out.String()
}

// #579: laib context fits an agent's first look by default (readiness, what
// is not green, the open proposals, the journal short); --full is the
// whole bundle as before; --since narrows the journal.
func TestContextFitsAFirstLook(t *testing.T) {
	env := busyBox(t)
	short := runJSON(t, env, cmdContext)
	if len(short) > 20000 {
		t.Fatalf("laib context answers %d characters", len(short))
	}
	var c struct {
		Ready    map[string]any
		Services []map[string]any
		Journal  []journal.Entry
		Open     []proposal.Proposal
		Omitted  int
		Upstream map[string][]string
	}
	if err := json.Unmarshal([]byte(short), &c); err != nil {
		t.Fatal(err)
	}
	if c.Ready["state"] == nil || len(c.Open) != 1 || c.Open[0].ID != 60 || c.Open[0].Reason != "Vorschlag 60" {
		t.Errorf("readiness or the open proposal is missing: %+v %+v", c.Ready, c.Open)
	}
	if len(c.Journal) == 0 || len(c.Journal) > 30 || c.Omitted == 0 {
		t.Fatalf("the journal is not short: %d entries, %d omitted", len(c.Journal), c.Omitted)
	}
	for _, e := range c.Journal {
		if e.Verb == "look" || len([]rune(e.Reason)) > 161 || len([]rune(e.Detail)) > 101 {
			t.Fatalf("a journal line in full: %s %d %d", e.Verb, len(e.Reason), len(e.Detail))
		}
	}
	if last := c.Journal[len(c.Journal)-1]; time.Since(last.T) > 10*time.Minute {
		t.Errorf("the short journal is not the newest entries: %v", last.T)
	}

	full := runJSON(t, env, cmdContext, "--full")
	var f struct{ Journal []journal.Entry }
	json.Unmarshal([]byte(full), &f)
	if len(f.Journal) < 300 || !strings.Contains(full, "\"look\"") || !strings.Contains(full, strings.Repeat("ein langer Befund mit Ausgabe ", 100)) {
		t.Errorf("--full is not the whole bundle: %d entries", len(f.Journal))
	}
	var h struct{ Journal []journal.Entry }
	json.Unmarshal([]byte(runJSON(t, env, cmdContext, "--full", "--since", "1h")), &h)
	if len(h.Journal) < 10 || len(h.Journal) > 20 {
		t.Errorf("--since 1h: %d entries", len(h.Journal))
	}
}

// #579: laib inbox --all lists the decided proposals without their cards
// and reasons, newest first, at most --limit (20 by default, 0 for all);
// laib inbox <id> is one proposal in full.
func TestInboxAllIsShortAndOneInFull(t *testing.T) {
	env := busyBox(t)
	all := runJSON(t, env, inbox.List, "--all")
	if len(all) > 20000 {
		t.Fatalf("laib inbox --all answers %d characters", len(all))
	}
	var ps []map[string]any
	if err := json.Unmarshal([]byte(all), &ps); err != nil {
		t.Fatal(err)
	}
	if len(ps) != 21 || ps[0]["id"] != 60.0 || ps[0]["card"] == nil || ps[1]["id"] != 59.0 {
		t.Fatalf("not the open one in full, then the newest 20 decided: %d", len(ps))
	}
	for _, k := range []string{"id", "actor", "reason", "status", "created", "files", "outcome", "rejectReason"} {
		if ps[1][k] == nil {
			t.Errorf("a decided entry lost %s: %v", k, ps[1])
		}
	}
	if ps[1]["card"] != nil || ps[1]["reasons"] != nil || ps[1]["warnings"] != nil {
		t.Errorf("a decided entry carries its card or reasons: %v", ps[1])
	}
	json.Unmarshal([]byte(runJSON(t, env, inbox.List, "--all", "--limit", "5")), &ps)
	if len(ps) != 6 {
		t.Errorf("--limit 5: %d entries", len(ps))
	}
	json.Unmarshal([]byte(runJSON(t, env, inbox.List, "--all", "--limit", "0")), &ps)
	if len(ps) != 60 {
		t.Errorf("--limit 0: %d entries", len(ps))
	}
	var one map[string]any
	json.Unmarshal([]byte(runJSON(t, env, inbox.List, "7")), &one)
	if one["id"] != 7.0 || one["card"] == nil || one["reasons"] == nil {
		t.Errorf("laib inbox 7 is not the proposal in full: %v", one)
	}
	st, _ := cli.LoadState(env)
	if out, code := mcp.Call(context.Background(), env, st, "ops", "laib_inbox", map[string]any{"all": true, "limit": "3"}); code != cli.ExitOK || json.Unmarshal([]byte(out), &ps) != nil || len(ps) != 4 {
		t.Errorf("laib_inbox all, limit 3 over MCP: %d %d", code, len(ps))
	}
	if out, code := mcp.Call(context.Background(), env, st, "ops", "laib_inbox", map[string]any{"id": "7"}); code != cli.ExitOK || !strings.Contains(out, `"card"`) {
		t.Errorf("laib_inbox id 7 over MCP: %d %s", code, out)
	}
	open := runJSON(t, env, inbox.List)
	json.Unmarshal([]byte(open), &ps)
	if len(ps) != 1 || ps[0]["card"] == nil || ps[0]["reasons"] == nil {
		t.Errorf("laib inbox (open) changed: %s", open)
	}
}
