package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/pages"
)

// #593: Haus's /box page asks for the 25 newest lines and the last
// morning report, not 36 hours of journal.
func TestJournalLimitAndKind(t *testing.T) {
	home := t.TempDir()
	env := &cliEnv{JSON: true}
	env.Paths.Journal = home
	old := time.Now().Add(-72 * time.Hour).UTC()
	journal.Append(home, journal.Entry{T: old, Actor: "ops", Verb: "note", Reason: "report", Result: "info", Detail: "Bericht"})
	for i := 0; i < 30; i++ {
		journal.Append(home, journal.Entry{Actor: "claude", Verb: "look", Result: "info", Detail: string(rune('a' + i%26))})
	}
	journal.Append(home, journal.Entry{Actor: "ops", Verb: "note", Reason: "watch", Result: "info"})
	run := func(args ...string) []journal.Entry {
		t.Helper()
		var buf bytes.Buffer
		env.Out = &buf
		if rc := cmdJournal(context.Background(), env, args); rc != cli.ExitOK {
			t.Fatalf("%v: rc %d", args, rc)
		}
		var es []journal.Entry
		if err := json.Unmarshal(buf.Bytes(), &es); err != nil {
			t.Fatalf("%v: %v %s", args, err, buf.String())
		}
		return es
	}
	if es := run("--kind", "report", "--limit", "1"); len(es) != 1 || es[0].Detail != "Bericht" {
		t.Fatalf("limit without since reaches the last report of any age: %+v", es)
	}
	if es := run("--limit", "25"); len(es) != 25 || es[24].Reason != "watch" {
		t.Fatalf("the 25 newest, oldest first: %d", len(es))
	}
	if es := run("--verb", "note"); len(es) != 1 || es[0].Reason != "watch" {
		t.Fatalf("verb filter within the default 24h: %+v", es)
	}
	if es := run("--since", "96h", "--kind", "report"); len(es) != 1 {
		t.Fatalf("kind with since: %+v", es)
	}
	if es := run(); len(es) != 31 {
		t.Fatalf("no flags: 24h as before, got %d", len(es))
	}
	if rc := cmdJournal(context.Background(), env, []string{"--kind", "bericht"}); rc == cli.ExitOK {
		t.Fatal("an unknown kind is refused")
	}
	if rc := cmdJournal(context.Background(), env, []string{"--limit", "x"}); rc == cli.ExitOK {
		t.Fatal("a limit that is no number is refused")
	}
}

// #593: laib_status answers from a readiness under 30 s old; --fresh, a
// named service, a new journal line (apply, decision, restart) or the
// apply lock recompute it. The CLI and /ready always compute.
func TestStatusAnswersFromAFreshReadiness(t *testing.T) {
	c := newCbox(t)
	status := func(args ...string) map[string]any {
		t.Helper()
		var buf bytes.Buffer
		env := &cliEnv{Paths: c.env.Paths, Actor: "haus", JSON: true, MCP: true, Out: &buf}
		if rc := pages.Status(context.Background(), env, args); rc != cli.ExitOK {
			t.Fatalf("rc %d", rc)
		}
		var out map[string]any
		json.Unmarshal(buf.Bytes(), &out)
		if _, ok := out["at"].(string); !ok {
			t.Fatalf("no at: %s", buf.String())
		}
		return out
	}
	probes := func() int {
		n := 0
		for _, l := range c.calls {
			if strings.Contains(l, "is-active") {
				n++
			}
		}
		c.calls = nil
		return n
	}
	first := status()
	if probes() == 0 {
		t.Fatal("the first call computes")
	}
	if again := status(); probes() != 0 || again["at"] != first["at"] {
		t.Fatal("a second call within 30 s answers from the kept readiness")
	}
	status("--fresh")
	if probes() == 0 {
		t.Fatal("--fresh computes")
	}
	status("whoami")
	if probes() == 0 {
		t.Fatal("a named service computes (after a restart)")
	}
	status()
	probes()
	journal.Append(c.env.Paths.Journal, journal.Entry{Actor: "laib", Verb: "apply", Result: "green"})
	status()
	if probes() == 0 {
		t.Fatal("a new journal line (apply, approve, restart) computes")
	}
	pages.StatusTTL = 0
	t.Cleanup(func() { pages.StatusTTL = 30 * time.Second })
	status()
	if probes() == 0 {
		t.Fatal("an expired readiness computes")
	}
	var buf bytes.Buffer
	if rc := pages.Status(context.Background(), &cliEnv{Paths: c.env.Paths, JSON: true, Out: &buf}, nil); rc != cli.ExitOK || probes() == 0 {
		t.Fatal("the CLI always computes")
	}
}
