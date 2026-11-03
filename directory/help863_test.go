package directory

import (
	"strings"
	"testing"
)

const admin = "+4917011111111"

func withAdmin(c Who) Who {
	c.Admins = []string{admin}
	return c
}

// #863: „Hilfe“, „Hallo“, „Was kannst du?“ or the assistant's name alone get
// plain German with five sentences the person can send as they are; the
// terminal's capitals stay behind Menü.
func TestHelpIsPlainGermanWithFiveExamples(t *testing.T) {
	for _, in := range []string{"Hilfe", "Hallo", "Hallo Homes", "Was kannst du?", "Homes", "hilfe!"} {
		j := New(func() []string { return []string{"laib", "frage", "ha", "automat", "haushalt"} }, withAdmin(homes()))
		out := j.Talk("+4917099999999", "de", in)
		text, ok := strings.CutPrefix(out, "TEXT:\n")
		if !ok {
			t.Fatalf("%q: help is text for the person, not a screen:\n%s", in, out)
		}
		if strings.Contains(text, "HOMES>") || text == strings.ToUpper(text) {
			t.Fatalf("%q: help is plain words, no terminal:\n%s", in, text)
		}
		if n := strings.Count(text, "\n- „"); n != 5 {
			t.Fatalf("%q: five example sentences, got %d:\n%s", in, n, text)
		}
		for _, want := range []string{"Hallo, hier ist Homes.", "Licht", "Erinnere mich", "Zugspitze"} {
			if !strings.Contains(text, want) {
				t.Errorf("%q: %q missing:\n%s", in, want, text)
			}
		}
	}
	if out := New(func() []string { return nil }, homes()).Talk("p", "en", "help"); !strings.HasPrefix(out, "TEXT:\nHello, this is Homes.") || strings.Count(out, "\n- “") != 5 {
		t.Fatalf("english help, five examples even without tools:\n%s", out)
	}
	if out := term().Talk("p", "de", "Menü"); !strings.HasPrefix(out, "VERZEICHNIS\n") || !strings.HasSuffix(out, "HOMES>") {
		t.Fatalf("Menü opens the directory:\n%s", out)
	}
}

// #863: box status and proposals are the admin's. The family's directory
// and help have no box level and no box example; the admin's number (and
// a profile name that resolves to it) has both.
func TestTheBoxIsOfferedOnlyToTheAdmin(t *testing.T) {
	caps := func() []string { return []string{"laib", "frage", "ha"} }
	c := withAdmin(homes())
	c.Resolve = func(label string) string {
		if label == "Martin" {
			return admin
		}
		return ""
	}
	j := New(caps, c)
	for _, sid := range []string{"+4917099999999", "Anna", "", "homes:lena"} {
		out := j.Talk(sid, "de", "Menü")
		if strings.Contains(out, "BOX") || !strings.Contains(out, "1  WEBSUCHE MIT QUELLEN") {
			t.Fatalf("%q: the family's directory has no box level:\n%s", sid, out)
		}
		if out := j.Talk(sid, "de", "Hilfe"); strings.Contains(out, "Box") {
			t.Fatalf("%q: the family's help offers nothing of the box:\n%s", sid, out)
		}
	}
	for _, sid := range []string{admin, "Martin"} {
		out := j.Talk(sid, "de", "Menü")
		if !strings.Contains(out, "1  BOX-STATUS UND VORSCHLÄGE") {
			t.Fatalf("%q: the admin's directory has the box level:\n%s", sid, out)
		}
		if out := j.Talk(sid, "de", "Hilfe"); !strings.Contains(out, "Box") {
			t.Fatalf("%q: the admin's help names the box:\n%s", sid, out)
		}
	}
	if out := New(caps, homes()).Talk(admin, "de", "Menü"); strings.Contains(out, "BOX") {
		t.Fatalf("without ADMIN_NUMBER nobody is the admin:\n%s", out)
	}
}
