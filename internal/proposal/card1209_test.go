package proposal

import (
	"strings"
	"testing"
)

// #1209: HA's marketplace asks each user for consent to its warning, and
// Laib installs as its own user: the card says plainly that approving the
// extensions is that consent; removing the value asks for none.
// Ablauf L-49
func TestTheExtensionsCardSaysTheApprovalIsTheMarketplaceConsent(t *testing.T) {
	consent := "bestätigt Laib in Home Assistant für seinen eigenen Benutzer den Warnhinweis des Marktplatzes"
	p := &Proposal{Actor: "operator", Reason: "home-assistant: Erweiterungen installieren: SENEC 2026.10.0", Tier: "apply", Kind: "values", Class: "values",
		Files: []string{"services/home-assistant/values.yml"}, Changes: []Change{{Op: "~", Service: "home-assistant", Key: "HA_INSTALL", New: "update.senec=2026.10.0"}}, Snapshot: []string{"home-assistant"}}
	if c := Card(p, "box.test"); !strings.Contains(strings.Join(c.Impact, " "), consent) {
		t.Errorf("the card does not say the approval is the consent: %+v", c.Impact)
	}
	p.Changes = []Change{{Op: "-", Service: "home-assistant", Key: "HA_INSTALL", Old: "update.senec=2026.10.0"}}
	if c := Card(p, "box.test"); strings.Contains(strings.Join(c.Impact, " "), consent) {
		t.Errorf("a card that installs nothing asks for consent: %+v", c.Impact)
	}
}
