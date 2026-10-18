package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/diag"
	"github.com/mdopp/laib/internal/pages"
	"github.com/mdopp/laib/internal/sysd"
)

// #195: a template's timer and the oneshot it fires (haus's library
// refresh). The oneshot is inactive between runs: apply neither starts nor
// waits for it, converge does not restart it, and status is green — until a
// run fails, which status and diag show red. The timer itself is a unit.
func TestATemplateTimersOneshotCountsOnlyWhenItFails(t *testing.T) {
	ctx := context.Background()
	c := newCbox(t)
	dir := filepath.Join(c.env.Paths.State, "services", "tick")
	os.MkdirAll(dir, 0o755)
	for f, body := range map[string]string{
		"laib.yml":             "name: tick\ntemplate: {registry: vm, name: tick, version: 1}\nroutes: []\nsecrets: []\ndata: []\nhealth: {none: a test unit}\nports: []\n",
		"values.yml":           "IMAGE: docker.io/library/alpine:3\n",
		"tick.container":       "[Container]\nImage=${IMAGE}\n\n[Install]\nWantedBy=default.target\n",
		"tick-refresh.service": "[Service]\nType=oneshot\nExecStart=/usr/bin/podman exec systemd-tick true\n",
		"tick-refresh.timer":   "[Timer]\nOnCalendar=*:0/15\n\n[Install]\nWantedBy=timers.target\n",
	} {
		os.WriteFile(filepath.Join(dir, f), []byte(body), 0o644)
	}
	refresh := "inactive"
	prev := sysd.Run
	sysd.Run = func(ctx context.Context, name string, args ...string) (string, error) {
		if slices.Contains(args, "is-active") && args[len(args)-1] == "tick-refresh.service" {
			return refresh, nil
		}
		return prev(ctx, name, args...)
	}
	st, err := cli.LoadState(c.env)
	if err != nil {
		t.Fatal(err)
	}
	if rc := apply.ApplyLocked(ctx, c.env, st, "", "test: a service with a timer"); rc != cli.ExitOK {
		t.Fatalf("apply waited for the timer's oneshot to become active: rc %d", rc)
	}
	if !slices.Contains(c.calls, "systemctl --user enable --now tick-refresh.timer") {
		t.Errorf("the timer was not enabled: %v", c.calls)
	}
	if !slices.Contains(c.calls, "systemctl --user is-active tick-refresh.timer") {
		t.Errorf("the timer was not verified: %v", c.calls)
	}
	for _, l := range c.calls {
		if strings.Contains(l, "tick-refresh.service") {
			t.Errorf("apply touched the oneshot; only its timer starts it: %s", l)
		}
	}
	c.calls = nil
	apply.Converge(ctx, c.env, []string{"--force"})
	for _, u := range c.restarts() {
		if strings.HasPrefix(u, "tick") {
			t.Errorf("converge restarted %s for an idle oneshot", u)
		}
	}
	tick := func() pages.ServiceStatus {
		_, rows := pages.CollectStatus(ctx, c.env, st)
		for _, row := range rows {
			if row.Name == "tick" {
				return row
			}
		}
		t.Fatal("no status row for tick")
		return pages.ServiceStatus{}
	}
	if row := tick(); row.State != "grün" {
		t.Errorf("an idle oneshot turned the service %s: %+v", row.State, row)
	}
	refresh = "failed"
	if row := tick(); row.State != "rot" || row.Units["tick-refresh.service"] != "failed" {
		t.Errorf("a failed refresh is not red in status: %+v", row)
	}
	var found bool
	for _, f := range diag.Probes(ctx, c.env, st, "tick") {
		found = found || (f.Severity == "rot" && strings.Contains(f.Detail, "tick-refresh.service"))
	}
	if !found {
		t.Error("diag does not name the failed refresh")
	}
}
