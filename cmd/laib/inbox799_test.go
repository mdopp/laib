package main

import (
	"net/url"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/proposal"
)

// #799: „Alle freigeben“ leaves what deletes and what the review flagged
// for one-by-one approval, and says on the page which and why; a POST that
// names them anyway does not approve them.
func TestAlleFreigebenLeavesDestroyAndFlaggedCards(t *testing.T) {
	env, _ := testBox(t)
	for _, name := range []string{"w1", "w2", "w3", "w4", "w5"} {
		proposeService(t, env, name)
	}
	mark := func(id int, f func(p *proposal.Proposal)) {
		p, _ := proposal.Get(cli.InboxDir(env), id)
		f(p)
		if err := proposal.Save(cli.InboxDir(env), p); err != nil {
			t.Fatal(err)
		}
	}
	mark(2, func(p *proposal.Proposal) { p.Tier = "destroy" })
	mark(3, func(p *proposal.Proposal) {
		p.Class, p.Reasons = "human", []string{"Dienst aus dem Internet erreichbar"}
	})
	mark(4, func(p *proposal.Proposal) { p.Warnings = []string{"zweiter Dienst für denselben Zweck"} })
	do := inboxMux(env)
	if body := do("GET", "/inbox", nil); !strings.Contains(body, "Alle freigeben (2)") {
		t.Fatalf("the Inbox counts what Alle freigeben would leave:\n%s", body)
	}
	confirm := do("GET", "/inbox/alle", nil)
	if !strings.Contains(confirm, "Ja, alle 2 freigeben") || strings.Count(confirm, "name=id") != 2 {
		t.Fatalf("the confirmation covers more than #1 and #5:\n%s", confirm)
	}
	for _, w := range []string{"Diese gibst du einzeln frei", "#2 ", "löscht etwas", "#3 ", "Laib will, dass du ihn dir selbst ansiehst", "#4 ", "hat einen Hinweis"} {
		if !strings.Contains(confirm, w) {
			t.Errorf("the confirmation lacks %q:\n%s", w, confirm)
		}
	}
	do("POST", "/inbox/alle", url.Values{"id": {"1", "2", "3", "4", "5"}})
	until(t, "#1 and #5 settled", settled(env, 1, 5))
	for _, id := range []int{2, 3, 4} {
		if p, _ := proposal.Get(cli.InboxDir(env), id); p.Status != "open" {
			t.Errorf("#%d is %s, want open: Alle freigeben approved it", id, p.Status)
		}
	}
	if body := do("GET", "/inbox", nil); !strings.Contains(body, "/inbox/2/approve") || !strings.Contains(body, "/inbox/3/approve") || strings.Contains(body, "Alle freigeben") {
		t.Errorf("the left cards lack their own Freigeben:\n%s", body)
	}
}
