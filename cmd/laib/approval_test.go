package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/backup"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/mcp"

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
	"github.com/mdopp/laib/internal/token"
)

// testBox is a box made by `laib init` — with the policy every real box gets,
// not a fixture's — and one small service a human committed. The host is
// faked at the sysd seam: git runs, systemctl reports every unit active,
// nothing else runs. Hooks are real: a post-deploy runs as it would on the box.
func testBox(t *testing.T) (env *cliEnv, dataDir string) {
	t.Helper()
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	env = &cliEnv{Actor: "human:test", Paths: boxfs.Paths{
		Home: home, State: filepath.Join(home, "state"), Journal: filepath.Join(home, "journal"), Render: filepath.Join(home, "render"),
		Quadlet: filepath.Join(dir, "quadlet"), Systemd: filepath.Join(dir, "systemd"),
		Key: filepath.Join(home, "age.key"), Secrets: filepath.Join(home, "state", "secrets.age"), Lock: filepath.Join(home, "apply.lock"),
	}}
	real := sysd.Run
	t.Cleanup(func() { sysd.Run = real })
	sysd.Run = func(ctx context.Context, name string, args ...string) (string, error) {
		switch {
		case name == "git":
			return real(ctx, name, args...)
		case name == "systemctl" && slices.Contains(args, "is-active"):
			return "active", nil
		}
		return "", nil
	}
	dataDir = filepath.Join(dir, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if code := cmdInit(context.Background(), env, []string{"--domain", "box.test", "--lan-ip", "10.0.0.2", "--data-dir", dataDir}); code != cli.ExitOK {
		t.Fatalf("init: exit %d", code)
	}
	svc := filepath.Join(env.Paths.State, "services", "whoami")
	if err := os.MkdirAll(svc, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"laib.yml":         "name: whoami\nsecrets:\n  - name: WHOAMI_TOKEN\n",
		"values.yml":       "IMAGE: docker.io/traefik/whoami:v1.10\nTOKEN: !secret WHOAMI_TOKEN\n",
		"whoami.container": "[Container]\nImage=${IMAGE}\nEnvironment=TOKEN=${TOKEN}\nAutoUpdate=registry\n\n[Install]\nWantedBy=default.target\n",
	} {
		if err := os.WriteFile(filepath.Join(svc, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := proposal.CommitDirty(context.Background(), env.Paths.State, "human:test"); err != nil {
		t.Fatal(err)
	}
	return env, dataDir
}

// #34: bote (ceiling propose) sends a patch that adds an executable
// post-deploy to a service. With the defaults `laib init` shipped, the dial
// approved patches by their shape: laib merged the patch, applied it, and the
// script ran on the host as core. Now the content decides: the proposal waits
// for a human, an agent's approval is refused, and journal and inbox say why.
func TestAScriptForTheHostWaitsForAHuman(t *testing.T) {
	ctx := context.Background()
	env, data := testBox(t)
	ran := filepath.Join(data, "ran-on-the-host")
	// hooks run in the hooks image since #36, never on the host: record the runs
	var hooks []string
	realHook := apply.RunHook
	t.Cleanup(func() { apply.RunHook = realHook })
	apply.RunHook = func(_ context.Context, h apply.HookSpec) (string, error) {
		hooks = append(hooks, h.Image+" "+strings.Join(h.Command, " "))
		return "", nil
	}
	st, err := cli.LoadState(env)
	if err != nil {
		t.Fatal(err)
	}
	patch := "diff --git a/services/whoami/post-deploy b/services/whoami/post-deploy\nnew file mode 100755\n--- /dev/null\n+++ b/services/whoami/post-deploy\n@@ -0,0 +1,2 @@\n+#!/bin/sh\n+touch \"$LAIB_DATA_DIR/ran-on-the-host\"\n"
	out, code := mcp.Call(ctx, env, st, "bote", "laib_propose", map[string]any{"args": `"whoami braucht ein Einrichtungs-Skript"`, "patch": patch})
	if _, err := os.Stat(ran); err == nil {
		t.Fatalf("bote's script ran on the host (propose exit %d): %s", code, out)
	}
	if code != cli.ExitOK {
		t.Fatalf("propose: exit %d: %s", code, out)
	}
	open, _ := proposal.List(cli.InboxDir(env), true)
	if len(open) != 1 || open[0].Class != "human" || !strings.Contains(strings.Join(open[0].Reasons, " "), "Skript") {
		t.Fatalf("the proposal should wait for a human and say why: %+v", open)
	}
	if out, _ := mcp.Call(ctx, env, st, "ops", "laib_inbox", map[string]any{"args": ""}); !strings.Contains(out, "führt ein Skript direkt auf der Box aus") {
		t.Fatalf("the inbox does not say why: %s", out)
	}
	id := strconv.Itoa(open[0].ID)
	for _, hand := range []string{"ops", "claude"} {
		out, code = mcp.Call(ctx, env, st, hand, "laib_approve", map[string]any{"args": id})
		if code != cli.ExitRefused || !strings.Contains(out, "Skript") {
			t.Fatalf("%s approving: exit %d, want %d and the reason: %s", hand, code, cli.ExitRefused, out)
		}
	}
	if _, err := os.Stat(ran); err == nil || len(hooks) != 0 {
		t.Fatalf("the script ran after a refused approval: %v", hooks)
	}
	if p, _ := proposal.Get(cli.InboxDir(env), open[0].ID); p.Status != "open" {
		t.Fatalf("status %q after the refusals, want open", p.Status)
	}
	es, _ := journal.Read(env.Paths.Journal, time.Time{})
	said := false
	for _, e := range es {
		if e.Verb == "approve" && e.Actor == "ops" && strings.Contains(e.Detail, "Skript") {
			said = true
		}
	}
	if !said {
		t.Fatalf("the journal does not say why ops was refused: %+v", es)
	}
	// the human may: the inbox told them what it does
	if code := inbox.Approve(ctx, &cliEnv{Paths: env.Paths, Actor: "human:test"}, []string{id}); code != cli.ExitOK {
		t.Fatalf("the human's approval: exit %d", code)
	}
	if _, err := os.Stat(ran); err == nil {
		t.Fatal("the script ran on the host: hooks run in the hooks image (#36)")
	}
	if len(hooks) != 1 || !strings.Contains(hooks[0], "laib-hooks") || !strings.HasSuffix(hooks[0], "services/whoami/post-deploy") {
		t.Fatalf("the script the human approved must run once, in the hooks image: %v", hooks)
	}
}

// The dial still decides what is harmless: a newer tag of the same image is
// a values change, and values are auto by default.
func TestANewerImageTagIsStillAutomatic(t *testing.T) {
	ctx := context.Background()
	env, _ := testBox(t)
	st, err := cli.LoadState(env)
	if err != nil {
		t.Fatal(err)
	}
	out, code := mcp.Call(ctx, env, st, "bote", "laib_propose", map[string]any{"args": `--set services/whoami/values.yml:IMAGE=docker.io/traefik/whoami:v1.11 "neueres whoami"`})
	if code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	all, _ := proposal.List(cli.InboxDir(env), false)
	if len(all) != 1 || all[0].Status != "approved" || all[0].DecidedBy != "laib" {
		t.Fatalf("laib should approve a newer tag itself: %+v", all)
	}
	b, _ := os.ReadFile(filepath.Join(env.Paths.Quadlet, "whoami", "whoami.container"))
	if !strings.Contains(string(b), "whoami:v1.11") {
		t.Fatalf("not applied: %s", b)
	}
}

// A template may never mint a token stronger than the hand that asked for it
// (#34): pi (propose) cannot get an ops (apply) token into a service.
func TestATokenStrongerThanItsProposerIsRefused(t *testing.T) {
	ctx := context.Background()
	env, _ := testBox(t)
	st, err := cli.LoadState(env)
	if err != nil {
		t.Fatal(err)
	}
	patch := "diff --git a/services/whoami/laib.yml b/services/whoami/laib.yml\n--- a/services/whoami/laib.yml\n+++ b/services/whoami/laib.yml\n@@ -3 +3,2 @@\n   - name: WHOAMI_TOKEN\n+  - {name: WHOAMI_LAIB, external: true, mint: ops}\n"
	out, code := mcp.Call(ctx, env, st, "pi", "laib_propose", map[string]any{"args": `"whoami soll Laib bedienen"`, "patch": patch})
	store, _ := token.Load(cli.TokenPath(env))
	for _, r := range store.List() {
		if r.Hand == "ops" {
			t.Fatalf("an ops token was minted for pi's proposal (exit %d): %s", code, out)
		}
	}
	if code == cli.ExitOK || !strings.Contains(out, "ops") {
		t.Fatalf("pi's proposal was accepted (exit %d): %s", code, out)
	}
	if ps, _ := proposal.List(cli.InboxDir(env), false); len(ps) != 0 {
		t.Fatalf("a refused proposal was kept: %+v", ps)
	}
}

// #528: a new image of a service with data (its container lacks
// AutoUpdate=registry, its data has a database) is never the dial's: the
// proposal waits for a person — inbox and Signal, it is no "human" class —
// and the run that applies it takes a pre-update snapshot of the service
// before it touches the box (#609: no longer before the merge); a snapshot
// that fails rolls the run back and the box stays as it was.
func TestANewImageOfAServiceWithDataWaitsAndIsSnapshottedFirst(t *testing.T) {
	ctx := context.Background()
	env, _ := testBox(t)
	svc := filepath.Join(env.Paths.State, "services", "vault")
	os.MkdirAll(svc, 0o755)
	for name, content := range map[string]string{
		"laib.yml":        "name: vault\ndata:\n  - path: ${DATA_DIR}/vault\n    quiesce: true\n",
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
	propose := func() string {
		if out, code := mcp.Call(ctx, env, st, "ops", "laib_propose", map[string]any{"reason": "vault: neues Image", "service": "vault", "key": "IMAGE", "value": pin}); code != cli.ExitOK {
			t.Fatalf("exit %d: %s", code, out)
		}
		open, _ := proposal.List(cli.InboxDir(env), true)
		return fmt.Sprint(open[0].ID)
	}
	id := propose()
	all, _ := proposal.List(cli.InboxDir(env), false)
	if len(all) != 1 || all[0].Status != "open" || all[0].Class == "human" || len(all[0].Warnings) != 1 || !strings.Contains(all[0].Warnings[0], "vorher sichert Laib vault") || !slices.Equal(all[0].Snapshot, []string{"vault"}) {
		t.Fatalf("the dial approved a data service's image, or the person is not told: %+v", all)
	}
	realRestic := backup.ResticRun
	t.Cleanup(func() { backup.ResticRun = realRestic })
	var snaps []string
	fail := true
	backup.ResticRun = func(_ context.Context, _ *state.State, _ string, args ...string) (string, error) {
		if args[0] == "backup" {
			b, _ := os.ReadFile(filepath.Join(env.Paths.Quadlet, "vault", "vault.container"))
			snaps = append(snaps, fmt.Sprintf("%s | box has the new image: %v", strings.Join(args, " "), strings.Contains(string(b), pin)))
			if fail {
				return "", errors.New("repo locked")
			}
		}
		return "", nil
	}
	if code := inbox.Approve(ctx, &cliEnv{Paths: env.Paths, Actor: "human:test", Out: io.Discard, ErrOut: io.Discard}, []string{id}); code == cli.ExitOK {
		t.Fatal("a failed snapshot must not end green")
	}
	if p, _ := proposal.Get(cli.InboxDir(env), all[0].ID); p.Outcome != "rolled-back" {
		t.Fatalf("a failed snapshot leaves the run rolled back: %+v", p)
	}
	if b, _ := os.ReadFile(filepath.Join(env.Paths.Quadlet, "vault", "vault.container")); strings.Contains(string(b), pin) {
		t.Fatal("the box was touched although the snapshot failed")
	}
	fail = false
	if code := inbox.Approve(ctx, &cliEnv{Paths: env.Paths, Actor: "human:test", Out: io.Discard}, []string{propose()}); code != cli.ExitOK {
		t.Fatalf("approval: %d", code)
	}
	if len(snaps) != 2 || !strings.Contains(snaps[1], "--tag pre-update --tag vault") || !strings.HasSuffix(snaps[1], "box has the new image: false") {
		t.Fatalf("the snapshot is not taken before the box is touched: %q", snaps)
	}
	if b, _ := os.ReadFile(filepath.Join(env.Paths.Quadlet, "vault", "vault.container")); !strings.Contains(string(b), pin) {
		t.Fatalf("not applied: %s", b)
	}
}
