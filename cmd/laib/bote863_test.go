package main

import (
	"context"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/aufgaben"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/envelope"
)

// #863 (owner, 2026-10-28): a family member's escalation goes to the owner
// as a Box-Aufgabe, never into a public repo. The Bote's hand is the one the
// family writes through, so an escalation by the hand bote files no ticket
// in box.yml intake.repo, whatever it is, and lands on the box's to-do list
// with the bundle beside it; the Operator's escalation still files its
// ticket (TestEscalateFilesAnIssueWhenIntakeIsConfigured).
func TestTheBotesEscalationIsABoxAufgabeNeverATicket(t *testing.T) {
	c := newCbox(t)
	g := newFakeGitHub(t)
	c.intake(t, "mdopp/laib", g)
	c.env.Actor = "bote"
	if rc := envelope.Escalate(context.Background(), c.env, []string{"caddy", "Mama fragt, warum die Fotos nicht laden"}); rc != cli.ExitOK {
		t.Fatalf("rc %d", rc)
	}
	if len(g.calls) != 0 {
		t.Fatalf("a family member's question went to the intake repo: %v", g.calls)
	}
	list, err := aufgaben.Load(c.env.Paths.State)
	if err != nil || len(list) != 1 {
		t.Fatalf("one Box-Aufgabe: %v %v", list, err)
	}
	a := list[0]
	if !a.Open() || a.Von != "bote" || !strings.Contains(a.Titel, "Fotos nicht laden") || !strings.Contains(a.Titel, "Bote") {
		t.Fatalf("the task names the question and who asked: %+v", a)
	}
	if !strings.Contains(a.Warum, "caddy") || !strings.Contains(a.Warum, "escalations/") {
		t.Fatalf("the task says which service and where the bundle lies: %q", a.Warum)
	}
	if !c.journalHas(t, "Box-Aufgabe #1") {
		t.Fatal("the journal names the Box-Aufgabe")
	}
	if c.journalHas(t, "issues/") {
		t.Fatal("the journal names a ticket")
	}
}
