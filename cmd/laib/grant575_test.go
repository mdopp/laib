package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/registry"
	"github.com/mdopp/laib/internal/state"
)

// #575: laib init writes the defaults; a box from before a release gets the
// built-in hands' new verbs as one proposal a human decides — never twice
// while it is open, never again for verbs the owner rejected, nothing for a
// hand without a verb list or one the owner added.
func TestAnOldBoxGetsTheNewDefaultVerbsAsOneProposal(t *testing.T) {
	env, _ := testBox(t)
	ctx := context.Background()
	pol := filepath.Join(env.Paths.State, "policy.yml")
	if b, _ := os.ReadFile(pol); string(b) != state.DefaultPolicy {
		t.Fatal("laib init does not write state.DefaultPolicy")
	}
	old := strings.Replace(state.DefaultPolicy, ", zuhause, scan, note]", ", zuhause]", 1)
	old = strings.Replace(old, "reject, inbox, status, note]", "reject, inbox, status]", 1)
	old = strings.Replace(old, "  human: {ceiling: destroy}\n", "  human: {ceiling: destroy}\n  kiosk: {ceiling: read, verbs: [status]}   # the owner's\n", 1)
	os.WriteFile(pol, []byte(old), 0o644)
	if err := proposal.CommitDirty(ctx, env.Paths.State, "human:test"); err != nil {
		t.Fatal(err)
	}
	env.Actor = "laib"
	run := func() *proposal.Proposal {
		t.Helper()
		st, err := cli.LoadState(env)
		if err != nil {
			t.Fatal(err)
		}
		p, err := inbox.ProposeDefaults(ctx, env, st)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	p := run()
	if p == nil || p.Class != "human" || strings.Join(p.Files, ",") != "policy.yml" {
		t.Fatalf("%+v", p)
	}
	diff := proposal.Diff(ctx, env.Paths.State, p)
	if strings.Count(diff, "\n+  ") != 2 || strings.Count(diff, "\n-  ") != 2 || !strings.Contains(diff, "zuhause, scan, note]") || !strings.Contains(diff, "status, note]") {
		t.Fatalf("not exactly the two lines:\n%s", diff)
	}
	if b, _ := os.ReadFile(pol); string(b) != old {
		t.Fatal("main changed before a human decided")
	}
	if again := run(); again != nil {
		t.Fatalf("a second proposal while #%d is open: #%d", p.ID, again.ID)
	}
	if err := proposal.Reject(ctx, env.Paths.State, cli.InboxDir(env), p, "human:test", "nein"); err != nil {
		t.Fatal(err)
	}
	if again := run(); again != nil {
		t.Fatalf("the rejected verbs came back as #%d", again.ID)
	}

	// a new release's verb is a new question; `laib updates --run` asks it (the unit runs it right after self-update.sh)
	os.WriteFile(pol, []byte(strings.Replace(old, "person_pending, calendar,", "person_pending,", 1)), 0o644)
	if code := registry.Updates(ctx, env, []string{"--run"}); code != cli.ExitOK {
		t.Fatalf("updates --run: exit %d", code)
	}
	if open, _ := proposal.List(cli.InboxDir(env), true); len(open) != 1 || !strings.Contains(open[0].Reason, "+calendar +scan") {
		t.Fatalf("%+v", open)
	}

	// laib propose --grant: one verb for one hand, as a proposal
	env.Actor = "human:test"
	st, _ := cli.LoadState(env)
	out, code := mcp.Call(ctx, env, st, "claude", "laib_propose", map[string]any{"reason": "Haus soll scannen", "hand": "haus", "verb": "scan"})
	if code != cli.ExitOK || !strings.Contains(out, "policy.yml") {
		t.Fatalf("exit %d: %s", code, out)
	}
	for _, bad := range []map[string]any{
		{"reason": "x", "hand": "haus", "verb": "fly"},    // no such verb
		{"reason": "x", "hand": "nobody", "verb": "scan"}, // no such hand
		{"reason": "x", "hand": "claude", "verb": "scan"}, // no list: a list would narrow it
		{"reason": "x", "hand": "haus", "verb": "status"}, // has it
		{"reason": "x", "hand": "haus"},                   // half a grant
	} {
		if out, code := mcp.Call(ctx, env, st, "claude", "laib_propose", bad); code == cli.ExitOK {
			t.Fatalf("%v: %s", bad, out)
		}
	}
	if code := inbox.Propose(ctx, env, []string{"--grant", "kiosk", "inbox", "der Kiosk zeigt den Eingang"}); code != cli.ExitOK {
		t.Fatalf("CLI --grant: exit %d", code)
	}
}
