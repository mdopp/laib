package diag

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/aufgaben"
	"github.com/mdopp/laib/internal/state"
)

// #1168: a check that knows who is missing asks for that person's task in
// its status file; diag passes the ask on while no task of its fertig_wenn
// is open, covers the finding with the task once it is, without naming the
// person, and the task closes when the check no longer counts the person.
func TestAStatusFindingAsksForAPersonsTaskUntilItIsOpen(t *testing.T) {
	dir := t.TempDir()
	svc := &state.Service{Name: "file-share", Contract: state.Contract{Status: &state.StatusFile{File: "${DATA_DIR}/s.json"}}}
	st := &state.State{Box: state.Box{DataDir: dir}, Services: map[string]*state.Service{"file-share": svc}}
	os.WriteFile(filepath.Join(dir, "s.json"), []byte(`{"detail":"Netzlaufwerk: 0 von 1 Person mit Passwort","findings":[{"severity":"info",
		"detail":"eine Person ohne Passwort fürs Netzlaufwerk","topic":"netzlaufwerk.anna",
		"neue_aufgabe":{"titel":"Netzlaufwerk-Passwort festlegen","warum":"w","fuer":"person:anna","weg":{"seite":"/netzlaufwerk"}}}]}`), 0o644)
	there := ReadStatus(st, svc).Findings
	if a := there[0].NeueAufgabe; a == nil || a.Fuer != "person:anna" || a.Weg == nil || a.Weg.Seite != "/netzlaufwerk" || a.Titel == "" {
		t.Fatalf("the status file's ask is read: %+v", there[0])
	}
	if got := cover(slices.Clone(there), nil, "2026-11-11"); got[0].NeueAufgabe == nil || got[0].Aufgabe != 0 {
		t.Fatalf("no task yet: the finding asks for one: %+v", got[0])
	}
	open := []aufgaben.Aufgabe{{ID: 7, Titel: "Netzlaufwerk-Passwort festlegen", Fuer: "person:anna", FertigWenn: "status netzlaufwerk.anna"}}
	got := cover(slices.Clone(there), open, "2026-11-11")
	if got[0].NeueAufgabe != nil || got[0].Aufgabe != 7 || got[0].Severity != "info" {
		t.Fatalf("an open task: never a second one, and it covers the finding: %+v", got[0])
	}
	if d := got[0].Detail; !strings.Contains(d, "Aufgabe #7 für die Person") || strings.Contains(d, "anna") || strings.Contains(d, "zurückgestellt") {
		t.Fatalf("the admins learn that a task exists, not whose: %q", d)
	}
	byLaib := []aufgaben.Aufgabe{{ID: 7, Fuer: "person:anna", FertigWenn: "status netzlaufwerk.anna", Erledigt: &aufgaben.Erledigt{Von: "laib", Wie: "Befund weg"}}}
	if got := cover(slices.Clone(there), byLaib, "2026-11-11"); got[0].NeueAufgabe == nil {
		t.Fatalf("laib closed the last one and the person is counted again: a new task: %+v", got[0])
	}
	byPerson := []aufgaben.Aufgabe{{ID: 7, Fuer: "person:anna", FertigWenn: "status netzlaufwerk.anna", Erledigt: &aufgaben.Erledigt{Von: "human:mdopp"}}}
	if got := cover(slices.Clone(there), byPerson, "2026-11-11"); got[0].NeueAufgabe != nil {
		t.Fatalf("a person ticked it off: theirs to decide, no new task: %+v", got[0])
	}
	if got := cover([]Finding{{Severity: "info", Probe: "status", Topic: "netzlaufwerk.anna", Unmeasured: true, NeueAufgabe: there[0].NeueAufgabe}}, nil, "2026-11-11"); got[0].NeueAufgabe != nil {
		t.Fatalf("an unmeasured finding asks for nothing: %+v", got[0])
	}
	// closed without a tap: the check counts anna no more
	if ids := gone(there, open); len(ids) != 0 {
		t.Fatalf("still counted, closed: %v", ids)
	}
	if ids := gone([]Finding{{Severity: "info", Probe: "status", Service: "file-share", Detail: "Netzlaufwerk: 1 von 1"}}, open); !slices.Equal(ids, []int{7}) {
		t.Fatalf("counted no more, still open: %v", ids)
	}
}
