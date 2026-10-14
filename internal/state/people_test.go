package state

import (
	"strings"
	"testing"
)

// #177, ADR 0022 §3: people and their chat channels are declared once in
// box.yml, preferred channel first; a channel is a known platform and a
// target of that platform's shape.
func TestPeopleAreValidated(t *testing.T) {
	st, err := Load(box46(t, "people:\n  mama: [\"signal:+4917012345678\", \"telegram:123456\"]\n  lena: [\"matrix:@lena:example.org\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Box.People["mama"]; len(got) != 2 || got[0] != "signal:+4917012345678" {
		t.Fatalf("mama's channels, in order: %v", got)
	}
	for _, c := range []struct{ yml, want string }{
		{"people: {mama: [\"signal:0170123\"]}\n", "E.164"},
		{"people: {mama: [\"sms:+49 170\"]}\n", "E.164"},
		{"people: {mama: [\"brieftaube:oma\"]}\n", "platform"},
		{"people: {mama: [\"telegram:\"]}\n", "target"},
		{"people: {mama: [\"telegram:@mama\"]}\n", "chat id"},
		{"people: {mama: [\"matrix:mama\"]}\n", "matrix"},
		{"people: {mama: [\"matrix:!raum:example.org:$event\"]}\n", "matrix"}, // a thread, which Hermes reads as <room>:$<event>
		{"people: {mama: [\"email:mama@localhost\"]}\n", "address"},           // Hermes wants a TLD
		{"people: {mama: []}\n", "no channel"},
		{"people: {Mama Müller: [\"telegram:1\"]}\n", "name"},
	} {
		_, err := Load(box46(t, c.yml))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: want an error naming %q, got %v", c.yml, c.want, err)
		}
	}
}

// people.json is what bote-notify reads: name → channels in order. Its rev
// changes with any channel, so the unit that carries it restarts.
func TestPeopleJSONAndRev(t *testing.T) {
	a, err := Load(box46(t, "people: {mama: [\"signal:+4917012345678\", \"telegram:1\"], papa: [\"telegram:2\"]}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if j := a.PeopleJSON(); j != `{"mama":["signal:+4917012345678","telegram:1"],"papa":["telegram:2"]}`+"\n" {
		t.Fatalf("people.json: %s", j)
	}
	b, _ := Load(box46(t, "people: {mama: [\"telegram:1\", \"signal:+4917012345678\"], papa: [\"telegram:2\"]}\n"))
	if a.PeopleRev() == b.PeopleRev() || len(a.PeopleRev()) != 12 {
		t.Fatalf("a changed order must change the rev: %s %s", a.PeopleRev(), b.PeopleRev())
	}
	none, _ := Load(box46(t, ""))
	if none.PeopleJSON() != "{}\n" {
		t.Fatalf("no people: %q", none.PeopleJSON())
	}
}
