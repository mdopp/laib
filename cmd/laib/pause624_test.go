package main

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/sysd"
)

// ADR 0027 (#624, part 2): laib-lease runs in a container and stops nothing
// itself. It pauses a service through laib's pause verb over MCP, as the
// lease hand: laib writes the marker and stops (or restarts, for a new
// voice device) the units named, only the service's own; a renewal moves
// the end and touches no unit; resume lifts the pause and brings back what
// the pause stopped or restarted. The lease hand can do nothing else.
func TestTheLeasePausesThroughLaibAndResumeBringsTheUnitsBack(t *testing.T) {
	env, _ := testBox(t)
	var calls []string
	prev := sysd.Run
	sysd.Run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "systemctl" && !slices.Contains(args, "is-active") {
			calls = append(calls, name+" "+strings.Join(args, " "))
		}
		return prev(ctx, name, args...)
	}
	call := func(tool, args string) (string, int) {
		t.Helper()
		st, err := cli.LoadState(env)
		if err != nil {
			t.Fatal(err)
		}
		return mcp.Call(context.Background(), env, st, "lease", tool, map[string]any{"args": args})
	}
	until := time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339)
	if out, code := call("laib_pause", "whoami --until "+until+` --stop whoami.service --restart whoami.service "Grafikkarte reserviert für bote"`); code != cli.ExitOK {
		t.Fatalf("the lease's pause: exit %d: %s", code, out)
	}
	stop, restart := slices.Index(calls, "systemctl --user stop whoami.service"), slices.Index(calls, "systemctl --user restart whoami.service")
	if stop < 0 || restart < stop {
		t.Fatalf("the pause must stop, then restart the units named:\n%s", strings.Join(calls, "\n"))
	}
	pz, ok := apply.Paused(env.Paths)["whoami"]
	if !ok || pz.By != "lease" || !strings.Contains(pz.Reason, "Grafikkarte") || !slices.Equal(pz.Stop, []string{"whoami.service"}) || !slices.Equal(pz.Restart, []string{"whoami.service"}) {
		t.Fatalf("the marker: %+v", pz)
	}

	calls = nil
	later := time.Now().Add(20 * time.Minute).UTC().Format(time.RFC3339)
	if out, code := call("laib_pause", "whoami --until "+later+` "Grafikkarte reserviert für bote"`); code != cli.ExitOK || len(calls) != 0 {
		t.Fatalf("a renewal: exit %d, calls %v: %s", code, calls, out)
	}
	if pz2 := apply.Paused(env.Paths)["whoami"]; !pz2.Until.After(pz.Until) || !slices.Equal(pz2.Stop, pz.Stop) || !slices.Equal(pz2.Restart, pz.Restart) {
		t.Fatalf("a renewal moves the end and keeps the units: %+v", pz2)
	}
	if out, code := call("laib_pause", "whoami --stop sshd.service"); code == cli.ExitOK || len(calls) != 0 {
		t.Fatalf("a unit whoami does not have was stopped: exit %d, %v: %s", code, calls, out)
	}

	if out, code := call("laib_resume", "whoami"); code != cli.ExitOK {
		t.Fatalf("resume: exit %d: %s", code, out)
	}
	if !slices.Contains(calls, "systemctl --user start whoami.service") || !slices.Contains(calls, "systemctl --user restart whoami.service") {
		t.Fatalf("resume must start what the pause stopped and restart what it restarted:\n%s", strings.Join(calls, "\n"))
	}
	if _, ok := apply.Paused(env.Paths)["whoami"]; ok {
		t.Fatal("resume left the marker")
	}
	for _, tool := range []string{"laib_stop", "laib_restart", "laib_apply", "laib_exec"} {
		if out, code := call(tool, "whoami"); code != cli.ExitRefused {
			t.Errorf("the lease hand used %s: exit %d: %s", tool, code, out)
		}
	}
}
