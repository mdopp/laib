package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/render"
)

// #441 (owner, 2026-10-19): in the chat the owner decides what is not
// destructive; what the content review marks for a human (a removal, data
// out of the backup, a script on the host, a public route, a token …) and
// every destroy is decided only in the Inbox, behind Authelia's second
// factor. The relay's approver gets the Inbox link instead of a verdict.
func TestTheChatDecidesOnlyWhatTheReviewDoesNotMark(t *testing.T) {
	ctx := context.Background()
	env, _ := testBox(t)
	pol := filepath.Join(env.Paths.State, "policy.yml")
	raw, _ := os.ReadFile(pol)
	os.WriteFile(pol, []byte(strings.Replace(string(raw), "values: auto ", "values: proposal ", 1)), 0o644)
	if err := proposal.CommitDirty(ctx, env.Paths.State, "human:test"); err != nil {
		t.Fatal(err)
	}
	st, err := cli.LoadState(env)
	if err != nil {
		t.Fatal(err)
	}
	// an update: the chat may approve it
	if out, code := mcp.Call(ctx, env, st, "bote", "laib_propose", map[string]any{"args": `--set services/whoami/values.yml:IMAGE=docker.io/traefik/whoami:v1.11 "whoami auf v1.11"`}); code != cli.ExitOK {
		t.Fatalf("propose an update: %d %s", code, out)
	}
	// a script on the host: the review marks it, so only the Inbox
	patch := "diff --git a/services/whoami/post-deploy b/services/whoami/post-deploy\nnew file mode 100755\n--- /dev/null\n+++ b/services/whoami/post-deploy\n@@ -0,0 +1,2 @@\n+#!/bin/sh\n+true\n"
	if out, code := mcp.Call(ctx, env, st, "bote", "laib_propose", map[string]any{"args": `"whoami braucht ein Skript"`, "patch": patch}); code != cli.ExitOK {
		t.Fatalf("propose a script: %d %s", code, out)
	}
	open, _ := proposal.List(cli.InboxDir(env), true)
	if len(open) != 2 {
		t.Fatalf("two open proposals: %+v", open)
	}
	var update, script string
	for _, p := range open {
		if p.Class == "human" {
			script = strconv.Itoa(p.ID)
		} else {
			update = strconv.Itoa(p.ID)
		}
	}
	out, code := mcp.Call(ctx, env, st, "approver", "laib_approve", map[string]any{"args": script})
	if code != cli.ExitRefused || !strings.Contains(out, "nur in der Inbox") || !strings.Contains(out, "https://laib.box.test/inbox") {
		t.Fatalf("the chat approved what only the Inbox may: %d %s", code, out)
	}
	if out, code := mcp.Call(ctx, env, st, "approver", "laib_approve", map[string]any{"args": update}); code != cli.ExitOK {
		t.Fatalf("the chat may approve an update: %d %s", code, out)
	}
	// in the Inbox (the Box page acts as human:<Remote-User>) the script is approved
	page := &cliEnv{Paths: env.Paths, Actor: "human:martin"}
	if code := cmdApproveFor(t, page, script); code != cli.ExitOK {
		t.Fatalf("the Inbox could not approve the script: %d", code)
	}
}

func cmdApproveFor(t *testing.T, env *cliEnv, id string) int {
	t.Helper()
	for _, v := range mcp.Verbs {
		if v.Name == "approve" {
			return v.Run(context.Background(), env, []string{id})
		}
	}
	t.Fatal("no approve verb")
	return 0
}

// #441: laib-notify tells the owner through the Bote first. box.yml
// notify.person names them in people; the unit carries bote-notify's
// address, the person and the Inbox's link, the env file the Bote's
// notify token; the notify hand also reads the inbox (the proposals it
// announces). The Inbox is behind Authelia's second factor.
func TestTheNotifierTellsTheOwnerThroughTheBote(t *testing.T) {
	b := newRbox(t)
	box := filepath.Join(b.env.Paths.State, "box.yml")
	raw, _ := os.ReadFile(box)
	withPerson := strings.Replace(string(raw), "notify:\n", "notify:\n  person: martin\n", 1)
	os.WriteFile(box, []byte(withPerson), 0o644)
	if _, err := cli.LoadState(b.env); err == nil || !strings.Contains(err.Error(), "notify.person") {
		t.Fatalf("notify.person who is not in people must fail the load: %v", err)
	}
	os.WriteFile(box, []byte(withPerson+"people:\n  martin: [\"discord:123456\"]\n"), 0o644)
	st, err := cli.LoadState(b.env)
	if err != nil {
		t.Fatal(err)
	}
	sec := map[string]string{render.NotifyToken: "tok-n", "BOTE_NOTIFY_TOKEN": "bote-n"}
	out, err := render.Render(st, sec)
	if err != nil {
		t.Fatal(err)
	}
	unit := out["quadlet/laib-notify/laib-notify.container"]
	for _, w := range []string{`Environment="NOTIFY_PERSON=martin"`, `Environment="BOTE_NOTIFY_URL=http://127.0.0.1:8645"`, `Environment="LAIB_INBOX_URL=https://laib.`} {
		if !strings.Contains(unit, w) {
			t.Errorf("the notifier's unit lacks %s (a person is enough to run it):\n%s", w, unit)
		}
	}
	if strings.Contains(unit, "bote-n") || render.PodmanSecrets(st, sec)["laib.laib-notify.laib-notify.BOTE_NOTIFY_TOKEN"] != "bote-n" {
		t.Errorf("the Bote's token belongs in a podman secret, not the unit:\n%s", unit)
	}
	var tools []string
	for _, tl := range mcp.Tools(st, "notify") {
		tools = append(tools, tl["name"].(string))
	}
	slices.Sort(tools)
	if strings.Join(tools, " ") != "laib_diag laib_inbox laib_note laib_updates" {
		t.Errorf("the notify hand reaches %v, want diag, inbox, note, updates", tools)
	}
	access := out["authelia/access.yml"]
	if access == "" {
		for k, v := range out {
			if strings.Contains(k, "authelia") && strings.Contains(v, "access_control") {
				access = v
			}
		}
	}
	if !strings.Contains(access, "laib."+st.Box.Domain+"\n      policy: two_factor") {
		t.Errorf("the Inbox is not behind the second factor:\n%s", access)
	}
}
