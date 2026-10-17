package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/sysd"
)

// #328: a secret a post-deploy hook hands back (laib-secret:) was saved
// after the apply's snapshot commit, so main had secrets.age modified and
// uncommitted. A plain `laib propose` then committed it into the proposal
// branch and put main's old secrets.age back; a reject dropped the key —
// one a hook mints only once (Immich's API key). The hand-back is on main
// before apply says green, and no proposal carries it off.
func TestAHandedBackSecretSurvivesAProposeAndReject(t *testing.T) {
	ctx := context.Background()
	env, _ := testBox(t)
	hook := filepath.Join(env.paths.State, "services", "whoami", "post-deploy")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := proposal.CommitDirty(ctx, env.paths.State, "human:test"); err != nil {
		t.Fatal(err)
	}
	realHook := apply.RunHook
	t.Cleanup(func() { apply.RunHook = realHook })
	apply.RunHook = func(context.Context, apply.HookSpec) (string, error) {
		return "laib-secret: WHOAMI_API_KEY=minted-once\n", nil
	}
	st, err := loadState(env)
	if err != nil {
		t.Fatal(err)
	}
	if code := runApply(ctx, env, st, "", "add whoami"); code != exitOK {
		t.Fatalf("apply: exit %d", code)
	}
	if status, _ := sysd.Run(ctx, "git", "-C", env.paths.State, "status", "--porcelain", "--", "secrets.age"); status != "" {
		t.Fatalf("apply said green with the hand-back uncommitted: %q", status)
	}
	// a human's edit on the box, proposed as a tree proposal, then rejected
	box := filepath.Join(env.paths.State, "box.yml")
	b, _ := os.ReadFile(box)
	os.WriteFile(box, append(b, []byte("# an edit\n")...), 0o644)
	if code := cmdPropose(ctx, env, []string{"an edit on the box"}); code != exitOK {
		t.Fatalf("propose: exit %d", code)
	}
	open, _ := proposal.List(inboxDir(env), true)
	if len(open) != 1 {
		t.Fatalf("want one open proposal: %+v", open)
	}
	if code := cmdReject(ctx, env, []string{strconv.Itoa(open[0].ID)}); code != exitOK {
		t.Fatalf("reject: exit %d", code)
	}
	sec, err := secrets.Load(env.paths.Secrets, env.paths.Key)
	if err != nil {
		t.Fatal(err)
	}
	if sec["WHOAMI_API_KEY"] != "minted-once" {
		t.Fatalf("the handed-back key is gone after propose + reject (%d secrets left)", len(sec))
	}
}
