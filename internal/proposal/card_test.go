package proposal

import (
	"strings"
	"testing"
)

// #58: every decision shows the same card — what changes in one sentence,
// what it means for the family, whether it can be undone, who asked and
// why, and a safe default. Mechanical, from the classified change.
// Ablauf L-2
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
			"Jellyfin", "aus dem Internet erreichbar", "mit einem Tipp", "Später"},
		{"a patch with a hook", Proposal{ID: 10, Actor: "ops", Reason: "Hook repariert", Kind: "patch", Class: "human", Files: []string{"services/frage/post-deploy"},
			Reasons: []string{"führt ein Skript direkt auf der Box aus (services/frage/post-deploy)"}},
			"Frage", "direkt auf der Box", "mit einem Tipp", "Später"},
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
		if !strings.Contains(strings.ToLower(c.Why), strings.ToLower(HandName(tc.p.Actor, false))) || !strings.Contains(c.Why, tc.p.Reason) {
			t.Errorf("%s: why %q must name the proposer and quote the reason", tc.name, c.Why)
		}
		for _, s := range append([]string{c.Summary, c.Reversible, c.Why}, c.Impact...) {
			if strings.Contains(s, "diff") || strings.Contains(s, "git ") || strings.Contains(s, ".yml") {
				t.Errorf("%s: not plain language: %q", tc.name, s)
			}
		}
	}
}

// Gesundheit's retirement (2026-11-06): a removal is the headline, even when
// the same change empties values elsewhere; „Zurücknehmen“ brings the
// service back to its data; the reason reads as a plain sentence.
func TestARemovalSaysSo(t *testing.T) {
	p := Proposal{ID: 11, Actor: "claude", Reason: "Paperless macht das jetzt", Kind: "patch", Class: "human",
		Files:   []string{"services/gesundheit/laib.yml", "services/bote/values.yml", "box.yml"},
		Removes: []string{"gesundheit"}, Reasons: []string{"entfernt den Dienst gesundheit"},
		Facts:   []string{"Die Daten von Gesundheit bleiben auf der Platte und werden weiter gesichert."},
		Changes: []Change{{Op: "~", Service: "bote", Key: "GESUNDHEIT_MCP_URL", Old: "http://x/mcp", New: ""}}}
	c := Card(&p, "dopp.home")
	if c.Summary != "Gesundheit wird von der Box entfernt" {
		t.Errorf("summary %q", c.Summary)
	}
	impact := strings.Join(c.Impact, " ")
	for _, want := range []string{"weil ein Dienst von der Box geht", "werden weiter gesichert", "Bote startet dafür kurz neu"} {
		if !strings.Contains(impact, want) {
			t.Errorf("impact %q lacks %q", impact, want)
		}
	}
	if strings.Contains(impact, "Achtung: entfernt") {
		t.Errorf("the reason as Laib's words: %q", impact)
	}
	if !c.Undoable || !strings.Contains(c.Reversible, "kommt zurück und findet seine Daten wieder") {
		t.Errorf("reversible %v %q", c.Undoable, c.Reversible)
	}
	p.Removes = []string{"gesundheit", "frage"}
	if c := Card(&p, "dopp.home"); c.Summary != "Gesundheit, Fragen werden von der Box entfernt" {
		t.Errorf("two: %q", c.Summary)
	}
}
