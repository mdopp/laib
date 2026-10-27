package pages

import (
	"regexp"
	"strings"
	"testing"
)

// #785: „Wo ist Jellyfin?“ — a card shows the service's plain name big,
// its one line of what it is, and the id small; without a title the id
// stays the card's name.
func TestACardShowsThePlainNameBigAndTheIdSmall(t *testing.T) {
	rows := []ServiceStatus{
		{Name: "media", Title: "Jellyfin", Beschreibung: "Filme, Serien, Musik", State: "grün", Units: map[string]string{}},
		{Name: "echo", State: "grün", Units: map[string]string{}},
	}
	body := statusBody(Readiness{State: "bereit"}, rows, nil, "", "")
	cards := regexp.MustCompile(`(?s)<div class="card grün" id="svc-[^"]+" data-f>.*?</div>`).FindAllString(body, -1)
	if len(cards) != 2 {
		t.Fatalf("want two cards: %s", body)
	}
	if !strings.Contains(cards[0], "<b>Jellyfin</b>") || !strings.Contains(cards[0], "Filme, Serien, Musik") || !strings.Contains(cards[0], "<small class=muted>media</small>") {
		t.Errorf("the card does not say Jellyfin big and media small: %s", cards[0])
	}
	if !strings.Contains(cards[1], "<b>echo</b>") || strings.Contains(cards[1], "<small") {
		t.Errorf("a service without a title is named by its id, once: %s", cards[1])
	}
}

// #785, the owner: „bitte oben einen Filter nach Status dazu“. Above the
// cards: Alle · Rot · Gelb · Grün with their counts, links the server
// answers (no script); problems first, a paused service counts green.
func TestTheStatusPageFiltersByStateAndShowsProblemsFirst(t *testing.T) {
	rows := []ServiceStatus{
		{Name: "a", State: "grün"}, {Name: "b", State: "gelb"}, {Name: "c", State: "pausiert"},
		{Name: "d", State: "rot"}, {Name: "e", State: "gelb"},
	}
	names := func(body string) string {
		var out []string
		for _, m := range regexp.MustCompile(`data-f><b>(\w+)</b>`).FindAllStringSubmatch(body, -1) {
			out = append(out, m[1])
		}
		return strings.Join(out, " ")
	}
	all := statusBody(Readiness{State: "bereit"}, rows, nil, "", "")
	for _, w := range []string{`<a href="/" aria-current=page>Alle 5</a>`, `<a href="/?filter=rot">Rot 1</a>`, `<a href="/?filter=gelb">Gelb 2</a>`, `<a href="/?filter=gruen">Grün 2</a>`} {
		if !strings.Contains(all, w) {
			t.Errorf("the filter lacks %s:\n%s", w, all)
		}
	}
	if strings.Index(all, "Alle 5") > strings.Index(all, "<div class=liste>") {
		t.Error("the filter is not above the cards")
	}
	if got := names(all); got != "d b e a c" {
		t.Errorf("order %q, want rot, gelb, then grün", got)
	}
	for show, want := range map[string]string{"rot": "d", "gelb": "b e", "gruen": "a c", "quatsch": "d b e a c"} {
		body := statusBody(Readiness{State: "bereit"}, rows, nil, "", show)
		if got := names(body); got != want {
			t.Errorf("?filter=%s shows %q, want %q", show, got, want)
		}
	}
	if gelb := statusBody(Readiness{}, rows, nil, "", "gelb"); !strings.Contains(gelb, `<a href="/?filter=gelb" aria-current=page>Gelb 2</a>`) {
		t.Errorf("the chosen filter is not marked:\n%s", gelb)
	}
}
