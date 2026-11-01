package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/registry"
	"github.com/mdopp/laib/internal/skill"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/skills"
)

// #932: v0.56.0 gave the built-in skill durchsicht the tool state; the
// Kraftwerk's policy.yml narrows ops to a verb list without it, and no
// grant reached the Inbox after the update. #575 compares the box's lists
// with state.DefaultPolicy's, whose ops has no list (it may use every
// verb), so a narrowed ops never lacked anything there. Now the update run
// also proposes what the built-in skills' own tools need from a narrowed
// ops, with the skill's name as the reason, once.
func TestTheUpdateRunProposesTheGrantABuiltInSkillsNewToolNeeds(t *testing.T) {
	env, _ := testBox(t)
	ctx := context.Background()
	var verbs []string // every built-in skill's tools but durchsicht's state: the box before the release
	for _, s := range skill.Load(skills.FS, t.TempDir()) {
		for _, v := range s.Tools {
			if v != "state" && !slices.Contains(verbs, v) {
				verbs = append(verbs, v)
			}
		}
	}
	pol := filepath.Join(env.Paths.State, "policy.yml")
	old := strings.Replace(state.DefaultPolicy, "ops: {ceiling: apply}", "ops: {ceiling: apply, verbs: ["+strings.Join(verbs, ", ")+"]}", 1)
	if old == state.DefaultPolicy {
		t.Fatal("the default ops line moved")
	}
	os.WriteFile(pol, []byte(old), 0o644)
	if err := proposal.CommitDirty(ctx, env.Paths.State, "human:test"); err != nil {
		t.Fatal(err)
	}
	// filed by hand with diag's command (the box's Inbox #286): not asked twice
	env.Actor = "human:test"
	if code := inbox.Propose(ctx, env, []string{"--grant", "ops", "state", "Fähigkeit durchsicht braucht state"}); code != cli.ExitOK {
		t.Fatalf("--grant: exit %d", code)
	}
	env.Actor = "laib"
	if code := registry.Updates(ctx, env, []string{"--run"}); code != cli.ExitOK {
		t.Fatalf("updates --run: exit %d", code)
	}
	open, _ := proposal.List(cli.InboxDir(env), true)
	if len(open) != 1 {
		t.Fatalf("the hand-filed grant was asked again: %+v", open)
	}
	if err := proposal.Reject(ctx, env.Paths.State, cli.InboxDir(env), open[0], "human:test", "später"); err != nil {
		t.Fatal(err)
	}
	for range 2 { // the second run finds the first proposal open
		if code := registry.Updates(ctx, env, []string{"--run"}); code != cli.ExitOK {
			t.Fatalf("updates --run: exit %d", code)
		}
	}
	open, _ = proposal.List(cli.InboxDir(env), true)
	if len(open) != 1 || !strings.Contains(open[0].Reason, "ops +state") || !strings.Contains(open[0].Reason, "durchsicht") || open[0].Class != "human" {
		t.Fatalf("want one human proposal granting ops state for durchsicht: %+v", open)
	}
	diff := proposal.Diff(ctx, env.Paths.State, open[0])
	if strings.Count(diff, "\n+  ") != 1 || !strings.Contains(diff, ", state]}") {
		t.Fatalf("not exactly the ops line:\n%s", diff)
	}
}

// #978: durchsicht now asks laib planned (vorfall's capable model may, an
// extra the grant does not ask for, as diag does not); the Kraftwerk's ops
// list (policy.yml, 2026-11-01) has no planned, so the update that brings
// the skill proposes it, once, naming the skill that needs it.
func TestTheUpdateRunProposesPlannedForTheKraftwerksOps(t *testing.T) {
	env, _ := testBox(t)
	ctx := context.Background()
	box := "ops: {ceiling: apply, verbs: [status, inbox, diag, inspect, read, look, logs, journal, context, skill, backups, updates, plan, propose, repair, note, escalate, apply, backup, restart, wish, state]}"
	pol := filepath.Join(env.Paths.State, "policy.yml")
	os.WriteFile(pol, []byte(strings.Replace(state.DefaultPolicy, "ops: {ceiling: apply}", box, 1)), 0o644)
	if err := proposal.CommitDirty(ctx, env.Paths.State, "human:test"); err != nil {
		t.Fatal(err)
	}
	env.Actor = "laib"
	if code := registry.Updates(ctx, env, []string{"--run"}); code != cli.ExitOK {
		t.Fatalf("updates --run: exit %d", code)
	}
	open, _ := proposal.List(cli.InboxDir(env), true)
	if len(open) != 1 || !strings.Contains(open[0].Reason, "ops +planned") || !strings.Contains(open[0].Reason, "durchsicht") {
		t.Fatalf("want one proposal granting ops planned for durchsicht: %+v", open)
	}
}
