package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/backup"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

// redDataRun is #853's red path: vault, a service with data, gets a new
// image; the run snapshots it, the new image does not come up, the run is
// taken back. It returns the box, the taken-back proposal and the restic
// calls the box made.
func redDataRun(t *testing.T) (env *cliEnv, p *proposal.Proposal, calls func() []string, snappedAt time.Time) {
	t.Helper()
	ctx := context.Background()
	env, _ = testBox(t)
	svc := filepath.Join(env.Paths.State, "services", "vault")
	os.MkdirAll(svc, 0o755)
	data := filepath.Join(t.TempDir(), "vault")
	os.MkdirAll(data, 0o755)
	for name, content := range map[string]string{
		"laib.yml":        "name: vault\ndata:\n  - path: " + data + "\n    quiesce: true\n",
		"values.yml":      "IMAGE: docker.io/vaultwarden/server:latest\n",
		"vault.container": "[Container]\nImage=${IMAGE}\n\n[Install]\nWantedBy=default.target\n",
	} {
		os.WriteFile(filepath.Join(svc, name), []byte(content), 0o644)
	}
	if err := proposal.CommitDirty(ctx, env.Paths.State, "human:test"); err != nil {
		t.Fatal(err)
	}
	st, _ := cli.LoadState(env)
	if code := apply.ApplyLocked(ctx, &cliEnv{Paths: env.Paths, Actor: "human:test", Out: io.Discard}, st, "", "vault"); code != cli.ExitOK {
		t.Fatalf("fixture apply: %d", code)
	}
	pin := "docker.io/vaultwarden/server:latest@sha256:b2"
	if out, code := mcp.Call(ctx, env, st, "ops", "laib_propose", map[string]any{"reason": "vault: neues Image", "service": "vault", "key": "IMAGE", "value": pin}); code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	open, _ := proposal.List(cli.InboxDir(env), true)
	p = open[0]
	// the new image does not come up; the old one does
	run := sysd.Run
	sysd.Run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "systemctl" && slices.Contains(args, "is-active") && slices.Contains(args, "vault.service") {
			if b, _ := os.ReadFile(filepath.Join(env.Paths.Quadlet, "vault", "vault.container")); strings.Contains(string(b), pin) {
				return "failed", nil
			}
		}
		return run(ctx, name, args...)
	}
	realRestic := backup.ResticRun
	t.Cleanup(func() { backup.ResticRun = realRestic })
	var mu sync.Mutex
	var restic []string
	backup.ResticRun = func(_ context.Context, _ *state.State, _ string, args ...string) (string, error) {
		mu.Lock()
		restic = append(restic, strings.Join(args, " "))
		mu.Unlock()
		if args[0] == "backup" {
			return "Files: 3 new\nsnapshot 4f9a2c1e saved", nil
		}
		return "", nil
	}
	calls = func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(restic)
	}
	before := time.Now()
	if code := inbox.Approve(ctx, &cliEnv{Paths: env.Paths, Actor: "human:test", Out: io.Discard, ErrOut: io.Discard}, []string{fmt.Sprint(p.ID)}); code == cli.ExitOK {
		t.Fatal("the new image's run must end red")
	}
	p, _ = proposal.Get(cli.InboxDir(env), p.ID)
	if p.Outcome != "rolled-back" {
		t.Fatalf("the red run is not taken back: %+v", p)
	}
	if b, _ := os.ReadFile(filepath.Join(env.Paths.Quadlet, "vault", "vault.container")); strings.Contains(string(b), pin) {
		t.Fatal("the old program is not back")
	}
	return env, p, calls, before
}

