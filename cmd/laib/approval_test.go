package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/proposal"
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
	env = &cliEnv{actor: "human:test", paths: boxfs.Paths{
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
	if code := cmdInit(context.Background(), env, []string{"--domain", "box.test", "--lan-ip", "10.0.0.2", "--data-dir", dataDir}); code != exitOK {
		t.Fatalf("init: exit %d", code)
	}
	svc := filepath.Join(env.paths.State, "services", "whoami")
	if err := os.MkdirAll(svc, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"laib.yml":         "name: whoami\nsecrets:\n  - name: WHOAMI_TOKEN\n",
		"values.yml":       "IMAGE: docker.io/traefik/whoami:v1.10\nTOKEN: !secret WHOAMI_TOKEN\n",
		"whoami.container": "[Container]\nImage=${IMAGE}\nEnvironment=TOKEN=${TOKEN}\n\n[Install]\nWantedBy=default.target\n",
	} {
		if err := os.WriteFile(filepath.Join(svc, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := proposal.CommitDirty(context.Background(), env.paths.State, "human:test"); err != nil {
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
	st, err := loadState(env)
	if err != nil {
		t.Fatal(err)
	}
	patch := "diff --git a/services/whoami/post-deploy b/services/whoami/post-deploy\nnew file mode 100755\n--- /dev/null\n+++ b/services/whoami/post-deploy\n@@ -0,0 +1,2 @@\n+#!/bin/sh\n+touch \"$LAIB_DATA_DIR/ran-on-the-host\"\n"
	out, code := mcpCall(ctx, env, st, "bote", "laib_propose", map[string]string{"args": `"whoami braucht ein Einrichtungs-Skript"`, "patch": patch})
	if _, err := os.Stat(ran); err == nil {
		t.Fatalf("bote's script ran on the host (propose exit %d): %s", code, out)
	}
	if code != exitOK {
		t.Fatalf("propose: exit %d: %s", code, out)
	}
	open, _ := proposal.List(inboxDir(env), true)
	if len(open) != 1 || open[0].Class != "human" || !strings.Contains(strings.Join(open[0].Reasons, " "), "Skript") {
		t.Fatalf("the proposal should wait for a human and say why: %+v", open)
	}
	if out, _ := mcpCall(ctx, env, st, "ops", "laib_inbox", map[string]string{"args": ""}); !strings.Contains(out, "führt ein Skript direkt auf der Box aus") {
		t.Fatalf("the inbox does not say why: %s", out)
	}
	id := strconv.Itoa(open[0].ID)
	for _, hand := range []string{"ops", "claude"} {
		out, code = mcpCall(ctx, env, st, hand, "laib_approve", map[string]string{"args": id})
		if code != exitRefused || !strings.Contains(out, "Skript") {
			t.Fatalf("%s approving: exit %d, want %d and the reason: %s", hand, code, exitRefused, out)
		}
	}
	if _, err := os.Stat(ran); err == nil {
		t.Fatal("the script ran after a refused approval")
	}
	if p, _ := proposal.Get(inboxDir(env), open[0].ID); p.Status != "open" {
		t.Fatalf("status %q after the refusals, want open", p.Status)
	}
	es, _ := journal.Read(env.paths.Journal, time.Time{})
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
	if code := decide(ctx, &cliEnv{paths: env.paths, actor: "human:test"}, []string{id}, true); code != exitOK {
		t.Fatalf("the human's approval: exit %d", code)
	}
	if _, err := os.Stat(ran); err != nil {
		t.Fatal("the script the human approved did not run")
	}
}

// The dial still decides what is harmless: a newer tag of the same image is
// a values change, and values are auto by default.
func TestANewerImageTagIsStillAutomatic(t *testing.T) {
	ctx := context.Background()
	env, _ := testBox(t)
	st, err := loadState(env)
	if err != nil {
		t.Fatal(err)
	}
	out, code := mcpCall(ctx, env, st, "bote", "laib_propose", map[string]string{"args": `--set services/whoami/values.yml:IMAGE=docker.io/traefik/whoami:v1.11 "neueres whoami"`})
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	all, _ := proposal.List(inboxDir(env), false)
	if len(all) != 1 || all[0].Status != "approved" || all[0].DecidedBy != "laib" {
		t.Fatalf("laib should approve a newer tag itself: %+v", all)
	}
	b, _ := os.ReadFile(filepath.Join(env.paths.Quadlet, "whoami", "whoami.container"))
	if !strings.Contains(string(b), "whoami:v1.11") {
		t.Fatalf("not applied: %s", b)
	}
}

// A template may never mint a token stronger than the hand that asked for it
// (#34): pi (propose) cannot get an ops (apply) token into a service.
func TestATokenStrongerThanItsProposerIsRefused(t *testing.T) {
	ctx := context.Background()
	env, _ := testBox(t)
	st, err := loadState(env)
	if err != nil {
		t.Fatal(err)
	}
	patch := "diff --git a/services/whoami/laib.yml b/services/whoami/laib.yml\n--- a/services/whoami/laib.yml\n+++ b/services/whoami/laib.yml\n@@ -3 +3,2 @@\n   - name: WHOAMI_TOKEN\n+  - {name: WHOAMI_LAIB, external: true, mint: ops}\n"
	out, code := mcpCall(ctx, env, st, "pi", "laib_propose", map[string]string{"args": `"whoami soll Laib bedienen"`, "patch": patch})
	store, _ := token.Load(tokenPath(env))
	for _, r := range store.List() {
		if r.Hand == "ops" {
			t.Fatalf("an ops token was minted for pi's proposal (exit %d): %s", code, out)
		}
	}
	if code == exitOK || !strings.Contains(out, "ops") {
		t.Fatalf("pi's proposal was accepted (exit %d): %s", code, out)
	}
	if ps, _ := proposal.List(inboxDir(env), false); len(ps) != 0 {
		t.Fatalf("a refused proposal was kept: %+v", ps)
	}
}
