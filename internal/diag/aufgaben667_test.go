package diag

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/aufgaben"
)

var tasks667 = []aufgaben.Aufgabe{
	{ID: 1, Titel: "Wächter von außen", FertigWenn: "watchdog", Erinnern: "2026-12-01"},
	{ID: 2, Titel: "Zweite Datenplatte", FertigWenn: "raid"},
	{ID: 3, Titel: "Sicherung außer Haus", FertigWenn: "backup offsite", Erinnern: "2026-11-01"},
	{ID: 4, Titel: "erledigt", FertigWenn: "boot", Erledigt: &aufgaben.Erledigt{Am: "2026-10-20", Von: "claude"}},
}

// #667: a finding an open Box-Aufgabe covers is info, „zurückgestellt →
// Box-Aufgabe #N“, until its erinnern date; from then on gelb with
// „erinnert“. A red stays red: a deferral silences a nudge, never damage.
// A finding that could not be measured is never covered.
func TestACoveredFindingIsDeferredUntilItsReminder(t *testing.T) {
	fs := []Finding{
		{Severity: "gelb", Probe: "watchdog", Detail: "kein Wächter"},
		{Severity: "gelb", Probe: "raid", Detail: "md127: 1 von 2 Platten"},
		{Severity: "info", Probe: "backup", Topic: "offsite", Detail: "Backup liegt nur auf der Datenplatte"},
		{Severity: "rot", Probe: "backup", Detail: "die letzte Sicherung ist fehlgeschlagen"},
		{Severity: "gelb", Probe: "boot", Detail: "/boot hat 59 MiB frei"},
	}
	got := cover(slices.Clone(fs), tasks667, "2026-11-15")
	want := []struct {
		sev, says string
		task      int
	}{
		{"info", "zurückgestellt → Box-Aufgabe #1", 1},
		{"info", "zurückgestellt → Box-Aufgabe #2", 2},
		{"gelb", "erinnert", 3},
		{"rot", "", 0},
		{"gelb", "", 0},
	}
	for i, w := range want {
		f := got[i]
		if f.Severity != w.sev || f.Aufgabe != w.task || w.says != "" && !strings.Contains(f.Detail, w.says) || w.says == "" && f.Detail != fs[i].Detail {
			t.Errorf("%s: %+v, want %s %q #%d", fs[i].Probe, f, w.sev, w.says, w.task)
		}
	}
	if !strings.Contains(got[2].Detail, "Box-Aufgabe #3") {
		t.Errorf("a reminded finding still names its task: %q", got[2].Detail)
	}
	silent := cover([]Finding{{Severity: "gelb", Probe: "raid", Detail: "laib-disk antwortet nicht", Unmeasured: true}}, tasks667, "2026-11-15")
	if silent[0].Severity != "gelb" || silent[0].Aufgabe != 0 {
		t.Errorf("an unmeasured finding was covered: %+v", silent[0])
	}
	red := cover([]Finding{{Severity: "rot", Probe: "watchdog", Detail: "x"}}, tasks667, "2026-11-15")
	if red[0].Severity != "rot" || red[0].Aufgabe != 1 {
		t.Errorf("a red covered finding stays red and names its task: %+v", red[0])
	}
}

// When diag measured a probe and no longer sees the finding, its task is
// done; a probe that could not be measured, or whose absence is no
// measurement at all, never closes one.
func TestATaskClosesOnlyWhenItsFindingIsMeasuredGone(t *testing.T) {
	open := append(slices.Clone(tasks667[:3]),
		aufgaben.Aufgabe{ID: 5, Titel: "Dienst", FertigWenn: "unit immich"},
		aufgaben.Aufgabe{ID: 6, Titel: "ohne Befund"},
		aufgaben.Aufgabe{ID: 7, Titel: "acme", FertigWenn: "acme"})
	// watchdog configured, offsite copy exists, the RAID probe silent
	fs := []Finding{{Severity: "gelb", Probe: "raid", Detail: "laib-disk antwortet nicht", Unmeasured: true}}
	if got := gone(fs, open); !slices.Equal(got, []int{1, 3}) {
		t.Fatalf("gone = %v, want [1 3]: watchdog and offsite measured away; raid unmeasured; unit, acme and none never", got)
	}
	// the mirror is in: laib-disk answered without a raid finding
	if got := gone(nil, open[1:2]); !slices.Equal(got, []int{2}) {
		t.Fatalf("raid measured away: %v", got)
	}
	// still there: nothing closes
	fs = []Finding{{Severity: "gelb", Probe: "watchdog"}, {Severity: "info", Probe: "backup", Topic: "offsite"}, {Severity: "gelb", Probe: "raid"}}
	if got := gone(fs, open); len(got) != 0 {
		t.Fatalf("closed %v while their findings are there", got)
	}
	// a done task is not closed again
	if got := gone(nil, tasks667[3:]); len(got) != 0 {
		t.Fatalf("closed a done task: %v", got)
	}
}

// The owner, 2026-10-25: „Rot bleibt rot“, and a backup on the data disk
// alone is a risk, not damage: info for the first week, then gelb however
// long the box runs, so a deferred Box-Aufgabe can cover it.
func TestABackupOnlyOnTheDataDiskIsARiskNotRed(t *testing.T) {
	if s := localOnlySeverity(time.Now().Add(-60 * 24 * time.Hour)); s != "gelb" {
		t.Fatalf("a local-only backup two months after install is %q, want gelb", s)
	}
	if s := localOnlySeverity(time.Now().Add(-2 * 24 * time.Hour)); s != "info" {
		t.Fatalf("in the first week it is %q, want info", s)
	}
}
