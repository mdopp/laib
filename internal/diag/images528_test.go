package diag

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/sysd"
)

// #528: laib diag is yellow when an image is more than 30 days behind its
// channel — counted from when laib updates first saw it behind, also when
// the channel moved again since — and when nobody keeps the images current:
// laib-images.timer not running, or podman's own timer running beside it.
func TestDiagIsYellowForAMonthBehindAndForNoUpdatePath(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	SeeBehind(home, now.Add(-40*24*time.Hour), map[string]string{"vaultwarden IMAGE": "sha256:v1", "frage IMAGE": "sha256:f1"})
	SeeBehind(home, now.Add(-2*24*time.Hour), map[string]string{"vaultwarden IMAGE": "sha256:v2", "media IMAGE": "sha256:m1"}) // frage caught up
	active := map[string]string{"laib-images.timer": "active"}
	real := sysd.Run
	t.Cleanup(func() { sysd.Run = real })
	sysd.Run = func(_ context.Context, _ string, args ...string) (string, error) {
		return active[args[len(args)-1]], nil
	}
	got := imageFindings(context.Background(), home, "", now)
	if len(got) != 1 || got[0].Service != "vaultwarden" || got[0].Severity != "gelb" || !strings.Contains(got[0].Detail, "seit 40 Tagen") || !strings.Contains(got[0].Detail, "sha256:v2") {
		t.Fatalf("vaultwarden 40 days behind (its channel moved since), media 2 days, frage current: %+v", got)
	}
	if got := imageFindings(context.Background(), home, "media", now); len(got) != 0 {
		t.Fatalf("diag for one service: only its own: %+v", got)
	}
	active = map[string]string{"podman-auto-update.timer": "active"}
	got = imageFindings(context.Background(), home, "", now)
	var says []string
	for _, f := range got[1:] {
		says = append(says, f.Detail)
	}
	if len(got) != 3 || !strings.Contains(says[0], "laib-images.timer läuft nicht") || !strings.Contains(says[1], "zweiter Update-Weg") {
		t.Fatalf("no laib-images.timer and podman's beside it: %v", says)
	}
}

// #1071: no read verb saw podman's storage; diag says what the images take,
// and how much of it no container uses, as upper bounds.
func TestDiagSaysWhatTheImagesTake(t *testing.T) {
	real := sysd.Run
	t.Cleanup(func() { sysd.Run = real })
	out := ""
	sysd.Run = func(_ context.Context, name string, args ...string) (string, error) { return out, nil }
	if f := storageFinding(context.Background()); f != nil {
		t.Fatalf("podman said nothing, diag invents: %+v", f)
	}
	out = `[{"Id":"a","Containers":1,"Size":2147483648},{"Id":"b","Containers":0,"Size":12884901888},{"Id":"c","Size":1073741824}]`
	f := storageFinding(context.Background())
	if len(f) != 1 || f[0].Severity != "info" || f[0].Detail != "3 Container-Abbilder belegen höchstens 15 GB, 2 davon ohne Container höchstens 13 GB (gemeinsame Schichten zählen mehrfach)" {
		t.Fatalf("%+v", f)
	}
}
