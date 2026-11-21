package proposal

import "testing"

func TestWayOfNamesTheCardButNeverTheCardItself(t *testing.T) {
	card := &Proposal{ID: 5, Wahl: []Weg{{Label: "a", Vorschlag: 3}, {Label: "b", Vorschlag: 5}}}
	all := []*Proposal{card, {ID: 3}, {ID: 4}}
	if WayOf(all, all[1]) != card || WayOf(all, card) != nil || WayOf(all, all[2]) != nil {
		t.Fatal("a way is found by its card; the card, which is its own ticket way, and a stranger are not")
	}
}
