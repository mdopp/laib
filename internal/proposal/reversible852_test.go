package proposal

import (
	"slices"
	"strings"
	"testing"
)

// #852: how a proposal is undone is computed from what it does, never
// promised. „Zurücknehmen“ shows only where it undoes something, „mit
// einem Tipp“ and „Daten bleiben“ only where they are true, and a service
// with data weighs at least „mittel“.
func TestTheWayBackIsComputedFromWhatTheProposalDoes(t *testing.T) {
	ask := "ein neues Image für immich (v1 → v2), dessen Daten es migrieren kann: vorher sichert Laib immich; Zurücknehmen mit laib rollback, die Daten von vorher mit laib restore"
	cases := []struct {
		name     string
		p        Proposal
		undoable bool
		want     []string // in the undo text
		risky    bool     // risk at least mittel, Später first
	}{
		{"a restart repair", Proposal{Actor: "ops", Reason: "Reparieren: whoami läuft nicht — laib restart whoami", Tier: "apply", Class: "tree", Kind: "tree",
			Files: []string{"repairs/2026-10-28T10-00-00.000000.md"}, Anchor: Anchor{Finding: "unit whoami", Undo: "nichts zurückzunehmen: laib führt nur laib restart whoami aus", Does: "Whoami wird neu gestartet"}},
			false, []string{"Ein Neustart ändert nichts Bleibendes; es gibt nichts zurückzunehmen."}, false},
		{"a flash", Proposal{Actor: "ops", Reason: "Reparieren: Firmware veraltet — laib flash satellit", Tier: "destroy", Class: "tree", Kind: "tree",
			Files: []string{"repairs/2026-10-28T10-00-01.000000.md"}, Anchor: Anchor{Finding: "firmware satellit", Undo: "nichts zurückzunehmen: laib führt nur laib flash satellit aus"}},
			false, []string{"Firmware"}, true},
		{"a retrain", Proposal{Actor: "ops", Reason: "Reparieren: Modell schlecht — laib retrain wakeword", Tier: "destroy", Class: "tree", Kind: "tree",
			Files: []string{"repairs/2026-10-28T10-00-02.000000.md"}},
			false, []string{"Modell"}, true},
		{"a value change", Proposal{Actor: "pi", Reason: "neues Modell", Tier: "apply", Kind: "values", Class: "values", Files: []string{"services/bote/values.yml"},
			Changes: []Change{{Op: "~", Service: "bote", Key: "MODEL", Old: "a", New: "b"}}},
			true, []string{"mit einem Tipp", "Daten bleiben"}, false},
		{"an image update of a service with data", Proposal{Actor: "laib", Reason: "laib updates", Tier: "apply", Kind: "values", Class: "values", Files: []string{"services/immich/values.yml"},
			Changes: []Change{{Op: "~", Service: "immich", Key: "IMAGE", Old: "immich:v1", New: "immich:v2"}}, Snapshot: []string{"immich"}, Warnings: []string{ask}},
			true, []string{"Programm", "mit einem Tipp", "Sicherung"}, true},
		{"an add", Proposal{Actor: "ops", Reason: "Dokumente scannen", Tier: "apply", Kind: "new-service", Class: "new-service", Adds: []string{"paperless"}, Files: []string{"services/paperless/laib.yml"}},
			true, []string{"mit einem Tipp", "seine Daten bleiben"}, true},
		{"a destroy", Proposal{Actor: "human:core", Reason: "weg damit", Tier: "destroy", Kind: "tree", Class: "tree", Files: []string{"services/alt/laib.yml"}},
			false, []string{"nicht"}, true},
	}
	for _, tc := range cases {
		c := Card(&tc.p, "dopp.home")
		undoable, text := Undoing(&tc.p)
		if undoable != tc.undoable || c.Undoable != tc.undoable {
			t.Errorf("%s: undoable %v, card %v, want %v", tc.name, undoable, c.Undoable, tc.undoable)
		}
		if text != c.Reversible && !strings.HasPrefix(c.Reversible, text) {
			t.Errorf("%s: the card's undo text %q is not the computed one %q", tc.name, c.Reversible, text)
		}
		for _, w := range tc.want {
			if !strings.Contains(c.Reversible, w) {
				t.Errorf("%s: undo text %q lacks %q", tc.name, c.Reversible, w)
			}
		}
		if !tc.undoable {
			for _, s := range []string{c.Reversible, c.RiskWhy} {
				if strings.Contains(s, "mit einem Tipp") || strings.Contains(s, "Daten bleiben") || strings.Contains(s, "laib rollback") {
					t.Errorf("%s: promises a way back that is not there: %q", tc.name, s)
				}
			}
		}
		if len(tc.p.Snapshot) > 0 && strings.Contains(c.Reversible, "Daten bleiben") {
			t.Errorf("%s: the data do not come back with the program: %q", tc.name, c.Reversible)
		}
		if risky := c.Risk != "gering"; risky != tc.risky || c.Risky != tc.risky {
			t.Errorf("%s: risk %s (risky %v), want at least mittel: %v", tc.name, c.Risk, c.Risky, tc.risky)
		}
		if slices.ContainsFunc(c.Warnings, func(w string) bool { return strings.Contains(w, "laib restore") }) && c.Risk == "gering" {
			t.Errorf("%s: a data warning weighs gering", tc.name)
		}
	}
}
