package pages

import (
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/state"
)

// #439: the box page shows the network map, one card per name.
func TestTheNetPageShowsEveryNameAndPort(t *testing.T) {
	st, err := state.Load("../../testdata/golden/basic/state")
	if err != nil {
		t.Fatal(err)
	}
	b := netBody(render.Net(st))
	for _, want := range []string{"<div class=card data-f><b>whoami.example.home</b><span class=state>nur LAN</span>", "Authelia vorab: one_factor family, admins", "<b>auth.example.home</b>", "meldet sich selbst an", "<b>3890</b><span class=state>nur Box und Pods</span>"} {
		if !strings.Contains(b, want) {
			t.Fatalf("missing %q:\n%s", want, b)
		}
	}
}
