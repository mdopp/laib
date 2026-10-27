package proposal

import (
	"strings"
	"testing"
)

// cardText is every word a card shows outside its technical details.
func cardText(c Decision) string {
	parts := []string{c.Summary, c.Reversible, c.Why, c.RiskWhy, c.Decider}
	parts = append(parts, c.Impact...)
	parts = append(parts, c.Warnings...)
	for _, l := range c.Changes {
		parts = append(parts, l.Text)
	}
	return strings.Join(parts, "\n")
}

// #616: a card in four parts — what changes (before → after, + and −),
// what it means, who wants it and why, how it is undone — with a headline
// from the change itself, a weight (gering/mittel/hoch) and the hands by
// the names the family knows. No word of Laib's own vocabulary.
func TestCardsSayWhatChangesAndWhatFollows(t *testing.T) {
	VerbTier = map[string]string{"status": "read", "inbox": "read", "restart": "apply", "zuhause": "propose", "person_signal": "propose", "scan": "propose"}
	t.Cleanup(func() { VerbTier = nil })
	cases := []struct {
		name    string
		p       Proposal
		box     []string
		summary string
		changes []string // lines the card's "Was sich ändert" must hold
		impact  string
		risk    string
		primary string
	}{
		{"a policy grant", Proposal{ID: 74, Actor: "laib", Reason: "Neue Standard-Verben für eingebaute Hände", Kind: "tree", Class: "human", Tier: "apply",
			Files: []string{"policy.yml"}, Reasons: []string{"ändert die Freigaberegeln (policy.yml)"},
			Warnings: []string{`Zweck "voice" wird von 2 Diensten erfüllt: home-assistant, stimme`},
			Changes:  []Change{{Op: "+", Hand: "haus", Verb: "zuhause"}, {Op: "+", Hand: "haus", Path: "people.*.signal"}, {Op: "=", Hand: "haus", Verb: "status"}, {Op: "=", Hand: "haus", Verb: "restart"}}},
			[]string{`Zweck "voice" wird von 2 Diensten erfüllt: home-assistant, stimme`},
			"Haus darf künftig zwei Dinge mehr vorschlagen",
			[]string{"Haus (die Familienseiten) darf zusätzlich", "+ die Adresse des Haushalts ändern lassen", "+ die Signal-Nummer einer Person eintragen lassen", "Was Haus schon darf, bleibt: den Zustand der Dienste sehen, Dienste neu starten"},
			"ändert dadurch nichts selbst", "mittel", "Später"},
		{"a policy revoke", Proposal{ID: 75, Actor: "human:mdopp", Reason: "Haus soll nicht mehr neu starten", Kind: "tree", Class: "human", Tier: "apply",
			Files: []string{"policy.yml"}, Reasons: []string{"ändert die Freigaberegeln (policy.yml)"},
			Changes: []Change{{Op: "-", Hand: "haus", Verb: "restart"}, {Op: "=", Hand: "haus", Verb: "status"}}},
			nil, "Haus darf künftig nicht mehr Dienste neu starten",
			[]string{"− Dienste neu starten"}, "kann danach nicht mehr", "mittel", "Später"},
		{"a value", Proposal{ID: 80, Actor: "haus", Reason: "Scanner gefunden", Kind: "values", Class: "values", Tier: "apply",
			Files:   []string{"services/scan/values.yml"},
			Changes: []Change{{Op: "~", Service: "scan", Key: "SCANNER", Old: "", New: "epson2:net:192.168.178.87"}}},
			nil, "Scannen: SCANNER wird epson2:net:192.168.178.87",
			[]string{"SCANNER: (leer) → epson2:net:192.168.178.87"}, "Scan", "gering", "Freigeben"},
		{"a new service", Proposal{ID: 81, Actor: "ops", Reason: "Dokumente scannen", Kind: "new-service", Class: "new-service", Tier: "apply", Adds: []string{"paperless"},
			Files: []string{"services/paperless/laib.yml", "box.yml"}, Facts: []string{"paperless ist unter paperless.dopp.home nur im Heimnetz erreichbar"}},
			nil, "Paperless wird installiert", nil, "nur im Heimnetz", "hoch", "Später"},
		{"a template update", Proposal{ID: 82, Actor: "laib", Reason: "neue Vorlage", Kind: "template-update", Class: "template-update", Tier: "apply",
			Files: []string{"services/media/laib.yml"}},
			nil, "Jellyfin bekommt die neue Vorlage", nil, "nichts Sichtbares", "gering", "Freigeben"},
	}
	for _, tc := range cases {
		c := Card(&tc.p, "dopp.home", tc.box...)
		if !strings.HasPrefix(c.Summary, tc.summary) {
			t.Errorf("%s: headline %q, want %q", tc.name, c.Summary, tc.summary)
		}
		var lines []string
		for _, l := range c.Changes {
			lines = append(lines, strings.TrimSpace(l.Op+" "+l.Text))
		}
		got := strings.Join(lines, "\n")
		for _, w := range tc.changes {
			if !strings.Contains(got, w) {
				t.Errorf("%s: changes lack %q:\n%s", tc.name, w, got)
			}
		}
		if !strings.Contains(strings.Join(c.Impact, " "), tc.impact) {
			t.Errorf("%s: impact %v lacks %q", tc.name, c.Impact, tc.impact)
		}
		if c.Risk != tc.risk || c.RiskWhy == "" {
			t.Errorf("%s: risk %q (%q), want %q with a reason", tc.name, c.Risk, c.RiskWhy, tc.risk)
		}
		if c.Primary != tc.primary {
			t.Errorf("%s: primary %q, want %q", tc.name, c.Primary, tc.primary)
		}
		if tc.p.Class == "human" && c.Decider != "Das entscheidest du, kein Agent." {
			t.Errorf("%s: decider %q", tc.name, c.Decider)
		}
		if len(c.Warnings) != 0 {
			t.Errorf("%s: a box-wide warning about other services is on the card: %v", tc.name, c.Warnings)
		}
		text := strings.ReplaceAll(cardText(c), tc.p.Reason, "") // the proposer's own words are quoted as they are
		for _, bad := range []string{"policy.yml", "Verb", "Hand", "Stufe", "apply", "*.", "Freigaberegeln"} {
			if strings.Contains(text, bad) {
				t.Errorf("%s: the card says %q:\n%s", tc.name, bad, text)
			}
		}
		if !strings.Contains(c.Details, tc.p.Actor) || !strings.Contains(c.Details, tc.p.Files[0]) {
			t.Errorf("%s: the technical details name the proposer and the files: %q", tc.name, c.Details)
		}
	}
	// a box warning about a service the proposal touches stays on its card
	p := Proposal{ID: 83, Actor: "ops", Reason: "Stimme", Kind: "values", Class: "values", Files: []string{"services/stimme/values.yml"},
		Warnings: []string{`Zweck "voice" wird von 2 Diensten erfüllt: home-assistant, stimme`}}
	if c := Card(&p, "dopp.home", p.Warnings...); len(c.Warnings) != 1 {
		t.Errorf("a warning naming the touched service must stay: %v", c.Warnings)
	}
}
