package journal

import (
	"testing"
	"time"
)

// #593: the newest n entries a filter keeps, oldest first, read from the
// newest day back; since still bounds them.
func TestNewestReadsBackAndStopsAtN(t *testing.T) {
	dir := t.TempDir()
	day := time.Date(2026, 10, 20, 7, 0, 0, 0, time.UTC)
	for i := 0; i < 4; i++ { // four days, a report and a look each
		Append(dir, Entry{T: day.Add(time.Duration(i) * 24 * time.Hour), Actor: "ops", Verb: "note", Reason: "report", Result: "info", Detail: day.Add(time.Duration(i) * 24 * time.Hour).Format("02.01.")})
		Append(dir, Entry{T: day.Add(time.Duration(i)*24*time.Hour + time.Hour), Actor: "claude", Verb: "look", Result: "info"})
	}
	report := func(e Entry) bool { return e.Verb == "note" && e.Reason == "report" }
	es, err := Newest(dir, time.Time{}, 1, report)
	if err != nil || len(es) != 1 || es[0].Detail != "23.10." {
		t.Fatalf("newest report: %v %+v", err, es)
	}
	es, _ = Newest(dir, time.Time{}, 3, nil)
	if len(es) != 3 || es[0].Verb != "look" || es[1].Detail != "23.10." || es[2].Verb != "look" || !es[0].T.Before(es[2].T) {
		t.Fatalf("newest three, oldest first: %+v", es)
	}
	es, _ = Newest(dir, day.Add(48*time.Hour), 10, report)
	if len(es) != 2 {
		t.Fatalf("since bounds the reports: %+v", es)
	}
}
