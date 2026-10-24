package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/sysd"
)

// proposeService files a proposal by pi that adds one small service.
func proposeService(t *testing.T, env *cliEnv, name string) *proposal.Proposal {
	t.Helper()
	var patch strings.Builder
	for file, lines := range map[string][]string{
		"laib.yml":          {"name: " + name},
		"values.yml":        {"IMAGE: docker.io/traefik/whoami:v1.10"},
		name + ".container": {"[Container]", "Image=${IMAGE}", "", "[Install]", "WantedBy=default.target"},
	} {
		path := "services/" + name + "/" + file
		fmt.Fprintf(&patch, "diff --git a/%s b/%s\nnew file mode 100644\n--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n+%s\n", path, path, path, len(lines), strings.Join(lines, "\n+"))
	}
	p, err := proposal.CreateFromEdit(context.Background(), env.Paths.State, cli.InboxDir(env), "pi", "neuer Dienst "+name, proposal.Edit{Patch: patch.String()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// applies are the apply runs the journal holds.
func applies(t *testing.T, env *cliEnv) []journal.Entry {
	t.Helper()
	es, _, err := journal.ReadCounted(env.Paths.Journal, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return slices.DeleteFunc(es, func(e journal.Entry) bool { return e.Verb != "apply" })
}

// #609: approving while an apply holds the lock answers at once; the
// proposal is approved and waits for the next run, which the holder's
// release starts.
func TestApproveDuringAnApplyAnswersAtOnce(t *testing.T) {
	ctx := context.Background()
	env, _ := testBox(t)
	st, _ := cli.LoadState(env)
	p := proposeService(t, env, "w1")
	unlock, code := apply.TakeLock(env, st, "apply")
	if code != cli.ExitOK {
		t.Fatal(code)
	}
	var out bytes.Buffer
	start := time.Now()
	if code := inbox.Approve(ctx, &cliEnv{Paths: env.Paths, Actor: "human:test", Out: &out, ErrOut: io.Discard}, []string{fmt.Sprint(p.ID)}); code != cli.ExitOK {
		t.Fatalf("approve during an apply: exit %d", code)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("approve waited %s for the lock", d)
	}
	if got, _ := proposal.Get(cli.InboxDir(env), p.ID); got.Status != "approved" || got.Outcome != "waiting" {
		t.Fatalf("approved at once and waiting for its run: %+v", got)
	}
	if !strings.Contains(out.String(), "nächsten Lauf") {
		t.Fatalf("the approver is not told it runs with the next run: %q", out.String())
	}
	if _, err := os.Stat(filepath.Join(env.Paths.Quadlet, "w1", "w1.container")); err == nil {
		t.Fatal("applied while another apply held the lock")
	}
	unlock() // the holder's release starts the run
	if _, err := os.Stat(filepath.Join(env.Paths.Quadlet, "w1", "w1.container")); err != nil {
		t.Fatal("the release of the lock did not apply what waited")
	}
	if got, _ := proposal.Get(cli.InboxDir(env), p.ID); got.Outcome == "waiting" || got.Batch == "" {
		t.Fatalf("the run that carried it is not recorded: %+v", got)
	}
}

// A human at the shell who approves (laib add, laib approve) means it to be
// applied when the command returns: it waits for the lock, bounded, and
// applies, instead of leaving it to the next run. The VM test's adds raced
// the converge timer and restarted a unit not there yet (train 2026-10-24f).
func TestAShellApproveWaitsForTheLockAndApplies(t *testing.T) {
	ctx := context.Background()
	env, _ := testBox(t)
	st, _ := cli.LoadState(env)
	p := proposeService(t, env, "w2")
	unlock, code := apply.TakeLock(env, st, "apply")
	if code != cli.ExitOK {
		t.Fatal(code)
	}
	go func() { time.Sleep(300 * time.Millisecond); unlock() }()
	shell := &cliEnv{Paths: env.Paths, Actor: "human:test", Out: io.Discard, ErrOut: io.Discard, Wait: 30 * time.Second}
	if code := inbox.Approve(ctx, shell, []string{fmt.Sprint(p.ID)}); code != cli.ExitOK {
		t.Fatalf("exit %d", code)
	}
	if _, err := os.Stat(filepath.Join(env.Paths.Quadlet, "w2", "w2.container")); err != nil {
		t.Fatal("a shell approve returned before its change was applied")
	}
}

// #609: three approvals during one apply give exactly one more apply,
// which carries all three; journal and proposals name that run.
func TestThreeApprovalsDuringOneApplyGiveOneMoreApply(t *testing.T) {
	ctx := context.Background()
	env, _ := testBox(t)
	st, _ := cli.LoadState(env)
	ps := []*proposal.Proposal{proposeService(t, env, "w1"), proposeService(t, env, "w2"), proposeService(t, env, "w3")}
	unlock, _ := apply.TakeLock(env, st, "apply")
	for _, p := range ps {
		if code := inbox.Approve(ctx, &cliEnv{Paths: env.Paths, Actor: "human:test", Out: io.Discard, ErrOut: io.Discard}, []string{fmt.Sprint(p.ID)}); code != cli.ExitOK {
			t.Fatalf("approve #%d: exit %d", p.ID, code)
		}
	}
	if n := len(applies(t, env)); n != 0 {
		t.Fatalf("%d applies while the lock was held", n)
	}
	unlock()
	as := applies(t, env)
	if len(as) != 1 || as[0].Result != "green" || !regexp.MustCompile(`^Freigaben #1, #2, #3 \([0-9a-f]{7,}\.\.[0-9a-f]{7,}\)$`).MatchString(as[0].Reason) {
		t.Fatalf("want one green apply carrying #1, #2, #3: %+v", as)
	}
	batch := ""
	for _, p := range ps {
		got, _ := proposal.Get(cli.InboxDir(env), p.ID)
		if got.Batch == "" || batch != "" && got.Batch != batch || !strings.Contains(as[0].Reason, got.Batch) {
			t.Fatalf("#%d not attributed to the run %q: %+v", p.ID, as[0].Reason, got)
		}
		batch = got.Batch
		if _, err := os.Stat(filepath.Join(env.Paths.Quadlet, fmt.Sprint("w", p.ID), fmt.Sprint("w", p.ID, ".container"))); err != nil {
			t.Fatalf("#%d not on the box", p.ID)
		}
	}
}

// #609: a red run rolls back every proposal it carried, and each says
// with which others it went.
func TestARedBatchRollsBackAllAndSaysSoForEach(t *testing.T) {
	ctx := context.Background()
	env, _ := testBox(t)
	st, _ := cli.LoadState(env)
	ps := []*proposal.Proposal{proposeService(t, env, "w1"), proposeService(t, env, "w2"), proposeService(t, env, "w3")}
	run := sysd.Run
	sysd.Run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "systemctl" && slices.Contains(args, "is-active") && slices.Contains(args, "w2.service") {
			return "failed", nil // w2 does not come up
		}
		return run(ctx, name, args...)
	}
	unlock, _ := apply.TakeLock(env, st, "apply")
	for _, p := range ps {
		inbox.Approve(ctx, &cliEnv{Paths: env.Paths, Actor: "human:test", Out: io.Discard, ErrOut: io.Discard}, []string{fmt.Sprint(p.ID)})
	}
	unlock()
	for _, p := range ps {
		got, _ := proposal.Get(cli.InboxDir(env), p.ID)
		if got.Outcome != "rolled-back" || !strings.HasPrefix(got.Batch, "#1, #2, #3 (") || strings.Contains(got.Batch, "fatal") {
			t.Fatalf("#%d not rolled back with its run: %+v", p.ID, got)
		}
		if _, err := os.Stat(filepath.Join(env.Paths.State, "services", "w"+fmt.Sprint(p.ID))); err == nil {
			t.Fatalf("#%d still in the state", p.ID)
		}
		if _, err := os.Stat(filepath.Join(env.Paths.Quadlet, "w"+fmt.Sprint(p.ID))); err == nil {
			t.Fatalf("#%d still on the box", p.ID)
		}
	}
	es, _, _ := journal.ReadCounted(env.Paths.Journal, time.Now().Add(-time.Hour))
	for _, want := range []string{"#1 zurückgenommen, mit dem ganzen Lauf #1, #2, #3", "#2 zurückgenommen, mit dem ganzen Lauf #1, #2, #3", "#3 zurückgenommen, mit dem ganzen Lauf #1, #2, #3"} {
		if !slices.ContainsFunc(es, func(e journal.Entry) bool { return e.Verb == "rollback" && strings.Contains(e.Detail, want) }) {
			t.Fatalf("the journal does not say %q", want)
		}
	}
}
