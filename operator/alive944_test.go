package operator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// #944: after a restart /ready seeds each skill's last run from the record,
// and diag reads the newest as the Operator's last finished tick. A calm
// night is all „unchanged“; left out, the restart looked like a hung tick
// for its first ten minutes. The seed reaches back one hour across midnight.
func TestARestartSeedsTheLastUnchangedRun(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 10, 31, 0, 20, 0, 0, time.Local)
	var lines []byte
	for _, s := range []Session{
		{Started: now.Add(-50 * time.Minute), Skill: "routineblick", Outcome: "green"},
		{Started: now.Add(-30 * time.Minute), Skill: "routineblick", Outcome: "unchanged"},
		{Started: now.Add(-5 * time.Minute), Skill: "routineblick", Outcome: "unchanged"},
	} {
		b, _ := json.Marshal(s)
		lines = append(append(lines, b...), '\n')
	}
	os.WriteFile(filepath.Join(home, "incidents.jsonl"), lines, 0o644)
	op := &Operator{Home: home}
	op.Recount(now)
	if r := op.Work().Skills["routineblick"]; !r.At.Equal(now.Add(-5*time.Minute)) || r.Outcome != "unchanged" {
		t.Fatalf("the seeded last run is %+v, want the unchanged one five minutes ago", r)
	}
	if got := op.sessions("routineblick", time.Time{}); len(got) != 1 {
		t.Fatalf("lessons and the report still leave unchanged runs out: %d sessions", len(got))
	}
}
