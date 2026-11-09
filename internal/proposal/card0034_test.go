package proposal

import (
	"strings"
	"testing"
)

// ADR 0034 (owner, 2026-11-09): „Ich will eine grobe Zusammenfassung bei
// der Frage sehen, was sich geändert hat.“ The nightly card names every
// new image by its version, each followed by the model's summary, marked
// as automatic; one without notes says so in one line. The pins of the
// floating values say that nothing new comes.
func TestTheNightlyCardCarriesTheSummaries(t *testing.T) {
	p := Proposal{ID: 1, Actor: "ops", Reason: "Neue Versionen: adguard, homes", Kind: "values", Class: "human", Tier: "apply",
		Files: []string{"services/adguard/values.yml", "services/homes/values.yml"},
		Changes: []Change{
			{Op: "~", Service: "adguard", Key: "IMAGE", Old: "docker.io/adguard/adguardhome:latest", New: "docker.io/adguard/adguardhome:latest@sha256:a2", OldVersion: "v0.107.55", NewVersion: "v0.107.56"},
			{Op: "~", Service: "homes", Key: "IMAGE", Old: "ghcr.io/mdopp/homes:latest@sha256:h1", New: "ghcr.io/mdopp/homes:latest@sha256:h2"},
		},
		Reasons: []string{"ein neues Image für adguard (…): neue Software nur auf deinen Tipp (ADR 0034)"},
		Anchor:  Anchor{Notes: map[string]string{"adguard/IMAGE": "Schließt eine Sicherheitslücke im DNS-Filter.", "homes/IMAGE": "Keine Änderungsnotizen gefunden."}},
	}
	d := Card(&p, "box.test")
	if !strings.HasPrefix(d.Summary, "Neue Versionen für ") {
		t.Errorf("headline %q", d.Summary)
	}
	text := cardText(d)
	for _, want := range []string{"Update v0.107.55 → v0.107.56\nZusammenfassung (automatisch): Schließt eine Sicherheitslücke im DNS-Filter.",
		"Zusammenfassung (automatisch): Keine Änderungsnotizen gefunden.", "neue Software nur auf deinen Tipp auf die Box kommt"} {
		if !strings.Contains(text, want) {
			t.Errorf("the card lacks %q:\n%s", want, text)
		}
	}
	if d.Decider == "" {
		t.Error("a new image is the person's decision, no agent's")
	}
	pin := Proposal{ID: 2, Actor: "ops", Reason: "Images festhalten: adguard, radicale", Kind: "values", Class: "human", Tier: "apply",
		Changes: []Change{{Op: "~", Service: "adguard", Key: "IMAGE", Old: "docker.io/adguard/adguardhome:latest", New: "docker.io/adguard/adguardhome:latest@sha256:a1", Pin: true},
			{Op: "~", Service: "radicale", Key: "IMAGE", Old: "docker.io/tomsquest/docker-radicale:latest", New: "docker.io/tomsquest/docker-radicale:latest@sha256:r1", Pin: true}}}
	d = Card(&pin, "box.test")
	if !strings.HasSuffix(d.Summary, "laufen weiter wie jetzt, fest vermerkt") || !strings.Contains(cardText(d), "läuft weiter wie jetzt") || strings.Contains(cardText(d), "Update") {
		t.Errorf("a pin is no update: %q\n%s", d.Summary, cardText(d))
	}
}
