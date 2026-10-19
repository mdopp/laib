package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/diag"
	"github.com/mdopp/laib/internal/pages"
)

// #57 (1): laib-notify pings the outside watchdog (#443, notify/notify_test.go)
// and stamps watchdog.last; readiness shows the last report from it.
func TestReadinessShowsTheWatchdogsLastReport(t *testing.T) {
	c := newCbox(t)
	b, _ := os.ReadFile(filepath.Join(c.env.Paths.State, "box.yml"))
	os.WriteFile(filepath.Join(c.env.Paths.State, "box.yml"), append(b, []byte("watchdog:\n  url: https://hc.example/ping/abc\n")...), 0o644)
	st, err := cli.LoadState(c.env)
	if err != nil {
		t.Fatal(err)
	}
	if r, _ := pages.CollectStatus(context.Background(), c.env, st); r.Watchdog != "noch nie gemeldet" {
		t.Fatalf("before any ping: %q", r.Watchdog)
	}
	os.WriteFile(filepath.Join(c.env.Paths.Home, "watchdog.last"), []byte(time.Now().UTC().Format(time.RFC3339)), 0o644)
	if r, _ := pages.CollectStatus(context.Background(), c.env, st); !strings.Contains(r.Watchdog, "zuletzt gemeldet") {
		t.Fatalf("readiness must show the last report: %q", r.Watchdog)
	}
}

// #57 (1): no watchdog configured is a yellow diag finding.
func TestDiagIsYellowWithoutAWatchdog(t *testing.T) {
	c := newCbox(t)
	st, _ := cli.LoadState(c.env)
	var found bool
	for _, f := range diag.Probes(context.Background(), c.env, st, "") {
		if f.Probe == "watchdog" && f.Severity == "gelb" {
			found = true
		}
	}
	if !found {
		t.Fatal("no watchdog.url must be yellow: when the box dies, nobody is told")
	}
}

// #57 (2): the paper card for the family — one page, German, with the
// unplug-and-wait instruction and whom to call.
func TestFamilyCardIsOnePageInGerman(t *testing.T) {
	c := newCbox(t)
	b, _ := os.ReadFile(filepath.Join(c.env.Paths.State, "box.yml"))
	os.WriteFile(filepath.Join(c.env.Paths.State, "box.yml"), append(b, []byte("watchdog:\n  contact: Martin, 0170 1234567\n")...), 0o644)
	out := captureStdout(t, func() {
		if rc := pages.FamilyCard(context.Background(), c.env, nil); rc != cli.ExitOK {
			t.Errorf("rc %d", rc)
		}
	})
	for _, want := range []string{"<html", "Wenn die Box nicht geht", "10 Sekunden", "5 Minuten", "Martin, 0170 1234567", "Stick", "Alles läuft"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the family card lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "laib ") || strings.Contains(out, "systemctl") {
		t.Fatal("the family card is not for admins: no commands")
	}
}
