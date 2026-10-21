package registry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/diag"
)

// #547: laib updates had no row for Laib itself. The row comes from what
// self-update.sh found at its last run; it carries the newest release as
// latest, never as patch or digest — those are what the Operator's
// aktualisierung proposes, and Laib's own update is no proposal.
func TestTheLaibRowSaysWhatRunsAndWhatIsOut(t *testing.T) {
	home := t.TempDir()
	if _, ok := laibRow(home, "v0.11.1"); ok {
		t.Fatal("no self-update.json yet: no row to guess")
	}
	for _, c := range []struct {
		rec  diag.SelfUpdate
		want string
	}{
		{diag.SelfUpdate{Running: "v0.16.0", Latest: "v0.16.0", Result: "current"}, "aktuell"},
		{diag.SelfUpdate{Running: "v0.11.1", Latest: "v0.16.0", Result: "skipped", Detail: "die Sicherung läuft"}, "neu: v0.16.0 (nachts automatisch)"},
		{diag.SelfUpdate{Running: "v0.11.1", Latest: "v0.16.0", Result: "off"}, "neu: v0.16.0 (aus: box.yml updates.laib)"},
		{diag.SelfUpdate{Running: "v0.11.1", Latest: "v0.16.0", Result: "failed", Detail: "nach dem Update nicht grün"}, "v0.16.0 zurückgenommen: nach dem Update nicht grün"},
	} {
		c.rec.At = time.Date(2026, 10, 21, 4, 30, 0, 0, time.Local)
		b, _ := json.Marshal(c.rec)
		os.WriteFile(filepath.Join(home, "self-update.json"), b, 0o644)
		u, ok := laibRow(home, c.rec.Running)
		if !ok || u.Service != "laib" || u.Patch != "" || u.Digest != "" || u.Minor != "" {
			t.Fatalf("%+v: the laib row must never look proposable: %+v", c.rec, u)
		}
		if line := laibLine(u); !strings.Contains(line, c.want) || !strings.Contains(line, c.rec.Running) || !strings.Contains(line, "21.10. 04:30") {
			t.Errorf("%+v: want %q, the running version and when it looked:\n%s", c.rec, c.want, line)
		}
	}
}
