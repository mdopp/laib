package main

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/proposal"
)

// #544: a proposal is judged by what happened, not by the model. It names
// the finding it answers; after an approved apply laib looks whether that
// finding went away and records it, once, in the journal.

func TestARepairNamesItsFindingAndLaibRecordsThatItWentAway(t *testing.T) {
	ctx := context.Background()
	env, _ := testBox(t)
	failedWhoami(t)
	st, _ := cli.LoadState(env)
	if out, code := mcp.Call(ctx, env, st, "ops", "laib_repair", map[string]any{"probe": "unit", "service": "whoami"}); code != cli.ExitOK {
		t.Fatalf("repair: exit %d: %s", code, out)
	}
	open, _ := proposal.List(cli.InboxDir(env), true)
	if len(open) != 1 || open[0].Finding != "unit whoami" || open[0].Evidence == "" || open[0].Undo == "" {
		t.Fatalf("the repair must carry its anchor: %+v", open)
	}
	// still red before the approval: nothing to settle
	mcp.Call(ctx, env, st, "ops", "laib_diag", map[string]any{})
	if p, _ := proposal.Get(cli.InboxDir(env), open[0].ID); p.Outcome != "" {
		t.Fatalf("an open proposal has an outcome: %q", p.Outcome)
	}
	if code := inbox.Decide(ctx, &cliEnv{Paths: env.Paths, Actor: "human:test"}, []string{strconv.Itoa(open[0].ID)}, true); code != cli.ExitOK {
		t.Fatalf("approve: exit %d", code)
	}
	// the restart fixed it: the next whole-box look settles it as effective
	mcp.Call(ctx, env, st, "ops", "laib_diag", map[string]any{})
	mcp.Call(ctx, env, st, "ops", "laib_diag", map[string]any{})
	p, _ := proposal.Get(cli.InboxDir(env), open[0].ID)
	if p.Outcome != "effective" || p.Settled.IsZero() {
		t.Fatalf("outcome %q, want effective", p.Outcome)
	}
	es, _ := journal.Read(env.Paths.Journal, time.Time{})
	n := 0
	for _, e := range es {
		if e.Verb == "outcome" && strings.Contains(e.Detail, "wirksam") && strings.Contains(e.Detail, "unit whoami") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("want one outcome line in the journal, got %d: %+v", n, es)
	}
}

func TestAProposalNamesAFindingDiagShowsAndAWebSourceMakesItHuman(t *testing.T) {
	ctx := context.Background()
	env, _ := testBox(t)
	st, _ := cli.LoadState(env)
	args := map[string]any{"reason": "zurück auf v1.10", "service": "whoami", "key": "IMAGE", "value": "docker.io/traefik/whoami:v1.99",
		"finding": "unit whoami", "evidence": "manifest unknown", "undo": "laib rollback"}
	// whoami runs: there is no such finding, so there is nothing to measure
	if out, code := mcp.Call(ctx, env, st, "ops", "laib_propose", args); code == cli.ExitOK || !strings.Contains(out, "steht nicht in laib diag") {
		t.Fatalf("an anchor on a finding diag does not show: exit %d: %s", code, out)
	}
	failedWhoami(t)
	args["sources"] = []any{"https://github.com/traefik/whoami/releases"}
	if out, code := mcp.Call(ctx, env, st, "ops", "laib_propose", args); code != cli.ExitOK {
		t.Fatalf("propose: exit %d: %s", code, out)
	}
	open, _ := proposal.List(cli.InboxDir(env), true)
	if len(open) != 1 {
		t.Fatalf("%+v", open)
	}
	p := open[0]
	if p.Finding != "unit whoami" || p.Evidence != "manifest unknown" || p.Undo != "laib rollback" || !slices.Equal(p.Sources, []string{"https://github.com/traefik/whoami/releases"}) {
		t.Fatalf("the anchor is not stored: %+v", p.Anchor)
	}
	if p.Class != "human" || !slices.ContainsFunc(p.Reasons, func(r string) bool { return strings.Contains(r, "Quelle aus dem Netz") }) {
		t.Fatalf("a proposal resting on the web is a human's: class %s %q", p.Class, p.Reasons)
	}
	if c := proposal.Card(p, "example.org"); !strings.Contains(c.Why, "manifest unknown") || !strings.Contains(c.Reversible, "laib rollback") {
		t.Fatalf("the card shows what was seen and the way back: %+v", c)
	}
}

func TestARejectKeepsTheOwnersReasonAndNichtJetztIsFree(t *testing.T) {
	ctx := context.Background()
	env, _ := testBox(t)
	// a human's proposals stay open whatever the dial says
	for _, v := range []string{"v1.98", "v1.97"} {
		if code := inbox.Propose(ctx, &cliEnv{Paths: env.Paths, Actor: "human:other"}, []string{"--set", "services/whoami/values.yml:IMAGE=docker.io/traefik/whoami:" + v, "zurück auf " + v}); code != cli.ExitOK {
			t.Fatalf("propose %s: %d", v, code)
		}
	}
	open, _ := proposal.List(cli.InboxDir(env), true)
	human := &cliEnv{Paths: env.Paths, Actor: "human:test"}
	if code := inbox.Reject(ctx, human, []string{strconv.Itoa(open[0].ID), "v1.10", "hat", "die", "Lücke"}); code != cli.ExitOK {
		t.Fatalf("reject: %d", code)
	}
	if code := inbox.Reject(ctx, human, []string{strconv.Itoa(open[1].ID), "Nicht jetzt."}); code != cli.ExitOK {
		t.Fatalf("reject: %d", code)
	}
	a, _ := proposal.Get(cli.InboxDir(env), open[0].ID)
	b, _ := proposal.Get(cli.InboxDir(env), open[1].ID)
	if a.Outcome != "rejected" || a.RejectReason != "v1.10 hat die Lücke" || b.Outcome != "later" {
		t.Fatalf("a: %q %q, b: %q", a.Outcome, a.RejectReason, b.Outcome)
	}
	es, _ := journal.Read(env.Paths.Journal, time.Time{})
	if !slices.ContainsFunc(es, func(e journal.Entry) bool {
		return e.Verb == "reject" && strings.HasSuffix(e.Detail, ": v1.10 hat die Lücke")
	}) {
		t.Fatalf("the journal must carry the reason: %+v", es)
	}
	if code := inbox.Approve(ctx, human, []string{"1", "2"}); code == cli.ExitOK {
		t.Fatal("approve takes one id")
	}
}
