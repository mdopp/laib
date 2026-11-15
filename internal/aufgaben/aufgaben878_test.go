package aufgaben

import (
	"os"
	"path/filepath"
	"testing"
)

// #878: a task says for whom (fuer), its guided way (weg) and the finding
// whose disappearance closes it (fertig_wenn). An aufgaben.yml from before
// loads unchanged: no fuer is the admins', a befund is its fertig_wenn.
func TestAnOldListLoadsAsTheAdminsWithItsBefundAsFertigWenn(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, File), []byte("aufgaben:\n  - id: 1\n    titel: Wächter\n    seit: \"2026-10-25\"\n    von: claude\n    befund: watchdog\n"), 0o644)
	list, err := Load(dir)
	if err != nil || len(list) != 1 {
		t.Fatalf("%v %v", list, err)
	}
	if a := list[0]; a.Fuer != Admins || a.FertigWenn != "watchdog" || a.Befund != "" || a.Weg != nil {
		t.Fatalf("the old task: %+v", a)
	}
}

func TestFuerAndWeg(t *testing.T) {
	for _, ok := range []string{"admins", "alle", "person:anna", "person:m.dopp"} {
		if err := Fuer(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "Admins", "person:", "person:Anna", "anna", "person:a b", "familie"} {
		if Fuer(bad) == nil {
			t.Errorf("%q was taken for a fuer", bad)
		}
	}
	// the way laib-haushalt composed (#1234): one of three, a page only as a Homes path
	for _, ok := range []Weg{{Seite: "/stimme"}, {Gespraech: true}, {Schluessel: "OPENAI_KEY"}} {
		if err := CheckWeg(ok); err != nil {
			t.Errorf("%+v: %v", ok, err)
		}
	}
	for _, bad := range []Weg{{}, {Seite: "https://evil.example/"}, {Seite: "//evil.example"}, {Seite: "/x?y=<b>"}, {Seite: "/stimme", Gespraech: true}} {
		if CheckWeg(bad) == nil {
			t.Errorf("%+v was taken for a weg", bad)
		}
	}
}

// Homes and the Bote act for one person: they see the admins' and
// everyone's tasks, and of the personal ones only that person's. A hand
// that sees the whole list (a human, claude, ops, laib) sees every one.
// Ablauf L-19
func TestWhoSeesWhichTask(t *testing.T) {
	anna, bert, all, adm := Aufgabe{Fuer: "person:anna"}, Aufgabe{Fuer: "person:bert"}, Aufgabe{Fuer: Alle}, Aufgabe{Fuer: Admins}
	for _, c := range []struct {
		a      Aufgabe
		whole  bool
		person string
		want   bool
	}{
		{anna, false, "anna", true},
		{bert, false, "anna", false},
		{anna, false, "", false},
		{all, false, "", true},
		{adm, false, "", true},
		{adm, false, "anna", true},
		{bert, true, "", true},
		{Aufgabe{}, false, "", true}, // no fuer: the admins'
	} {
		if got := Sees(c.a, c.whole, c.person); got != c.want {
			t.Errorf("Sees(%q, %v, %q) = %v", c.a.Fuer, c.whole, c.person, got)
		}
	}
}

// A fertig_wenn on a probe whose silence is no measurement never closes
// its task: the verb says so instead of promising it.
func TestClosesItself(t *testing.T) {
	for _, c := range []struct {
		fertigWenn string
		want       bool
	}{{"watchdog", true}, {"status stimme", true}, {"backup offsite", true}, {"restore-test", true}, {"boot", false}, {"unit immich", false}, {"", false}} {
		if got := ClosesItself(c.fertigWenn); got != c.want {
			t.Errorf("ClosesItself(%q) = %v", c.fertigWenn, got)
		}
	}
}
