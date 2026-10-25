package aufgaben

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/proposal"
)

func repo(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "state")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "box.yml"), []byte("domain: box.test\n"), 0o644)
	if err := proposal.EnsureRepo(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s", args, out)
	}
	return strings.TrimSpace(string(out))
}

// #667: the list lives in the state repo as aufgaben.yml, committed on main
// by itself — backed up and versioned with the box, no proposal, and a
// human's uncommitted edit elsewhere stays theirs.
func TestTheListRoundTripsThroughTheStateRepo(t *testing.T) {
	dir := repo(t)
	ctx := context.Background()
	if list, err := Load(dir); err != nil || len(list) != 0 {
		t.Fatalf("a box without aufgaben.yml has an empty list: %v %v", list, err)
	}
	os.WriteFile(filepath.Join(dir, "box.yml"), []byte("domain: box.test\n# a human's edit\n"), 0o644)
	now := time.Date(2026, 10, 25, 9, 0, 0, 0, time.Local)
	var got Aufgabe
	err := Update(ctx, dir, "claude", func(list []Aufgabe) ([]Aufgabe, string, error) {
		list, got = Add(list, Aufgabe{Titel: "Wächter von außen", Warum: "fällt die Box aus, erfährt es niemand", Erinnern: "2026-12-01", Befund: "watchdog"}, "claude", now)
		return list, "aufgabe add #1", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != 1 || got.Seit != "2026-10-25" || got.Von != "claude" || !got.Open() {
		t.Fatalf("added: %+v", got)
	}
	list, err := Load(dir)
	if err != nil || len(list) != 1 || list[0] != got {
		t.Fatalf("loaded %+v, %v; want %+v", list, err, got)
	}
	if s := git(t, dir, "log", "-1", "--format=%s %an", "--", File); s != "aufgabe add #1 claude" {
		t.Fatalf("the commit on main: %q", s)
	}
	if s := git(t, dir, "status", "--porcelain"); s != "M box.yml" {
		t.Fatalf("only aufgaben.yml is committed, the human's box.yml edit stays uncommitted: %q", s)
	}
	b, _ := os.ReadFile(filepath.Join(dir, File))
	for _, want := range []string{"titel: Wächter von außen", "erinnern: \"2026-12-01\"", "befund: watchdog", "seit: \"2026-10-25\""} {
		if !strings.Contains(string(b), want) {
			t.Errorf("aufgaben.yml lacks %q:\n%s", want, b)
		}
	}
}

// add numbers on, erledigt records when and by whom, zurueckstellen moves the
// reminder; ids are never reused, a done task stays in the file.
func TestAddDoneDefer(t *testing.T) {
	now := time.Date(2026, 10, 25, 9, 0, 0, 0, time.Local)
	var list []Aufgabe
	list, a := Add(list, Aufgabe{Titel: "TOTP für mdopp prüfen"}, "human:core", now)
	list, b := Add(list, Aufgabe{Titel: "NUC löschen und vermerken"}, "ops", now)
	if a.ID != 1 || b.ID != 2 {
		t.Fatalf("ids %d %d", a.ID, b.ID)
	}
	if err := Done(list, 1, "human:core", "", now); err != nil {
		t.Fatal(err)
	}
	if e := list[0].Erledigt; e == nil || e.Am != "2026-10-25" || e.Von != "human:core" || list[0].Open() {
		t.Fatalf("done: %+v", list[0])
	}
	if err := Done(list, 1, "claude", "", now); err == nil {
		t.Fatal("a done task was checked off twice")
	}
	if err := Done(list, 9, "claude", "", now); err == nil {
		t.Fatal("an unknown id was checked off")
	}
	if err := Defer(list, 2, "2026-11-30"); err != nil || list[1].Erinnern != "2026-11-30" {
		t.Fatalf("defer: %v %+v", err, list[1])
	}
	for _, bad := range []string{"30.11.2026", "2026-13-01", "morgen", ""} {
		if err := Defer(list, 2, bad); err == nil {
			t.Errorf("deferred to %q", bad)
		}
	}
	if err := Defer(list, 1, "2026-11-30"); err == nil {
		t.Fatal("a done task was deferred")
	}
	list, c := Add(list, Aufgabe{Titel: "Voice PE flashen"}, "claude", now)
	if c.ID != 3 {
		t.Fatalf("the next id after a done one: %d", c.ID)
	}
	if !list[1].Due("2026-11-30") || list[1].Due("2026-11-29") || list[2].Due("2030-01-01") {
		t.Fatal("due: open with erinnern on or before today; without erinnern never")
	}
}

// A befund names a diag finding: its probe, and its service or topic. A
// befund without one covers only a finding without one, so "backup" never
// hides a failed nightly behind the deferred off-box copy.
func TestMatch(t *testing.T) {
	for _, c := range []struct {
		befund, probe, service, topic string
		want                          bool
	}{
		{"watchdog", "watchdog", "", "", true},
		{"raid", "raid", "", "", true},
		{"backup offsite", "backup", "", "offsite", true},
		{"backup offsite", "backup", "", "", false},
		{"backup", "backup", "", "offsite", false},
		{"unit immich", "unit", "immich", "", true},
		{"unit immich", "unit", "paperless", "", false},
		{"unit", "unit", "immich", "", false},
		{"", "watchdog", "", "", false},
	} {
		if got := Match(c.befund, c.probe, c.service, c.topic); got != c.want {
			t.Errorf("Match(%q, %q %q %q) = %v", c.befund, c.probe, c.service, c.topic, got)
		}
	}
}
