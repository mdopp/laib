package proposal

import (
	"testing"
	"time"
)

// #544: laib, not the proposer, records what came of an approved proposal:
// its finding gone is effective; still there after Window, no effect; one
// that names no finding is applied, nothing to measure. Each is settled
// once, and only the measured ones are returned for the journal.
func TestSettleMeasuresTheFindingOnce(t *testing.T) {
	inbox := t.TempDir()
	at := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	for _, p := range []*Proposal{
		{ID: 1, Status: "approved", Decided: at, Anchor: Anchor{Finding: "unit jellyfin"}},
		{ID: 2, Status: "approved", Decided: at, Anchor: Anchor{Finding: "health immich"}},
		{ID: 3, Status: "approved", Decided: at},
		{ID: 4, Status: "open", Anchor: Anchor{Finding: "unit jellyfin"}},
		{ID: 5, Status: "approved", Decided: at, Outcome: "rolled-back", Anchor: Anchor{Finding: "unit jellyfin"}},
	} {
		if err := Save(inbox, p); err != nil {
			t.Fatal(err)
		}
	}
	present := func(f string) bool { return f == "health immich" }
	got := Settle(inbox, present, at.Add(30*time.Minute))
	if len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("settled %+v, want #1 only", got)
	}
	want := map[int]string{1: "effective", 2: "", 3: "applied", 4: "", 5: "rolled-back"}
	for id, o := range want {
		if p, _ := Get(inbox, id); p.Outcome != o {
			t.Fatalf("#%d outcome %q, want %q", id, p.Outcome, o)
		}
	}
	// within the window a finding still there is not yet a verdict; after it, no effect
	if got := Settle(inbox, present, at.Add(Window+time.Minute)); len(got) != 1 || got[0].ID != 2 || got[0].Outcome != "no-effect" {
		t.Fatalf("after the window: %+v", got)
	}
	if got := Settle(inbox, func(string) bool { return false }, at.Add(3*Window)); len(got) != 0 {
		t.Fatalf("settled twice: %+v", got)
	}
}

func TestLaterIsOnlyNotNow(t *testing.T) {
	for r, want := range map[string]bool{"nicht jetzt": true, "Später!": true, " Nicht jetzt. ": true, "": false, "nicht so": false, "falscher Dienst": false} {
		if Later(r) != want {
			t.Fatalf("Later(%q) = %v", r, !want)
		}
	}
}
