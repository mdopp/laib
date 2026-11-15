package haushalt

import (
	"encoding/json"
	"strings"
	"testing"
)

// #495, #1234: the address is checked here and goes to the kernel's part on
// stdin, inside the Abo; neither the argv nor the answer carries it.
func TestACalendarAddressIsCheckedHereAndGoesOnStdin(t *testing.T) {
	const ics = "https://calendar.google.com/calendar/ical/x/private-0123/basic.ics"
	k := &kern{reply: `{"abos":[{"person":"lea","name":"familie","kind":"google","title":"Familie","since":"s"}],"kalender":false}`}
	out, code, errs := run1234(t, k, ics+"\n", "calendar", "subscribe", "lea", "familie", "google", "Familie", "--every", "30m")
	if code != exitOK || len(k.asked) != 1 || strings.Join(k.asked[0], " ") != "laib_calendar_kern subscribe lea familie" {
		t.Fatalf("exit %d %s: %v", code, errs, k.asked)
	}
	var sent Abo
	if json.Unmarshal([]byte(k.stdin[0]), &sent); sent.URL != ics || sent.Kind != "google" || sent.Every != "30m" || sent.Title != "Familie" {
		t.Fatalf("stdin %q", k.stdin[0])
	}
	if strings.Contains(out, "private-") || !strings.Contains(out, "verbunden") || !strings.Contains(out, "sobald das Template kalender installiert ist") {
		t.Fatalf("answer %s", out)
	}
	for _, bad := range []string{"https://example.org/x.ics", "", "http://calendar.google.com/a/basic.ics"} {
		k := &kern{reply: `{}`}
		if _, code, _ := run1234(t, k, bad, "calendar", "subscribe", "lea", "arbeit", "google"); code == exitOK || len(k.asked) != 0 {
			t.Errorf("%q: exit %d, asked %v", bad, code, k.asked)
		}
	}
	// webcal, as phones show it, is https; every names the title it answers with
	k = &kern{reply: `{"abos":[{"person":"homes","name":"feiertage","title":"Feiertage"}],"kalender":true}`}
	if _, code, _ := run1234(t, k, "webcal://example.org/f.ics", "calendar", "subscribe", "homes", "feiertage", "abo"); code != exitOK || !strings.Contains(k.stdin[0], `"url":"https://example.org/f.ics"`) {
		t.Fatalf("webcal: exit %d %q", code, k.stdin)
	}
	if out, code, _ := run1234(t, k, "", "calendar", "every", "homes", "feiertage", "--every", "12h"); code != exitOK || !strings.Contains(out, "„Feiertage“ wird ab jetzt alle 12h abgeglichen.") || strings.Join(k.asked[1], " ") != "laib_calendar_kern every homes feiertage 12h" {
		t.Fatalf("every: exit %d %s %v", code, out, k.asked)
	}
}