// #853 (owner 2026-10-28, b): a red run with data restores nothing by
// itself; its card says that the old program runs again, that the data are
// as the update left them and that the snapshot from HH:MM is there, and
// offers two ways on: „Sicherung zurückspielen“ and „Nach vorne reparieren“,
// each a proposal laib files, one tap each.
func TestARedRunWithDataSaysTheDataStayAndOffersTwoWays(t *testing.T) {
	env, p, calls, before := redDataRun(t)
	if slices.ContainsFunc(calls(), func(c string) bool { return strings.HasPrefix(c, "restore") }) {
		t.Fatalf("the red run restored by itself: %q", calls())
	}
	at := ""
	for _, c := range []time.Time{before, time.Now()} { // the snapshot's minute, as the box shows it
		if strings.Contains(p.Because, "die Sicherung von "+c.Format("15:04")+" liegt bereit") {
			at = c.Format("15:04")
		}
	}
	want := "Das alte Programm läuft wieder. Die Daten sind auf dem Stand nach dem Update; die Sicherung von " + at + " liegt bereit."
	if at == "" || !strings.Contains(p.Because, want) {
		t.Fatalf("the card does not say where the data are and which snapshot is there: %q", p.Because)
	}
	if p.Restore == 0 || p.Forward == 0 || p.Restore == p.Forward {
		t.Fatalf("no restore and forward proposal filed: %+v", p)
	}
	restore, err := proposal.Get(cli.InboxDir(env), p.Restore)
	if err != nil || restore.Status != "open" || restore.Actor != "laib" {
		t.Fatalf("the restore is not laib's open proposal: %+v %v", restore, err)
	}
	if _, err := os.Stat(filepath.Join(env.Paths.State, restore.Files[0])); err == nil {
		t.Fatal("the restore is on main before anyone approved it")
	}
	if diff := proposal.Diff(context.Background(), env.Paths.State, restore); !strings.Contains(diff, "laib restore 4f9a2c1e vault") {
		t.Fatalf("the restore does not name the run's snapshot: %s", diff)
	}
	forward, err := proposal.Get(cli.InboxDir(env), p.Forward)
	if err != nil || forward.Status != "open" || forward.Actor != "laib" || len(forward.Snapshot) != 0 {
		t.Fatalf("the forward is not laib's open proposal without a new snapshot: %+v %v", forward, err)
	}
	sha, _ := sysd.Run(context.Background(), "git", "-C", env.Paths.State, "log", "--format=%H", "--grep=^rollback #"+fmt.Sprint(p.ID)+"$", "-n", "1", "main")
	if diff := proposal.Diff(context.Background(), env.Paths.State, forward); strings.TrimSpace(sha) == "" || !strings.Contains(diff, "laib rollback "+strings.TrimSpace(sha)) {
		t.Fatalf("the forward does not put the run's change on again (%q): %s", sha, diff)
	}
	for _, q := range []*proposal.Proposal{restore, forward} {
		c := proposal.Card(q, "box.test")
		for _, bad := range []string{"laib ", "restore", "rollback", "4f9a2c1e"} {
			if strings.Contains(c.Summary+c.Why+c.Reversible+strings.Join(c.Impact, " "), bad) {
				t.Errorf("#%d's card speaks technical (%q): %+v", q.ID, bad, c)
			}
		}
	}
	if c := proposal.Card(restore, ""); !strings.Contains(c.Summary, "Sicherung von "+at) || !strings.Contains(c.Summary, "zurückspielen") {
		t.Errorf("the restore's headline (the owner's message) does not name the snapshot: %q", c.Summary)
	}
	if c := proposal.Card(forward, ""); !strings.Contains(strings.Join(c.Impact, " "), "Sicherung von "+at+" bleibt") || !strings.Contains(strings.Join(c.Impact, " "), "Operator") {
		t.Errorf("the forward's card does not say the restore stays and who works it: %+v", c.Impact)
	}
	do := inboxMux(env)
	body := do("GET", "/inbox", nil)
	for _, w := range []string{want, fmt.Sprintf("/inbox/%d/approve", p.Restore), "Sicherung zurückspielen", fmt.Sprintf("/inbox/%d/approve", p.Forward), "Nach vorne reparieren"} {
		if !strings.Contains(body, w) {
			t.Errorf("the red card lacks %q:\n%s", w, body)
		}
	}
	if strings.Contains(body, "Daten bleiben") {
		t.Errorf("the Inbox still promises the data stay:\n%s", body)
	}
}

// #853: „Sicherung zurückspielen“ is one tap: the snapshot the run took
// comes back, the old program stays.
func TestSicherungZurückspielenRestoresTheRunsSnapshot(t *testing.T) {
	env, p, calls, _ := redDataRun(t)
	if code := inbox.Approve(context.Background(), &cliEnv{Paths: env.Paths, Actor: "human:test", Out: io.Discard, ErrOut: io.Discard}, []string{fmt.Sprint(p.Restore)}); code != cli.ExitOK {
		t.Fatalf("the restore's approval: %d", code)
	}
	if !slices.ContainsFunc(calls(), func(c string) bool { return strings.HasPrefix(c, "restore 4f9a2c1e:") }) {
		t.Fatalf("the run's snapshot was not restored: %q", calls())
	}
	if r, _ := proposal.Get(cli.InboxDir(env), p.Restore); r.Because != "" {
		t.Fatalf("the restore failed: %q", r.Because)
	}
}

