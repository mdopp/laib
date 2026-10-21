package main

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/proposal"
)

// #544: a lesson is a file in the state repo (lessons/<name>.md). The
// Operator may propose one; only a human lets it in (the review says
// why), and from then on laib context carries it into every session.
func TestALessonIsAHumansAndThenInEveryContext(t *testing.T) {
	ctx := context.Background()
	env, _ := testBox(t)
	st, _ := cli.LoadState(env)
	patch := "--- /dev/null\n+++ b/lessons/unit-whoami.md\n@@ -0,0 +1,2 @@\n+# Lehre: unit whoami\n+- Was half: zurück auf v1.10\n"
	if out, code := mcp.Call(ctx, env, st, "ops", "laib_propose", map[string]any{"reason": "Lehre: unit whoami — zurück auf v1.10", "patch": patch}); code != cli.ExitOK {
		t.Fatalf("propose: exit %d: %s", code, out)
	}
	open, _ := proposal.List(cli.InboxDir(env), true)
	if len(open) != 1 || open[0].Class != "human" || !slices.ContainsFunc(open[0].Reasons, func(r string) bool { return strings.Contains(r, "eine Lehre für den Operator") }) {
		t.Fatalf("a lesson waits for a human, and the card says what it is: %+v", open)
	}
	if out, _ := mcp.Call(ctx, env, st, "ops", "laib_context", map[string]any{}); strings.Contains(out, "Was half") {
		t.Fatalf("a lesson in the context before a human let it in: %s", out)
	}
	if out, code := mcp.Call(ctx, env, st, "ops", "laib_approve", map[string]any{"args": strconv.Itoa(open[0].ID)}); code == cli.ExitOK {
		t.Fatalf("the Operator let its own lesson in: %s", out)
	}
	if code := inbox.Decide(ctx, &cliEnv{Paths: env.Paths, Actor: "human:test"}, []string{strconv.Itoa(open[0].ID)}, true); code != cli.ExitOK {
		t.Fatalf("approve: %d", code)
	}
	out, code := mcp.Call(ctx, env, st, "ops", "laib_context", map[string]any{})
	if code != cli.ExitOK || !strings.Contains(out, `"lessons"`) || !strings.Contains(out, `"# Lehre: unit whoami\n- Was half: zurück auf v1.10\n"`) {
		t.Fatalf("the context does not carry the lesson: %s", out)
	}
}
