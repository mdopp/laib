package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/backup"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/state"
)

// #865 (owner, 2026-10-28): ADR 0019 (e) as the code has it — no token
// approves what only a human may (TestAScriptForTheHostWaitsForAHuman,
// TestApprovalsAreTheInboxsAlone), and an agent hand whose ceiling holds
// approve (ops, claude) may approve a proposal that is not human-class and
// not its own. The bote hand (ceiling propose) approves nothing.
func TestAnAgentHandApprovesWhatIsNotHumanClass(t *testing.T) {
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
	realRestic := backup.ResticRun
	t.Cleanup(func() { backup.ResticRun = realRestic })
	backup.ResticRun = func(context.Context, *state.State, string, ...string) (string, error) { return "", nil }
	// a new image of a service with data waits for a decision (#528) and is no human class
	propose := func(hand, tag string) string {
		t.Helper()
		if out, code := mcp.Call(ctx, env, st, hand, "laib_propose", map[string]any{"reason": "vault: neues Image", "service": "vault", "key": "IMAGE", "value": "docker.io/vaultwarden/server:latest@sha256:" + tag}); code != cli.ExitOK {
			t.Fatalf("%s propose: exit %d: %s", hand, code, out)
		}
		open, _ := proposal.List(cli.InboxDir(env), true)
		if len(open) != 1 || open[0].Class == "human" {
			t.Fatalf("one open proposal that is no human class: %+v", open)
		}
		return fmt.Sprint(open[0].ID)
	}
	id := propose("bote", "b2")
	if out, code := mcp.Call(ctx, env, st, "bote", "laib_approve", map[string]any{"args": id}); code == cli.ExitOK {
		t.Fatalf("the bote hand approved: %s", out)
	}
	if out, code := mcp.Call(ctx, env, st, "claude", "laib_approve", map[string]any{"args": id}); code != cli.ExitOK {
		t.Fatalf("claude may approve what is not human-class (ADR 0019 (e), #865): exit %d: %s", code, out)
	}
	id = propose("ops", "b3")
	if out, code := mcp.Call(ctx, env, st, "ops", "laib_approve", map[string]any{"args": id}); code != cli.ExitRefused || !strings.Contains(out, "own proposal") {
		t.Fatalf("a hand never approves its own proposal: exit %d: %s", code, out)
	}
}
