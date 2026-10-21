package diag

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// #547: the Kraftwerk ran v0.11.1 while v0.16.0 was out, and diag said
// nothing. self-update.sh leaves Home/self-update.json after each run; diag
// is yellow when Laib does not keep itself current: no self-update.sh on the
// box, a release rolled back, off while a newer one is out, or no look for
// more than eight days. A current, an updated or a held box says nothing.
func TestDiagIsYellowWhenLaibDoesNotKeepItselfCurrent(t *testing.T) {
	now := time.Now()
	box := func(script bool, rec *SelfUpdate) (home, bin string) {
		home, bin = t.TempDir(), t.TempDir()
		if script {
			os.WriteFile(filepath.Join(bin, "self-update.sh"), []byte("#!/bin/bash\n"), 0o755)
		}
		if rec != nil {
			b, _ := json.Marshal(rec)
			os.WriteFile(filepath.Join(home, "self-update.json"), b, 0o644)
		}
		return home, bin
	}
	for name, c := range map[string]struct {
		script bool
		rec    *SelfUpdate
		want   string
	}{
		"no script":   {false, nil, "self-update.sh fehlt"},
		"first night": {true, nil, ""},
		"current":     {true, &SelfUpdate{At: now, Running: "v0.16.0", Latest: "v0.16.0", Result: "current"}, ""},
		"held":        {true, &SelfUpdate{At: now, Running: "v0.12.0", Latest: "v0.12.0", Result: "current"}, ""},
		"skipped":     {true, &SelfUpdate{At: now, Running: "v0.11.1", Latest: "v0.16.0", Result: "skipped", Detail: "die Sicherung läuft"}, ""},
		"rolled back": {true, &SelfUpdate{At: now, Running: "v0.11.1", Latest: "v0.16.0", Result: "failed", Detail: "nach dem Update nicht grün"}, "v0.16.0 zurückgenommen"},
		"off behind":  {true, &SelfUpdate{At: now, Running: "v0.11.1", Latest: "v0.16.0", Result: "off"}, "updates.laib: off"},
		"off current": {true, &SelfUpdate{At: now, Running: "v0.16.0", Latest: "v0.16.0", Result: "off"}, ""},
		"stale":       {true, &SelfUpdate{At: now.Add(-9 * 24 * time.Hour), Running: "v0.16.0", Latest: "v0.16.0", Result: "current"}, "seit 9 Tagen"},
	} {
		home, bin := box(c.script, c.rec)
		got := selfUpdateFindings(home, bin, now)
		if c.want == "" {
			if len(got) != 0 {
				t.Errorf("%s: no finding wanted: %+v", name, got)
			}
			continue
		}
		if len(got) != 1 || got[0].Severity != "gelb" || got[0].Probe != "self-update" || !strings.Contains(got[0].Detail, c.want) || got[0].Fix == "" {
			t.Errorf("%s: want one yellow self-update finding with %q: %+v", name, c.want, got)
		}
	}
}
