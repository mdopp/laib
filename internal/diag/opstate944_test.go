package diag

import (
	"strings"
	"testing"
	"time"
)

// ran is one skill's last run as /ready reports it.
func ran(w *OpWork, skill string, at time.Time, outcome, why string) {
	if w.Skills == nil {
		w.Skills = map[string]struct {
			At      time.Time `json:"at"`
			Outcome string    `json:"outcome"`
			Why     string    `json:"why,omitempty"`
			Caused  string    `json:"caused,omitempty"`
		}{}
	}
	s := w.Skills[skill]
	s.At, s.Outcome, s.Why = at, outcome, why
	w.Skills[skill] = s
}

// #944: the routineblick ran every ten minutes, each run „unchanged, 0
// calls“, and diag said gelb „letzter Blick ist 43m alt … Tick hängt“. A
// finished tick — green, unchanged, skipped with a reason, error — proves
// the Operator alive; only a tick that stopped is gelb.
func TestAnOperatorWhoseTicksEndUnchangedIsAlive(t *testing.T) {
	now, tick := time.Now(), 10*time.Minute
	f := OpFacts{Enabled: true, UnitActive: true, Answers: true, LastNote: now.Add(-45 * time.Minute), Looked: now.Add(-45 * time.Minute), Now: now, Tick: tick}
	for _, ago := range []time.Duration{43, 33, 23, 13, 3} {
		ran(&f.Work, "routineblick", now.Add(-ago*time.Minute), "unchanged", "nichts Neues seit dem letzten Blick")
	}
	if state, finding := OperatorState(f); state != "läuft" || finding != nil {
		t.Fatalf("calm ticks, each unchanged: %q %+v, want läuft and no finding", state, finding)
	}
	// right after a restart /ready has no look yet: the last run counts the same
	f.Looked = time.Time{}
	if state, finding := OperatorState(f); state != "läuft" || finding != nil {
		t.Fatalf("after a restart, a fresh unchanged run: %q %+v, want läuft", state, finding)
	}
	// the tick really stopped: its last run is old as well
	ran(&f.Work, "routineblick", now.Add(-45*time.Minute), "unchanged", "")
	if state, finding := OperatorState(f); state != "stumm" || finding == nil || finding.Severity != "gelb" || !strings.Contains(finding.Detail, "Tick hängt") {
		t.Fatalf("a stopped tick: %q %+v, want stumm/gelb", state, finding)
	}
}

// #944: a tick that ends skipped with a reason is alive and says why its
// work waits, as info; a tick that ends in an error is alive too, but
// works no more: gelb with the error, not „Tick hängt“.
func TestALiveOperatorWithoutWorkSaysWhy(t *testing.T) {
	now, tick := time.Now(), 10*time.Minute
	f := OpFacts{Enabled: true, UnitActive: true, Answers: true, Looked: now.Add(-45 * time.Minute), Now: now, Tick: tick,
		Work: OpWork{Waiting: "die Grafikkarte ist vergeben: gesundheit hält sie"}}
	ran(&f.Work, "routineblick", now.Add(-4*time.Minute), "skipped", f.Work.Waiting)
	if state, finding := OperatorState(f); state != "wartet" || finding == nil || finding.Severity != "info" || !strings.Contains(finding.Detail, "Grafikkarte") {
		t.Fatalf("a reasoned skip: %q %+v, want wartet/info with the reason", state, finding)
	}
	f.Work.Waiting = ""
	ran(&f.Work, "routineblick", now.Add(-4*time.Minute), "error", "Modell: connection refused")
	state, finding := OperatorState(f)
	if state != "stumm" || finding == nil || finding.Severity != "gelb" || !strings.Contains(finding.Detail, "connection refused") || strings.Contains(finding.Detail, "Tick hängt") {
		t.Fatalf("ticks that end in an error: %q %+v, want gelb naming the error", state, finding)
	}
}
