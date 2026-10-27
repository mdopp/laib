package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/state"
)

// legacyApprover is the relay's hand as a box from before #778 has it in policy.yml.
const legacyApprover = "  approver: {ceiling: apply, human: true, verbs: [approve, reject, inbox, status, note]}   # the Signal relay (#48)\n"

// #778 (owner, 2026-10-26, „B3“): approvals happen only in the Inbox. A
// policy.yml from before, with the relay's approver hand and its
// `human: true`, still loads; the mark counts for nothing: the hand cannot
// approve what only a human may (here a script on the host), and the Inbox
// (human:<Remote-User>) can. The approved script runs once, in the hooks
// image (#36).
func TestApprovalsAreTheInboxsAlone(t *testing.T) {
	ctx := context.Background()
	env, data := testBox(t)
	pol := filepath.Join(env.Paths.State, "policy.yml")
	raw, _ := os.ReadFile(pol)
	os.WriteFile(pol, []byte(strings.Replace(string(raw), "  human: {ceiling: destroy}\n", legacyApprover+"  human: {ceiling: destroy}\n", 1)), 0o644)
	if err := proposal.CommitDirty(ctx, env.Paths.State, "human:test"); err != nil {
		t.Fatal(err)
	}
	ran := filepath.Join(data, "ran-on-the-host")
	var hooks []string
	realHook := apply.RunHook
	t.Cleanup(func() { apply.RunHook = realHook })
	apply.RunHook = func(_ context.Context, h apply.HookSpec) (string, error) {
		hooks = append(hooks, h.Image+" "+strings.Join(h.Command, " "))
		return "", nil
	}
	st, err := cli.LoadState(env)
	if err != nil {
		t.Fatalf("a policy.yml with the old approver hand must still load: %v", err)
	}
	if _, ok := st.Policy.Hands["approver"]; !ok {
		t.Fatal("the old hand is the owner's to remove, not the loader's")
	}
	patch := "diff --git a/services/whoami/post-deploy b/services/whoami/post-deploy\nnew file mode 100755\n--- /dev/null\n+++ b/services/whoami/post-deploy\n@@ -0,0 +1,2 @@\n+#!/bin/sh\n+touch \"$LAIB_DATA_DIR/ran-on-the-host\"\n"
	if out, code := mcp.Call(ctx, env, st, "bote", "laib_propose", map[string]any{"args": `"whoami braucht ein Einrichtungs-Skript"`, "patch": patch}); code != cli.ExitOK {
		t.Fatalf("propose: %d %s", code, out)
	}
	open, _ := proposal.List(cli.InboxDir(env), true)
	id := strconv.Itoa(open[0].ID)
	if out, code := mcp.Call(ctx, env, st, "approver", "laib_approve", map[string]any{"args": id}); code != cli.ExitRefused || !strings.Contains(out, "nur ein Mensch") {
		t.Fatalf("the old approver hand approved a script on the host: %d %s", code, out)
	}
	if len(hooks) != 0 {
		t.Fatalf("a refused verdict ran the script: %v", hooks)
	}
	if code := cmdApproveFor(t, &cliEnv{Paths: env.Paths, Actor: "human:martin"}, id); code != cli.ExitOK {
		t.Fatalf("the Inbox's verdict was refused: %d", code)
	}
	if _, err := os.Stat(ran); err == nil {
		t.Fatal("the script ran on the host: hooks run in the hooks image (#36)")
	}
	if len(hooks) != 1 || !strings.HasSuffix(hooks[0], "services/whoami/post-deploy") {
		t.Fatalf("the approved script must run once, in the hooks image: %v", hooks)
	}
}

// #778: the default policy has no approver hand, and the sync of the built-in
// hands' verbs (#575) neither adds it to a new box nor touches it on an old one.
func TestTheDefaultPolicyHasNoApproverAndTheSyncLeavesItAlone(t *testing.T) {
	ctx := context.Background()
	if strings.Contains(state.DefaultPolicy, "approver") || strings.Contains(state.DefaultPolicy, "human: true") {
		t.Fatalf("the default policy still knows the relay:\n%s", state.DefaultPolicy)
	}
	env, _ := testBox(t)
	pol := filepath.Join(env.Paths.State, "policy.yml")
	raw, _ := os.ReadFile(pol)
	os.WriteFile(pol, []byte(strings.Replace(string(raw), "  human: {ceiling: destroy}\n", legacyApprover+"  human: {ceiling: destroy}\n", 1)), 0o644)
	if err := proposal.CommitDirty(ctx, env.Paths.State, "human:test"); err != nil {
		t.Fatal(err)
	}
	st, err := cli.LoadState(env)
	if err != nil {
		t.Fatal(err)
	}
	if m := policy.Missing(st.Policy, false); len(m["approver"]) != 0 {
		t.Fatalf("the sync wants to grant the old approver: %v", m)
	}
	if p, err := inbox.ProposeDefaults(ctx, env, st); err != nil || p != nil {
		t.Fatalf("an up-to-date box with the old hand gets a defaults proposal: %+v %v", p, err)
	}
}
