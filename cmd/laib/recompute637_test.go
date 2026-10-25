package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/proposal"
)

// #637, the case that happened (#90, #91): two grants for one hand edit the
// same policy.yml line. Whichever the owner approves first, the second no
// longer merges; Laib adds its verb to the current line instead of calling
// it stale, and says so. A grant whose verb is there by now is stale, in German.
func TestTwoGrantsOnOneHandBothApplyInEitherOrder(t *testing.T) {
	for _, order := range [][2]int{{1, 2}, {2, 1}} {
		t.Run(strconv.Itoa(order[0])+"-then-"+strconv.Itoa(order[1]), func(t *testing.T) {
			env, _ := testBox(t)
			ctx := context.Background()
			claude := &cliEnv{Paths: env.Paths, Actor: "claude"}
			for _, verb := range []string{"wish", "diag"} {
				if code := inbox.Propose(ctx, claude, []string{"--grant", "haus", verb, "Haus braucht " + verb}); code != cli.ExitOK {
					t.Fatalf("--grant haus %s: exit %d", verb, code)
				}
			}
			var said strings.Builder
			for _, id := range order {
				if code := inbox.Decide(ctx, &cliEnv{Paths: env.Paths, Actor: "human:test", Out: &said}, []string{strconv.Itoa(id)}, true); code != cli.ExitOK {
					t.Fatalf("approve #%d (after #%d): exit %d", id, order[0], code)
				}
			}
			if said.String() != "#"+strconv.Itoa(order[1])+" neu berechnet auf dem aktuellen Stand\n" {
				t.Fatalf("the owner is not told: %q", said.String())
			}
			pol, _ := os.ReadFile(filepath.Join(env.Paths.State, "policy.yml"))
			line := ""
			for _, l := range strings.Split(string(pol), "\n") {
				if strings.HasPrefix(l, "  haus:") {
					line = l
				}
			}
			if !strings.Contains(line, ", wish") || !strings.Contains(line, ", diag") {
				t.Fatalf("not both verbs on haus' line:\n%s", line)
			}
			second, _ := proposal.Get(cli.InboxDir(env), order[1])
			if second.Status != "approved" || !second.Recomputed {
				t.Fatalf("the second grant: %s, recomputed %v (%s)", second.Status, second.Recomputed, second.StaleReason)
			}
			if first, _ := proposal.Get(cli.InboxDir(env), order[0]); first.Recomputed {
				t.Fatal("the first grant merged as it was and says it was recomputed")
			}
		})
	}

	// a verb that is there by now: the intent is done, by someone else
	env, _ := testBox(t)
	ctx := context.Background()
	for i, who := range []string{"claude", "pi", "bote"} {
		verb := []string{"wish", "diag", "wish"}[i]
		if code := inbox.Propose(ctx, &cliEnv{Paths: env.Paths, Actor: who}, []string{"--grant", "haus", verb, "Haus braucht " + verb}); code != cli.ExitOK {
			t.Fatalf("%s: exit %d", who, code)
		}
	}
	inbox.Decide(ctx, env, []string{"1"}, true)
	inbox.Decide(ctx, env, []string{"2"}, true)
	if code := inbox.Decide(ctx, &cliEnv{Paths: env.Paths, Actor: "human:test", ErrOut: new(strings.Builder)}, []string{"3"}, true); code == cli.ExitOK {
		t.Fatal("the same grant applied twice")
	}
	if p, _ := proposal.Get(cli.InboxDir(env), 3); p.Status != "stale" || !strings.Contains(p.StaleReason, "haus darf wish schon") {
		t.Fatalf("%s: %q", p.Status, p.StaleReason)
	}

	// the same line twice merges cleanly and changes nothing: no approval of nothing
	for _, who := range []string{"claude", "pi"} {
		inbox.Propose(ctx, &cliEnv{Paths: env.Paths, Actor: who}, []string{"--grant", "haus", "updates", "Haus sieht Updates"})
	}
	inbox.Decide(ctx, env, []string{"4"}, true)
	if code := inbox.Decide(ctx, &cliEnv{Paths: env.Paths, Actor: "human:test", ErrOut: new(strings.Builder)}, []string{"5"}, true); code == cli.ExitOK {
		t.Fatal("an approve that changes nothing reported success")
	}
	if p, _ := proposal.Get(cli.InboxDir(env), 5); p.Status != "stale" || !strings.Contains(p.StaleReason, "das steht schon so") {
		t.Fatalf("%s: %q", p.Status, p.StaleReason)
	}
}
