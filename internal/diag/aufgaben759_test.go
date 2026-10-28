package diag

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/mdopp/laib/internal/aufgaben"
	"github.com/mdopp/laib/internal/state"
)

// #759: a template's status file closes a Box-Aufgabe by its topic (status
// wakeword: the wake word on the device), but only when it measured: a
// status that says it could not look, or a status file that is not there,
// closes nothing.
func TestAStatusFindingClosesItsTaskOnlyWhenMeasuredGone(t *testing.T) {
	task := []aufgaben.Aufgabe{{ID: 4, Titel: "Voice PE flashen", FertigWenn: "status wakeword"}}
	there := []Finding{{Severity: "gelb", Probe: "status", Service: "stimme", Topic: "wakeword"}}
	if got := cover(slices.Clone(there), task, "2026-10-26"); got[0].Aufgabe != 4 || got[0].Severity != "info" {
		t.Fatalf("the task covers the device finding: %+v", got[0])
	}
	if got := gone(there, task); len(got) != 0 {
		t.Fatalf("still there, closed: %v", got)
	}
	if got := gone([]Finding{{Severity: "info", Probe: "status", Service: "stimme", Topic: "wakeword", Unmeasured: true}}, task); len(got) != 0 {
		t.Fatalf("not measured, closed: %v", got)
	}
	if got := gone(nil, task); !slices.Equal(got, []int{4}) {
		t.Fatalf("measured away, still open: %v", got)
	}
}

func TestAMissingStatusFileMeasuredNothing(t *testing.T) {
	dir := t.TempDir()
	svc := &state.Service{Name: "stimme", Contract: state.Contract{Status: &state.StatusFile{File: "${DATA_DIR}/s.json"}}}
	st := &state.State{Box: state.Box{DataDir: dir}, Services: map[string]*state.Service{"stimme": svc}}
	if fs := unmeasuredStatus(st, nil); len(fs) != 1 || !fs[0].Unmeasured || fs[0].Probe != "status" {
		t.Fatalf("no status file is no measurement: %+v", fs)
	}
	os.WriteFile(filepath.Join(dir, "s.json"), []byte(`{"detail":"x","findings":[{"severity":"gelb","detail":"d","topic":"wakeword","unmeasured":true,"repair":["flash","esphome"],"effect":"e","undo":"u"}]}`), 0o644)
	if fs := unmeasuredStatus(st, nil); len(fs) != 0 {
		t.Fatalf("a status file is there: %+v", fs)
	}
	f := ReadStatus(st, svc).Findings[0]
	if f.Topic != "wakeword" || !f.Unmeasured || !slices.Equal(f.Repair, []string{"flash", "esphome"}) || f.Effect != "e" || f.Undo != "u" {
		t.Fatalf("a status file's finding carries topic, unmeasured, repair, effect and undo: %+v", f)
	}
}