// #853: „Nach vorne reparieren“ keeps the data, puts the new version on
// again and leaves a red line the Operator opens an incident on (#792);
// the restore stays open beside it.
func TestNachVorneReparierenPutsTheNewVersionOnAndKeepsTheRestore(t *testing.T) {
	env, p, calls, _ := redDataRun(t)
	n := len(calls())
	since := time.Now().Add(-time.Second)
	if code := inbox.Approve(context.Background(), &cliEnv{Paths: env.Paths, Actor: "human:test", Out: io.Discard, ErrOut: io.Discard}, []string{fmt.Sprint(p.Forward)}); code == cli.ExitOK {
		t.Fatal("the new version is still red; the forward cannot end green")
	}
	if b, _ := os.ReadFile(filepath.Join(env.Paths.Quadlet, "vault", "vault.container")); !strings.Contains(string(b), "@sha256:b2") {
		t.Fatalf("the new version is not on again: %s", b)
	}
	if slices.ContainsFunc(calls()[n:], func(c string) bool { return strings.HasPrefix(c, "restore") }) {
		t.Fatalf("the forward touched the data: %q", calls()[n:])
	}
	es, _, _ := journal.ReadCounted(env.Paths.Journal, since)
	if !slices.ContainsFunc(es, func(e journal.Entry) bool {
		return e.Result == "red" && e.Actor == "human:test" && strings.Contains(e.Detail, "vault")
	}) {
		t.Fatalf("no red line names vault for the Operator's incident: %+v", es)
	}
	if r, _ := proposal.Get(cli.InboxDir(env), p.Restore); r.Status != "open" {
		t.Fatalf("the restore is no longer there: %+v", r)
	}
	f, _ := proposal.Get(cli.InboxDir(env), p.Forward)
	if !strings.Contains(f.Because, "neue Version") || !strings.Contains(f.Because, "Sicherung") || strings.Contains(f.Because, "exit") {
		t.Fatalf("the forward's card does not say where it stands: %q", f.Because)
	}
}

// #853: a red run without data offers nothing to restore.
func TestARedRunWithoutDataOffersNoRestore(t *testing.T) {
	env, _ := testBox(t)
	p := proposeService(t, env, "w1")
	run := sysd.Run
	sysd.Run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "systemctl" && slices.Contains(args, "is-active") && slices.Contains(args, "w1.service") {
			return "failed", nil
		}
		return run(ctx, name, args...)
	}
	inbox.Approve(context.Background(), &cliEnv{Paths: env.Paths, Actor: "human:test", Out: io.Discard, ErrOut: io.Discard}, []string{fmt.Sprint(p.ID)})
	got, _ := proposal.Get(cli.InboxDir(env), p.ID)
	if got.Outcome != "rolled-back" || got.Restore != 0 || got.Forward != 0 || strings.Contains(got.Because, "Sicherung") {
		t.Fatalf("a run without data offers a restore: %+v", got)
	}
}

// #853: from repairs/ only a snapshot by its id of a service with data
// comes back, and only a commit by its hash is taken back again.
func TestOnlyASnapshotIdAndACommitHashAreRepairs(t *testing.T) {
	env, _ := testBox(t)
	st, _ := cli.LoadState(env)
	st.Services["vault"] = &state.Service{Name: "vault", Contract: state.Contract{Data: []state.DataPath{{Path: t.TempDir()}}}}
	for argv, want := range map[string]bool{
		"restore 4f9a2c1e vault":  true,
		"restore latest vault":    false,
		"restore --box 4f9a2c1e":  false,
		"restore 4f9a2c1e whoami": false, // no data
		"restore 4f9a2c1e nobody": false,
		"rollback 4f9a2c1e77":     true,
		"rollback HEAD":           false,
		"rollback --hard":         false,
	} {
		if got := inbox.Repairable(st, strings.Fields(argv)); got != want {
			t.Errorf("Repairable(%q) = %v, want %v", argv, got, want)
		}
	}
}
