package pages

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/state"
)

// #515: the box pages work on the phone as Haus does (haus#67). One nav on
// every page — a bar at the bottom on the phone, a list on the right from
// 900 px — with the current page marked and Abmelden in it; a free-text
// filter on top of each list page that hides the cards (data-f) whose text
// lacks what is typed; cards whose note drops below name and state under
// 600 px.
func TestTheBoxPagesWorkOnThePhone(t *testing.T) {
	for _, tab := range tabs {
		got := pageHTML(tab.name, "")
		nav := regexp.MustCompile(`<nav class=tabs aria-label=Seiten>.*?</nav>`).FindString(got)
		for _, other := range tabs {
			if !strings.Contains(nav, `<a href="`+other.href+`"`) || !strings.Contains(nav, "</svg>"+other.name+"</a>") {
				t.Errorf("%s: the nav lacks %s: %s", tab.name, other.name, nav)
			}
		}
		if n := strings.Count(nav, "aria-current=page"); n != 1 || !strings.Contains(nav, `<a href="`+tab.href+`" aria-current=page>`) {
			t.Errorf("%s: the current page is not the one marked (%d marks): %s", tab.name, n, nav)
		}
	}
	for _, w := range []string{"position:fixed", "bottom:0", "@media (min-width:900px)", "flex-direction:column", "@media (max-width:600px){.card .note{flex-basis:100%}}", "[hidden]{display:none!important}"} {
		if !strings.Contains(css, w) {
			t.Errorf("laib.css lacks %q", w)
		}
	}

	// each list page carries the filter and marks its cards for it
	st, err := state.Load("../../testdata/golden/basic/state")
	if err != nil {
		t.Fatal(err)
	}
	status := statusBody(Readiness{State: "bereit"}, []ServiceStatus{{Name: "immich", State: "grün"}}, nil, "", [2]string{}, false)
	jour := "<h1>Journal</h1>" + filter + journalBody([]journal.Entry{{T: time.Now(), Actor: "laib", Verb: "apply", Result: "green"}})
	netz := filter + netBody(render.Net(st))
	for name, body := range map[string]string{"Status": status, "Journal": jour, "Netz": netz} {
		if !strings.Contains(body, `<input type=search class=filter`) {
			t.Errorf("%s has no filter: %s", name, body)
		}
		if !regexp.MustCompile(`<div class="?card[^>]* data-f>`).MatchString(body) {
			t.Errorf("%s's cards are not marked for the filter: %s", name, body)
		}
	}
	// the filter matches the card's whole text, case-insensitively, as typed
	for _, w := range []string{"oninput=", "toLowerCase()", "querySelectorAll('[data-f]')", "e.hidden=!e.textContent.toLowerCase().includes(q)"} {
		if !strings.Contains(filter, w) {
			t.Errorf("the filter lacks %q: %s", w, filter)
		}
	}
	// "lan" on /net finds the LAN names: their card's text holds it
	lan := 0
	for _, c := range regexp.MustCompile(`<div class=card data-f>.*?</div>`).FindAllString(netz, -1) {
		if strings.Contains(strings.ToLower(regexp.MustCompile(`<[^>]+>`).ReplaceAllString(c, "")), "lan") {
			lan++
		}
	}
	if lan == 0 {
		t.Errorf("no card on /net holds \"lan\": %s", netz)
	}
}
