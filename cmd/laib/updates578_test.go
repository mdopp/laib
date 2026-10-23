package main

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/sysd"
)

// nightBox is envBox with laib-images.service and laib-backup.service in
// the given states; everything else is active.
func nightBox(t *testing.T, units map[string]string) (*cliEnv, *[]string) {
	env, _, calls := envBox(t)
	run := sysd.Run
	sysd.Run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "systemctl" && slices.Contains(args, "is-active") {
			*calls = append(*calls, name+" "+strings.Join(args, " "))
			if s, ok := units[args[len(args)-1]]; ok {
				return s, nil
			}
			return "active", nil
		}
		if name == "systemctl" && slices.Contains(args, "show") {
			*calls = append(*calls, name+" "+strings.Join(args, " "))
			return "@1700000000", nil
		}
		return run(ctx, name, args...)
	}
	return env, calls
}

func started(calls []string) bool {
	return slices.Contains(calls, "systemctl --user start --no-block laib-images.service")
}

// #578: laib updates --now starts the nightly run (Laib's own update, then
// the images) without waiting for it, as an apply-tier step, and says where
// its result shows. The night's guards hold before it starts: no backup
// running, the lock free; and a second start while one runs is refused.
func TestUpdatesNowStartsTheNightlyRun(t *testing.T) {
	ctx := context.Background()
	env, calls := nightBox(t, map[string]string{"laib-images.service": "inactive", "laib-backup.service": "inactive"})
	var out bytes.Buffer
	env.Out = &out
	if code := cmdUpdates(ctx, env, []string{"--now"}); code != cli.ExitOK {
		t.Fatalf("--now: exit %d", code)
	}
	if !started(*calls) {
		t.Fatalf("laib-images.service was not started without waiting: %v", *calls)
	}
	if s := out.String(); !strings.Contains(s, "laib updates") || !strings.Contains(s, "self-update.json") {
		t.Errorf("the answer does not say where the result shows: %q", s)
	}
	es, _ := journal.Read(env.Paths.Journal, time.Time{})
	if !slices.ContainsFunc(es, func(e journal.Entry) bool { return e.Verb == "updates" && e.Actor == "ops" }) {
		t.Errorf("the start is not journaled: %+v", es)
	}

	// a propose hand may not start it
	*calls = nil
	pi := &cliEnv{Actor: "pi", Paths: env.Paths, Out: &out, ErrOut: &out}
	if cmdUpdates(ctx, pi, []string{"--now"}) != cli.ExitRefused || started(*calls) {
		t.Error("a propose hand started the run")
	}
}

func TestUpdatesNowRefusesWhileTheNightCannotRun(t *testing.T) {
	ctx := context.Background()
	for name, c := range map[string]struct {
		units map[string]string
		lock  bool
		want  string
	}{
		"running": {map[string]string{"laib-images.service": "activating", "laib-backup.service": "inactive"}, false, "läuft schon"},
		"backup":  {map[string]string{"laib-images.service": "inactive", "laib-backup.service": "active"}, false, "Sicherung"},
		"locked":  {map[string]string{"laib-images.service": "inactive", "laib-backup.service": "inactive"}, true, "gesperrt"},
		"red":     {map[string]string{"laib-images.service": "inactive", "laib-backup.service": "inactive", "whoami.service": "failed"}, false, "nicht grün"},
	} {
		env, calls := nightBox(t, c.units)
		if c.lock {
			unlock, _, err := apply.Lock(env.Paths, "apply:human:test")
			if err != nil {
				t.Fatal(err)
			}
			defer unlock()
		}
		var errOut bytes.Buffer
		env.Out, env.ErrOut = &bytes.Buffer{}, &errOut
		*calls = nil
		if code := cmdUpdates(ctx, env, []string{"--now"}); code != cli.ExitRefused {
			t.Errorf("%s: exit %d, want refused", name, code)
		}
		if started(*calls) {
			t.Errorf("%s: started anyway", name)
		}
		if !strings.Contains(errOut.String(), c.want) {
			t.Errorf("%s: the refusal does not say why (%q): %q", name, c.want, errOut.String())
		}
	}
}

// #578: the verb over MCP, as the typed parameter now; and laib updates
// shows a run that is under way.
func TestUpdatesNowOverMCPAndTheRunningRow(t *testing.T) {
	ctx := context.Background()
	env, calls := nightBox(t, map[string]string{"laib-images.service": "inactive", "laib-backup.service": "inactive"})
	st, err := cli.LoadState(env)
	if err != nil {
		t.Fatal(err)
	}
	out, code := mcp.Call(ctx, env, st, "ops", "laib_updates", map[string]any{"now": true})
	if code != cli.ExitOK || !started(*calls) {
		t.Fatalf("over MCP: exit %d %s %v", code, out, *calls)
	}
	var ans struct{ Started bool }
	if json.Unmarshal([]byte(out), &ans) != nil || !ans.Started {
		t.Errorf("the MCP answer: %s", out)
	}

	env, _ = nightBox(t, map[string]string{"laib-images.service": "activating"})
	var text bytes.Buffer
	env.Out = &text
	if code := cmdUpdates(ctx, env, nil); code != cli.ExitOK {
		t.Fatalf("updates: exit %d", code)
	}
	if !strings.Contains(text.String(), "läuft seit") {
		t.Errorf("laib updates does not show the run under way: %q", text.String())
	}
	var rows bytes.Buffer
	env.Out, env.JSON = &rows, true
	cmdUpdates(ctx, env, nil)
	var ups []struct{ Service, Running string }
	json.Unmarshal(rows.Bytes(), &ups)
	if !slices.ContainsFunc(ups, func(u struct{ Service, Running string }) bool { return u.Service == "laib" && u.Running != "" }) {
		t.Errorf("the laib row carries no running: %s", rows.String())
	}
}
