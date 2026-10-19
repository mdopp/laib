package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/diag"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/sysd"
)

// #441 (owner, 2026-10-19: "Das Interface ist jetzt der Chat"): a diag
// finding whose fix is one laib verb can become an inbox proposal —
// "Reparieren". A chat hand or the Operator proposes it, a human approves it,
// and only then does laib run the verb, as the human who approved. Nothing
// runs on the proposal alone; a proposal file that names anything but the
// repairs laib offers is refused at approval.

// failedWhoami makes whoami's unit failed until something restarts it, and
// records every systemctl call.
func failedWhoami(t *testing.T) (calls func() []string) {
	t.Helper()
	var mu sync.Mutex
	var log []string
	restarted := false
	prev := sysd.Run
	t.Cleanup(func() { sysd.Run = prev })
	sysd.Run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "systemctl" {
			mu.Lock()
			log = append(log, strings.Join(args, " "))
			if slices.Contains(args, "restart") && slices.Contains(args, "whoami.service") {
				restarted = true
			}
			r := restarted
			mu.Unlock()
			if slices.Contains(args, "is-active") && slices.Contains(args, "whoami.service") && !r {
				return "failed", nil
			}
		}
		return prev(ctx, name, args...)
	}
	return func() []string { mu.Lock(); defer mu.Unlock(); return slices.Clone(log) }
}

func restartedWhoami(calls []string) bool {
	return slices.ContainsFunc(calls, func(c string) bool { return strings.Contains(c, "restart") && strings.Contains(c, "whoami.service") })
}

func TestADiagFindingCarriesItsRepair(t *testing.T) {
	env, _ := testBox(t)
	failedWhoami(t)
	st, _ := cli.LoadState(env)
	for _, f := range diag.Probes(context.Background(), env, st, "whoami") {
		if f.Probe == "unit" {
			if !slices.Equal(f.Repair, []string{"restart", "whoami"}) {
				t.Fatalf("a failed unit's repair is a restart of its service, got %q", f.Repair)
			}
			b, _ := json.Marshal(f)
			if !strings.Contains(string(b), `"repair":["restart","whoami"]`) {
				t.Fatalf("diag --json does not carry the repair: %s", b)
			}
			return
		}
	}
	t.Fatal("no unit finding for the failed whoami")
}

func TestARepairIsProposedByTheOperatorAndRunsOnlyAfterAHumanApproves(t *testing.T) {
	ctx := context.Background()
	env, _ := testBox(t)
	calls := failedWhoami(t)
	st, _ := cli.LoadState(env)
	out, code := mcp.Call(ctx, env, st, "ops", "laib_repair", map[string]any{"probe": "unit", "service": "whoami"})
	if code != cli.ExitOK {
		t.Fatalf("the Operator's repair proposal: exit %d: %s", code, out)
	}
	if restartedWhoami(calls()) {
		t.Fatal("the repair ran on the proposal alone")
	}
	open, _ := proposal.List(cli.InboxDir(env), true)
	if len(open) != 1 || open[0].Class != "human" || !strings.HasPrefix(open[0].Files[0], "repairs/") || !strings.Contains(open[0].Reason, "laib restart whoami") {
		t.Fatalf("want one open proposal for a human that names the command it runs: %+v", open)
	}
	id := strconv.Itoa(open[0].ID)
	if out, code := mcp.Call(ctx, env, st, "ops", "laib_approve", map[string]any{"args": id}); code != cli.ExitRefused {
		t.Fatalf("an agent approved a repair: exit %d: %s", code, out)
	}
	if restartedWhoami(calls()) {
		t.Fatal("the repair ran after a refused approval")
	}
	if code := inbox.Decide(ctx, &cliEnv{Paths: env.Paths, Actor: "human:test"}, []string{id}, true); code != cli.ExitOK {
		t.Fatalf("the human's approval: exit %d", code)
	}
	if !restartedWhoami(calls()) {
		t.Fatalf("approved, but whoami was not restarted: %v", calls())
	}
	es, _ := journal.Read(env.Paths.Journal, time.Time{})
	if !slices.ContainsFunc(es, func(e journal.Entry) bool {
		return e.Verb == "restart" && e.Actor == "human:test" && e.Service == "whoami"
	}) {
		t.Fatalf("the restart must run as the human who approved: %+v", es)
	}
	if p, _ := proposal.Get(cli.InboxDir(env), open[0].ID); p.Status != "approved" {
		t.Fatalf("status %q, want approved", p.Status)
	}
}

func TestARepairFileWithAnotherCommandIsRefusedAtApproval(t *testing.T) {
	ctx := context.Background()
	env, _ := testBox(t)
	calls := failedWhoami(t)
	st, _ := cli.LoadState(env)
	patch := "--- /dev/null\n+++ b/repairs/x.md\n@@ -0,0 +1,3 @@\n+# Reparieren: whoami\n+\n+laib remove whoami\n"
	if out, code := mcp.Call(ctx, env, st, "ops", "laib_propose", map[string]any{"args": `"whoami reparieren"`, "patch": patch}); code != cli.ExitOK {
		t.Fatalf("propose: exit %d: %s", code, out)
	}
	open, _ := proposal.List(cli.InboxDir(env), true)
	if len(open) != 1 || open[0].Class != "human" {
		t.Fatalf("a hand-made repair file must wait for a human: %+v", open)
	}
	code := inbox.Decide(ctx, &cliEnv{Paths: env.Paths, Actor: "human:test"}, []string{strconv.Itoa(open[0].ID)}, true)
	if code == cli.ExitOK {
		t.Fatal("a repair file naming `laib remove` ran")
	}
	if _, err := os.Stat(filepath.Join(env.Paths.State, "services", "whoami")); err != nil || restartedWhoami(calls()) {
		t.Fatal("something ran for a repair laib does not offer")
	}
	es, _ := journal.Read(env.Paths.Journal, time.Time{})
	if !slices.ContainsFunc(es, func(e journal.Entry) bool {
		return e.Verb == "repair" && e.Result == "red" && strings.Contains(e.Detail, "laib remove whoami")
	}) {
		t.Fatalf("the journal must say which command was refused: %+v", es)
	}
}

func TestAFindingWithoutARepairIsNoProposal(t *testing.T) {
	ctx := context.Background()
	env, _ := testBox(t)
	st, _ := cli.LoadState(env)
	out, code := mcp.Call(ctx, env, st, "ops", "laib_repair", map[string]any{"probe": "unit", "service": "whoami"})
	if code == cli.ExitOK || !strings.Contains(out, "keine Reparatur") {
		t.Fatalf("whoami runs: there is nothing to repair, want a refusal that says so: exit %d: %s", code, out)
	}
	if ps, _ := proposal.List(cli.InboxDir(env), false); len(ps) != 0 {
		t.Fatalf("a proposal without a finding: %+v", ps)
	}
}
