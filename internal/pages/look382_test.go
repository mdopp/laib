package pages

import (
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/journal"
)

// #382: Laib's box, inbox and journal pages in the household's look
// (mdopp/haus#35): the Solaris palette, light or dark by the device, the
// system font, no external request, one card per service with its state
// and note on one line, readable on a phone. The status page is a golden
// file; UPDATE_GOLDEN=1 rewrites it.
func TestThePagesWearTheHouseholdLook(t *testing.T) {
	rd := Readiness{State: "bereit", Services: 3, Green: 1, Yellow: 1, Red: 1, Operator: "läuft", LastHeard: "2026-10-19T07:41:05Z"}
	rows := []ServiceStatus{
		{Name: "auth", State: "grün", Units: map[string]string{"auth-pod.service": "active"}, Detail: "Passwort-Mails über smtp.example · geprüft vor 3 min"},
		{Name: "home-assistant", State: "gelb", Units: map[string]string{"home-assistant.service": "active"}, Detail: "fehlt: Jellyfin, Waschmaschine"},
		{Name: "llama", State: "rot", Units: map[string]string{"llama-lease.service": "activating"}},
	}
	body := statusBody(rd, rows, map[string]bool{"home-assistant": false}, "19.10. 07:30 · human:core · apply auth · Session-Speicher", [2]string{"19.10.", "Alles ruhig."}, true)
	got := pageHTML("Status", body)
	golden := "testdata/status.html"
	if os.Getenv("UPDATE_GOLDEN") != "" {
		os.WriteFile(golden, []byte(got), 0o644)
	}
	want, err := os.ReadFile(golden)
	if err != nil || string(want) != got {
		t.Errorf("the status page differs from %s (UPDATE_GOLDEN=1 rewrites it):\n%s", golden, got)
	}
	for _, w := range []string{"#070709", "#14141a", "#f97316", "prefers-color-scheme: light", "system-ui", `name=viewport`} {
		if !strings.Contains(got, w) {
			t.Errorf("the page lacks %s", w)
		}
	}
	if regexp.MustCompile(`(src|href)="?https?://|@import|url\(`).MatchString(got) {
		t.Error("the page makes an external request (a font, a stylesheet)")
	}
	cards := regexp.MustCompile(`(?s)<div class="card (grün|gelb|rot)" data-f>.*?</div>`).FindAllString(got, -1)
	if len(cards) != 3 {
		t.Fatalf("want one card per service, got %d:\n%s", len(cards), got)
	}
	for i, c := range cards {
		for _, w := range []string{rows[i].Name, rows[i].State} {
			if !strings.Contains(c, w) {
				t.Errorf("card %d lacks %q: %s", i, w, c)
			}
		}
	}
	if !strings.Contains(cards[1], "fehlt: Jellyfin, Waschmaschine") || !strings.Contains(cards[2], "llama-lease.service=activating") {
		t.Errorf("a card lost its note: %q", cards)
	}
	if strings.Contains(got, "<table") {
		t.Error("the status page is still a table")
	}
	// the journal: one row card per entry, the result as word and colour
	j := journalBody([]journal.Entry{{T: time.Date(2026, 10, 19, 7, 30, 0, 0, time.UTC), Actor: "human:core", Verb: "apply", Service: "auth", Result: "red", Reason: "Session-Speicher"}})
	if !strings.Contains(j, `class="card rot" data-f`) || strings.Contains(j, "<table") || !strings.Contains(j, "Session-Speicher") {
		t.Errorf("the journal is not rows of cards: %s", j)
	}
}
