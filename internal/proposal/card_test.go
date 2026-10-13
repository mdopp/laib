package proposal

import (
	"strings"
	"testing"
)

// #58: every decision shows the same card — what changes in one sentence,
// what it means for the family, whether it can be undone, who asked and
// why, and a safe default. Mechanical, from the classified change.
func TestCardsArePlainGermanAndDefaultToSpaeterWhenRisky(t *testing.T) {
	cases := []struct {
		name    string
		p       Proposal
		summary string
		impact  string
		undo    string
		primary string
	}{
		{"an add", Proposal{ID: 7, Actor: "ops", Reason: "Dokumente scannen", Kind: "new-service", Class: "new-service", Adds: []string{"paperless"},
			Facts: []string{"paperless ist unter paperless.dopp.home nur im Heimnetz erreichbar"}},
			"Paperless wird installiert", "nur im Heimnetz", "Daten bleiben", "Später"},
		{"a values change", Proposal{ID: 8, Actor: "pi", Reason: "neues Modell", Kind: "values", Class: "values", Files: []string{"services/bote/values.yml"}},
			"Einstellungen von Bote ändern sich", "nichts Sichtbares", "mit einem Tipp", "Freigeben"},
		{"a public route", Proposal{ID: 9, Actor: "claude", Reason: "von unterwegs", Kind: "tree", Class: "human", Files: []string{"services/media/laib.yml"},
			Reasons: []string{"macht media aus dem Internet erreichbar (media)"}},
			"Media", "aus dem Internet erreichbar", "mit einem Tipp", "Später"},
		{"a patch with a hook", Proposal{ID: 10, Actor: "ops", Reason: "Hook repariert", Kind: "patch", Class: "human", Files: []string{"services/frage/post-deploy"},
			Reasons: []string{"führt ein Skript direkt auf der Box aus (services/frage/post-deploy)"}},
			"Frage", "Skript direkt auf der Box", "mit einem Tipp", "Später"},
	}
	for _, tc := range cases {
		c := Card(&tc.p, "dopp.home")
		if !strings.Contains(c.Summary, tc.summary) {
			t.Errorf("%s: summary %q lacks %q", tc.name, c.Summary, tc.summary)
		}
		if !strings.Contains(strings.Join(c.Impact, " "), tc.impact) {
			t.Errorf("%s: impact %v lacks %q", tc.name, c.Impact, tc.impact)
		}
		if !strings.Contains(c.Reversible, tc.undo) {
			t.Errorf("%s: reversible %q lacks %q", tc.name, c.Reversible, tc.undo)
		}
		if c.Primary != tc.primary {
			t.Errorf("%s: primary %q, want %q", tc.name, c.Primary, tc.primary)
		}
		if !strings.Contains(c.Why, tc.p.Actor) || !strings.Contains(c.Why, tc.p.Reason) {
			t.Errorf("%s: why %q must quote actor and reason", tc.name, c.Why)
		}
		for _, s := range append([]string{c.Summary, c.Reversible, c.Why}, c.Impact...) {
			if strings.Contains(s, "diff") || strings.Contains(s, "git ") || strings.Contains(s, ".yml") {
				t.Errorf("%s: not plain language: %q", tc.name, s)
			}
		}
	}
}
