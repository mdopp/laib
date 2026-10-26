package main

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/sysd"
)

// #746: a repair card's headline says what the repair does, in German,
// from the finding and the verb, never „Einstellungen der Box ändern sich“.
func TestARepairCardSaysWhatTheRepairDoes(t *testing.T) {
	ctx := context.Background()
	env, _ := testBox(t)
	failedWhoami(t)
	st, _ := cli.LoadState(env)
	if out, code := mcp.Call(ctx, env, st, "ops", "laib_repair", map[string]any{"probe": "unit", "service": "whoami"}); code != cli.ExitOK {
		t.Fatalf("repair: exit %d: %s", code, out)
	}
	open, _ := proposal.List(cli.InboxDir(env), true)
	if len(open) != 1 {
		t.Fatalf("want one proposal: %+v", open)
	}
	if c := proposal.Card(open[0], "box.test"); c.Summary != "Whoami wird neu gestartet" || !strings.Contains(c.Why, "whoami.service ist") {
		t.Fatalf("the headline says what runs, the finding stays in Why: %+v", c)
	}
}

// #771: the same repair for the same finding is filed once; a second
// laib repair, by another hand, answers with the open one.
func TestTheSameRepairIsFiledOnce(t *testing.T) {
	ctx := context.Background()
	env, _ := testBox(t)
	failedWhoami(t)
	st, _ := cli.LoadState(env)
	for _, hand := range []string{"claude", "ops"} {
		if out, code := mcp.Call(ctx, env, st, hand, "laib_repair", map[string]any{"probe": "unit", "service": "whoami"}); code != cli.ExitOK || hand == "ops" && !strings.Contains(out, `"id": 1,`) {
			t.Fatalf("%s: exit %d: %s", hand, code, out)
		}
	}
	if open, _ := proposal.List(cli.InboxDir(env), true); len(open) != 1 {
		t.Fatalf("one repair, filed twice: %+v", open)
	}
}

// #753: a repair that fails says why — in the journal, red, and as the
// card's because; one approved on the page waits for the work before it,
// like a human's approve at the shell.
func TestAFailedRepairSaysWhyAndAPageApprovalWaitsForTheLock(t *testing.T) {
	ctx := context.Background()
	env, _ := testBox(t)
	failedWhoami(t)
	st, _ := cli.LoadState(env)
	mcp.Call(ctx, env, st, "ops", "laib_repair", map[string]any{"probe": "unit", "service": "whoami"})
	run := sysd.Run
	t.Cleanup(func() { sysd.Run = run })
	var restarted atomic.Bool
	sysd.Run = func(ctx context.Context, name string, args ...string) (string, error) {
		if slices.Contains(args, "restart") {
			restarted.Store(true)
			return "Job for whoami.service failed", errors.New("exit status 1")
		}
		return run(ctx, name, args...)
	}
	unlock, _ := apply.TakeLock(env, st, "apply") // the work before it
	inboxMux(env)("POST", "/inbox/1/approve", nil)
	time.Sleep(300 * time.Millisecond)
	if restarted.Load() {
		t.Fatal("the repair ran while another run held the lock")
	}
	unlock()
	p := &proposal.Proposal{}
	until(t, "the repair ends", func() bool { p, _ = proposal.Get(cli.InboxDir(env), 1); return p.Because != "" })
	if !restarted.Load() || !strings.HasPrefix(p.Because, "fehlgeschlagen: ") {
		t.Fatalf("restarted %v, because %q", restarted.Load(), p.Because)
	}
	es, _ := journal.Read(env.Paths.Journal, time.Time{})
	if !slices.ContainsFunc(es, func(e journal.Entry) bool {
		return e.Verb == "repair" && e.Result == "red" && strings.Contains(e.Detail, "laib restart whoami") && strings.Contains(e.Detail, strings.TrimPrefix(p.Because, "fehlgeschlagen: "))
	}) {
		t.Fatalf("the journal must say which repair failed and why: %+v", es)
	}
	if page := inboxMux(env)("GET", "/inbox", nil); !strings.Contains(page, p.Because) {
		t.Fatalf("the card does not say why: %s", page)
	}
}
