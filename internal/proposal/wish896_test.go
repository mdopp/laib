package proposal

import (
	"strings"
	"testing"
)

// #896: an Operator's wish reads on its card as what it is — „Wunsch an die
// Werkstatt: …“, its text in full, what follows the approval — never as
// „Laib schlägt eine Reparatur vor“ with the text behind Details.
func TestAWishCardReadsAsAWish(t *testing.T) {
	text := "Prüfe die Netzwerkverbindung und DNS-Forwarding (192.178.168.192) von AdGuard.\n\nGesehen: DNS-Timeouts seit 05:00.\n\n— vom Operator (ops) auf example.org, 29.10.2026 05:30."
	p := &Proposal{ID: 251, Actor: "ops", Tier: "propose", Class: "human",
		Reason:  "Wunsch: Prüfe die Netzwerkverbindung und DNS-Forwarding (192.178.168.192) von AdGuard.",
		Files:   []string{"wishes/2026-10-29T05-30-00.000000.md"},
		Reasons: []string{"ein Wunsch an die Werkstatt (wishes/2026-10-29T05-30-00.000000.md): nach der Freigabe wird daraus ein Ticket"},
		Anchor:  Anchor{Wish: text}}
	c := Card(p, "example.org")
	if c.Summary != "Wunsch an die Werkstatt: Prüfe die Netzwerkverbindung und DNS-Forwarding (192.178.168.192) von AdGuard." {
		t.Fatalf("headline %q", c.Summary)
	}
	if !strings.Contains(c.Why, "Gesehen: DNS-Timeouts seit 05:00.") || !strings.Contains(c.Why, "vom Operator") {
		t.Fatalf("the wish text in full: %q", c.Why)
	}
	if strings.Contains(c.Why, "Technische Details") {
		t.Fatalf("the text is on the card, not behind Details: %q", c.Why)
	}
	if strings.Join(c.Impact, " ") != "Nach der Freigabe wird daraus ein Ticket; an der Box ändert sich nichts." {
		t.Fatalf("impact %q", c.Impact)
	}
	// a wish filed before #896 has no text on record: still a wish, never a repair
	p.Wish = ""
	if c := Card(p, "example.org"); !strings.HasPrefix(c.Summary, "Wunsch an die Werkstatt: Prüfe") {
		t.Fatalf("an older wish: %q", c.Summary)
	}
}
