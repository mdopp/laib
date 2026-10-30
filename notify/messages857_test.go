package notify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// #857: a message to the owner says what is red by the name the family
// uses, since when, who is on it, and links to Status; a red that was told
// ends in one „wieder grün“ line; a proposal links to its own card; the
// watchdog's ping means only „Laib lebt“; the weekly list comes only when
// it changed.

func sweepAt(t *testing.T, c Config, at time.Time) {
	t.Helper()
	if err := Sweep(context.Background(), c, at); err != nil {
		t.Fatal(err)
	}
}

func lastWith(mails []string, s string) string {
	out := ""
	for _, m := range mails {
		if strings.Contains(m, s) {
			out = m
		}
	}
	return out
}

func TestTheRedMessageSaysSinceWhenWhoIsOnItAndLinksToStatus(t *testing.T) {
	for _, tc := range []struct{ name, ready, aufgabe, want string }{
		{"the Operator works on it", `{"state":"bereit","operator":"läuft","operatorWork":{"incident":"health paperless: dial tcp","since":"2026-10-30T09:00:00Z"}}`, "", "Der Operator arbeitet an einem Vorfall"},
		{"the Operator looks", `{"state":"bereit","operator":"läuft"}`, "", "Der Operator sieht nach"},
		{"nobody", `{"state":"bereit","operator":"aus"}`, "", "Niemand"},
		{"a Box-Aufgabe", `{"state":"bereit","operator":"aus"}`, `,"aufgabe":7`, "Box-Aufgabe #7"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeLaib(t)
			addr, mails := fakeSMTP(t)
			c := f.config(t, addr)
			c.InboxURL = "https://laib.e2e.home/inbox"
			f.ready = tc.ready
			f.diag = `[{"severity":"rot","probe":"health","service":"paperless","detail":"dial tcp","say":"Dokumente antwortet nicht."` + tc.aufgabe + `}]`
			t0 := time.Date(2026, 10, 30, 10, 31, 0, 0, time.Local)
			sweepAt(t, c, t0)
			sweepAt(t, c, t0.Add(11*time.Minute))
			top, details, _ := strings.Cut(lastWith(mails(), "seit 10 Minuten rot"), "Details für den")
			for _, w := range []string{"Dokumente antwortet nicht.", "seit 10:31", tc.want, "https://laib.e2e.home/\r\n"} {
				if !strings.Contains(top, w) {
					t.Errorf("the red message lacks %q:\n%s", w, top)
				}
			}
			if m := technical.FindString(top); m != "" || strings.Contains(top, "dial tcp") {
				t.Errorf("the top shows %q:\n%s", m, top)
			}
			if tc.name == "the Operator works on it" && !strings.Contains(details, "health paperless: dial tcp") {
				t.Errorf("the incident's own words belong under Details:\n%s", details)
			}
		})
	}
}

func TestARedThatWasToldEndsInWiederGruen(t *testing.T) {
	f := newFakeLaib(t)
	addr, mails := fakeSMTP(t)
	c := f.config(t, addr)
	red := `[{"severity":"rot","probe":"health","service":"paperless","detail":"x","say":"Dokumente antwortet nicht."}]`
	t0 := time.Now()
	f.diag = red
	sweepAt(t, c, t0)
	sweepAt(t, c, t0.Add(4*time.Minute))
	f.diag = "[]"
	sweepAt(t, c, t0.Add(6*time.Minute))
	if m := lastWith(mails(), "wieder grün"); m != "" {
		t.Fatalf("a red nobody was told about needs no all-clear: %q", m)
	}
	f.diag = red
	sweepAt(t, c, t0.Add(7*time.Minute))
	sweepAt(t, c, t0.Add(18*time.Minute))
	f.diag = "[]"
	sweepAt(t, c, t0.Add(30*time.Minute))
	m := lastWith(mails(), "wieder grün")
	if !strings.Contains(m, "Dokumente antwortet nicht.") || !strings.Contains(m, "wieder grün nach 23 Minuten") {
		t.Fatalf("a red that was told ends in one line with its minutes: %q", mails())
	}
	sweepAt(t, c, t0.Add(35*time.Minute))
	if n := strings.Count(strings.Join(mails(), "\n"), "wieder grün nach"); n != 1 {
		t.Errorf("the all-clear comes once: %d", n)
	}
}

func TestAProposalLinksToItsOwnCard(t *testing.T) {
	f := newFakeLaib(t)
	b := newFakeBote(t)
	c := chatConfig(t, f, b, "127.0.0.1:1")
	f.inbox = `[{"id":17,"reason":"x","card":{"summary":"whoami bekommt ein neues Image"}}]`
	sweepAt(t, c, time.Now())
	if got := b.messages(); len(got) != 1 || !strings.HasSuffix(got[0]["text"], "https://laib.e2e.home/inbox#17") {
		t.Fatalf("the message links to /inbox#17: %v", got)
	}
}

func TestThePingMeansOnlyThatLaibLives(t *testing.T) {
	f := newFakeLaib(t)
	c := f.config(t, "127.0.0.1:1")
	var pings int
	wd := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { pings++ }))
	defer wd.Close()
	c.WatchdogURL = wd.URL + "/ping/abc"
	f.ready = `{"state":"eingeschränkt","red":3}`
	if !Ping(context.Background(), c) || pings != 1 {
		t.Fatalf("a red service is the red message's, not the watchdog's: laib lives, so it pings (%d)", pings)
	}
	f.ready = "503"
	if Ping(context.Background(), c) || pings != 1 {
		t.Fatal("laib that cannot read its state does not count as alive")
	}
	f.srv.Close()
	if Ping(context.Background(), c) || pings != 1 {
		t.Fatal("no laib, no ping: silence is the alarm")
	}
}

func TestTheWeeklyListComesOnlyWhenItChanged(t *testing.T) {
	f := newFakeLaib(t)
	addr, mails := fakeSMTP(t)
	c := f.config(t, addr)
	f.updates = `[{"service":"whoami","image":"docker.io/traefik/whoami","current":"v1.11","minor":"v1.12"}]`
	t0 := time.Now()
	week := 7 * 24 * time.Hour
	sweepAt(t, c, t0)
	sweepAt(t, c, t0.Add(time.Hour)) // a sweep that does not try the list forgets its send
	sweepAt(t, c, t0.Add(week))
	if n := strings.Count(strings.Join(mails(), "\n"), "Was ist veraltet"); n == 0 || n > 2 { // subject and body of one mail
		t.Fatalf("the same list twice: %d mentions in %q", n, mails())
	}
	f.updates = `[{"service":"whoami","image":"docker.io/traefik/whoami","current":"v1.11","minor":"v1.13"}]`
	sweepAt(t, c, t0.Add(week+time.Hour))
	sweepAt(t, c, t0.Add(2*week))
	if m := lastWith(mails(), "v1.13"); m == "" {
		t.Fatalf("a changed list is sent: %q", mails())
	}
}
