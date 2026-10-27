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
	// in notify's own folder, the one its container mounts (ADR 0027): a stamp beside it is not read
	os.WriteFile(filepath.Join(c.env.Paths.Home, "watchdog.last"), []byte(time.Now().UTC().Format(time.RFC3339)), 0o644)
	if r, _ := pages.CollectStatus(context.Background(), c.env, st); r.Watchdog != "noch nie gemeldet" {
		t.Fatalf("a stamp outside notify/ is read: %q", r.Watchdog)
	}
	os.MkdirAll(filepath.Join(c.env.Paths.Home, "notify"), 0o755)
	os.WriteFile(filepath.Join(c.env.Paths.Home, "notify", "watchdog.last"), []byte(time.Now().UTC().Format(time.RFC3339)), 0o644)
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
