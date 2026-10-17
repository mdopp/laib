package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/proposal"
)

// #270, ADR 0023 §6: the Operator's wish waits for the admin. It is a
// proposal only a human may free — nothing is filed — approving it files
// the intake issue, rejecting it files nothing.
func TestAnOperatorWishWaitsForTheAdmin(t *testing.T) {
	c := newCbox(t)
	c.intake(t, "mdopp/x")
	g := newFakeGitHub(t)
	ctx := context.Background()
	c.env.Actor = "ops"
	for _, text := range []string{
		"Den Abfuhrkalender als Antwort für Haus und Bote einrichten.\n\nGesehen: 9 unbeantwortete Fragen zur Müllabfuhr in 7 Tagen.",
		"Die Energie-Seite von der Startseite nach hinten.\n\nGesehen: 0 Aufrufe in 7 Tagen.",
	} {
		if rc := cmdWish(ctx, c.env, []string{text}); rc != cli.ExitOK {
			t.Fatalf("rc %d", rc)
		}
	}
	if len(g.calls) != 0 {
		t.Fatalf("an Operator wish filed an issue before anyone approved it: %v", g.calls)
	}
	ps, err := proposal.List(cli.InboxDir(c.env), true)
	if err != nil || len(ps) != 2 {
		t.Fatalf("two proposals expected: %v %v", ps, err)
	}
	for _, p := range ps {
		if p.Class != "human" || !strings.Contains(strings.Join(p.Reasons, " "), "Wunsch an die Werkstatt") {
			t.Fatalf("#%d: class %s, reasons %v", p.ID, p.Class, p.Reasons)
		}
	}
	if strings.Contains(ps[0].Reason, "Energie") {
		ps[0], ps[1] = ps[1], ps[0]
	}
	c.env.Actor = "human:admin"
	if rc := cmdApprove(ctx, c.env, []string{fmt.Sprint(ps[0].ID)}); rc != cli.ExitOK {
		t.Fatalf("approve rc %d", rc)
	}
	if len(g.calls) != 1 {
		t.Fatalf("one issue after one approval: %v", g.calls)
	}
	title, _ := g.calls[0]["title"].(string)
	body, _ := g.calls[0]["body"].(string)
	labels, _ := g.calls[0]["labels"].([]any)
	if !strings.Contains(title+body, "Abfuhrkalender") || !strings.Contains(body, "9 unbeantwortete") || !slices.Contains(labels, any("wunsch")) {
		t.Fatalf("issue %q %q %v", title, body, labels)
	}
	if rc := cmdReject(ctx, c.env, []string{fmt.Sprint(ps[1].ID)}); rc != cli.ExitOK {
		t.Fatalf("reject rc %d", rc)
	}
	if len(g.calls) != 1 {
		t.Fatalf("a rejected wish filed an issue: %v", g.calls)
	}
}
