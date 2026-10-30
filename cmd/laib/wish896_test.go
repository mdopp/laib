package main

import (
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/proposal"
)

// #896: the Operator's `laib wish --von operator "…"` — the flag is the
// asker, not the title's first word, and the title ends at a sentence's
// end, not at a dot inside an address.
func TestAnOperatorWishTitleTakesTheFlagAndASentence(t *testing.T) {
	c := newCbox(t)
	c.env.Actor = "ops"
	text := "Prüfe die Netzwerkverbindung und DNS-Forwarding (192.178.168.192). Gesehen: Timeouts."
	if rc := c.wish(t, "--von", "operator", text); rc != cli.ExitOK {
		t.Fatalf("rc %d", rc)
	}
	ps, err := proposal.List(cli.InboxDir(c.env), true)
	if err != nil || len(ps) != 1 {
		t.Fatalf("one proposal: %v %v", ps, err)
	}
	p := ps[0]
	if p.Reason != "Wunsch: Prüfe die Netzwerkverbindung und DNS-Forwarding (192.178.168.192)." {
		t.Fatalf("title %q", p.Reason)
	}
	if strings.Contains(p.Wish, "--von") || !strings.Contains(p.Wish, "Gesehen: Timeouts.") || !strings.Contains(p.Wish, "operator") {
		t.Fatalf("the text in full with its asker, no flag: %q", p.Wish)
	}
	if rc := c.wish(t, strings.Repeat("lang ", 30)); rc != cli.ExitOK {
		t.Fatalf("rc %d", rc)
	}
	ps, _ = proposal.List(cli.InboxDir(c.env), true)
	for _, p := range ps {
		if r := []rune(strings.TrimPrefix(p.Reason, "Wunsch: ")); !strings.Contains(p.Reason, "Prüfe") && (len(r) != 71 || r[70] != '…') {
			t.Fatalf("cut at 70 runes: %q", p.Reason)
		}
	}
}
