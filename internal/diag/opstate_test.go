package diag

import (
	"testing"
	"time"
)

func TestOperatorState(t *testing.T) {
	now := time.Now()
	tick := 10 * time.Minute
	cases := []struct {
		name  string
		f     OpFacts
		state string
		sev   string
	}{
		{"no model, not enabled", OpFacts{Now: now, Tick: tick}, "aus", ""},
		{"model but not enabled", OpFacts{ModelURL: "http://m/v1", Now: now, Tick: tick}, "aus", "gelb"},
		{"enabled, unit down", OpFacts{Enabled: true, ModelURL: "http://m/v1", Now: now, Tick: tick}, "fehlt", "rot"},
		{"unit up, no answer", OpFacts{Enabled: true, UnitActive: true, Now: now, Tick: tick}, "antwortet nicht", "rot"},
		{"fresh, no note yet", OpFacts{Enabled: true, UnitActive: true, Answers: true, Now: now, Tick: tick}, "startet", ""},
		{"recent note", OpFacts{Enabled: true, UnitActive: true, Answers: true, LastNote: now.Add(-5 * time.Minute), Now: now, Tick: tick}, "läuft", ""},
		{"stale note", OpFacts{Enabled: true, UnitActive: true, Answers: true, LastNote: now.Add(-45 * time.Minute), Now: now, Tick: tick}, "stumm", "gelb"},
	}
	for _, c := range cases {
		state, f := OperatorState(c.f)
		sev := ""
		if f != nil {
			sev = f.Severity
		}
		if state != c.state || sev != c.sev {
			t.Errorf("%s: got %q/%q, want %q/%q", c.name, state, sev, c.state, c.sev)
		}
	}
}
