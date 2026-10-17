package main

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/proposal"
)

// #147: review finds the facts a decision card shows ("whoami ist unter
// who.box.test …", #58), but the verdict judgeFor hands to the proposal
// left them out, so no real card ever showed one. Driven through the real
// propose path, not a hand-built verdict.
func TestAProposedRouteShowsItsFactOnTheCard(t *testing.T) {
	ctx := context.Background()
	env, _ := testBox(t)
	st, err := cli.LoadState(env)
	if err != nil {
		t.Fatal(err)
	}
	patch := "diff --git a/services/whoami/laib.yml b/services/whoami/laib.yml\n--- a/services/whoami/laib.yml\n+++ b/services/whoami/laib.yml\n@@ -1,3 +1,8 @@\n name: whoami\n secrets:\n   - name: WHOAMI_TOKEN\n+routes:\n+  - host: who\n+    port: 8080\n+    exposure: internal\n+    auth: none\n"
	if out, code := mcpCall(ctx, env, st, "bote", "laib_propose", map[string]any{"args": `"whoami soll eine Adresse bekommen"`, "patch": patch}); code != cli.ExitOK {
		t.Fatalf("propose: exit %d: %s", code, out)
	}
	ps, _ := proposal.List(inboxDir(env), false)
	if len(ps) != 1 {
		t.Fatalf("want one proposal, got %+v", ps)
	}
	want := "whoami ist unter who.box.test nur im Heimnetz erreichbar, mit eigener Anmeldung"
	if !slices.Contains(ps[0].Facts, want) {
		t.Fatalf("the proposal lost review's fact %q: %+v", want, ps[0].Facts)
	}
	if c := proposal.Card(ps[0], "box.test"); !strings.Contains(c.Summary, "who.box.test") {
		t.Fatalf("the card does not show the fact: %+v", c)
	}
}
