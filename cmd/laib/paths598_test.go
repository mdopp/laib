package main

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

// #598: laib propose --path sets one value at a named path of box.yml as a
// line edit that keeps the comments; the loader refuses what box.yml cannot
// be, and the review makes a box.yml change a human's, as before.
func TestProposePathIsACommentKeepingEdit(t *testing.T) {
	env, _ := testBox(t)
	ctx := context.Background()
	boxPath := filepath.Join(env.Paths.State, "box.yml")
	b, _ := os.ReadFile(boxPath)
	os.WriteFile(boxPath, append([]byte("# the owner's note\n"), b...), 0o644)
	if err := proposal.CommitDirty(ctx, env.Paths.State, "human:test"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"box.yml:people.mama.signal=0151", "box.yml:zuhause.strasse=Musterweg", "box.yml:people.mama.signal"} {
		if code := inbox.Propose(ctx, env, []string{"--path", bad, "Mama auf Signal"}); code == cli.ExitOK {
			t.Errorf("%s: accepted", bad)
		}
	}
	if all, _ := proposal.List(cli.InboxDir(env), false); len(all) != 0 {
		t.Fatalf("a refused path left %d proposals", len(all))
	}
	if code := inbox.Propose(ctx, env, []string{"--path", "box.yml:people.mama.signal=+4900000002", "Mama auf Signal"}); code != cli.ExitOK {
		t.Fatalf("exit %d", code)
	}
	p, err := proposal.Get(cli.InboxDir(env), 1)
	if err != nil || p.Class != "human" || strings.Join(p.Files, ",") != "box.yml" {
		t.Fatalf("%v: %+v", err, p)
	}
	diff := proposal.Diff(ctx, env.Paths.State, p)
	if regexp.MustCompile(`(?m)^-[^-]`).MatchString(diff) || !strings.Contains(diff, "\n+people:\n+  mama: [\"signal:+4900000002\"]") {
		t.Fatalf("not a line edit that only adds the person:\n%s", diff)
	}
	// over MCP: path and value
	out, code := mcp.Call(ctx, env, mustState(t, env), "claude", "laib_propose", map[string]any{"reason": "Adresse", "path": "box.yml:zuhause.city", "value": "Musterstadt"})
	if code != cli.ExitOK || !strings.Contains(out, "box.yml") {
		t.Fatalf("exit %d: %s", code, out)
	}
}

// #598: a hand with a verb list proposes only the paths policy.yml grants it;
// without the grant laib_person_invite --signal is refused before anything
// is written or anyone invited. The default sync proposes the grant, once.
// (laib_zuhause and laib_person_signal are served behind the door since
// #677: the verb grant gates them, and laib-haushalt's hand holds the
// paths; zuhause561_test.go, person596_test.go.)
func TestAHandWithoutThePathGrantIsRefusedUntilTheOwnerGrantsIt(t *testing.T) {
	env, calls := personBox(t, `{"user":"mama","name":"Mama","email":"mama@box.test","mailed":true,"expires":"2026-10-19T10:00:00Z"}`)
	ctx := context.Background()
	invite := map[string]any{"user": "mama", "first": "Mama", "email": "mama@box.test", "signal": "+4900000002"}
	pol := filepath.Join(env.Paths.State, "policy.yml")
	old := strings.Replace(state.DefaultPolicy, ", paths: [zuhause, people.*.signal]}", "}", 1)
	if old == state.DefaultPolicy {
		t.Fatal("the haus hand's defaults grant no paths")
	}
	os.WriteFile(pol, []byte(old), 0o644)
	if err := proposal.CommitDirty(ctx, env.Paths.State, "human:test"); err != nil {
		t.Fatal(err)
	}
	branches := func() string {
		out, _ := sysd.Run(ctx, "git", "-C", env.Paths.State, "branch", "--list")
		return out
	}
	before := branches()
	if out, code := mcp.Call(ctx, env, mustState(t, env), "haus", "laib_person_invite", invite); code == cli.ExitOK || len(*calls) != 0 {
		t.Fatalf("invite --signal without the path grant: exit %d, %d tool runs: %s", code, len(*calls), out)
	}
	if all, _ := proposal.List(cli.InboxDir(env), false); len(all) != 0 || branches() != before {
		t.Fatalf("a refused path left %d proposals or a branch", len(all))
	}
	env.Actor = "laib"
	sync := func() *proposal.Proposal {
		p, err := inbox.ProposeDefaults(ctx, env, mustState(t, env))
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	p := sync()
	if p == nil || p.Class != "human" || !strings.Contains(p.Reason, "haus +zuhause +people.*.signal") {
		t.Fatalf("%+v", p)
	}
	if diff := proposal.Diff(ctx, env.Paths.State, p); strings.Count(diff, "\n+  ") != 1 || !strings.Contains(diff, "note, tell, aufgabe], paths: [zuhause, people.*.signal]}") {
		t.Fatalf("not the one line:\n%s", diff)
	}
	if again := sync(); again != nil {
		t.Fatalf("a second proposal while #%d is open: #%d", p.ID, again.ID)
	}
	env.Actor = "human:test"
	if code := inbox.Approve(ctx, env, []string{"1"}); code != cli.ExitOK {
		t.Fatalf("approve: exit %d", code)
	}
	if out, code := mcp.Call(ctx, env, mustState(t, env), "haus", "laib_person_invite", invite); code != cli.ExitOK {
		t.Fatalf("after the grant: exit %d: %s", code, out)
	}
}
