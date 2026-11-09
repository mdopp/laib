package apply

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

// ADR 0034 (owner's choice c, 2026-11-09): since #528 the nightly run pulled
// every floating image without data — whoever pushed the tag decided what
// ran. Now a newer digest is a proposal (the Operator's versionen) and the
// run pulls nothing, restarts nothing: not an upstream's image, not our own
// (ghcr.io/mdopp/). It still removes the images nothing needs (#1071), and
// a running backup skips the night.
func TestTheImageRunPullsNothing(t *testing.T) {
	dir := t.TempDir()
	env := &cli.Env{Paths: boxfs.Paths{Home: dir, Journal: filepath.Join(dir, "journal"), Lock: filepath.Join(dir, "apply.lock")}, Actor: "laib"}
	unit := func(name, extra string) map[string]string {
		return map[string]string{name + ".container": "[Container]\nImage=${IMAGE}\n" + extra + "\n[Install]\nWantedBy=default.target\n"}
	}
	st := &state.State{Services: map[string]*state.Service{
		"frage":   {Name: "frage", Values: map[string]state.Value{"IMAGE": {Literal: "ghcr.io/mdopp/laib-frage:latest"}}, Units: unit("frage", "AutoUpdate=registry")},
		"adguard": {Name: "adguard", Values: map[string]state.Value{"IMAGE": {Literal: "docker.io/adguard/adguardhome:latest"}}, Units: unit("adguard", "AutoUpdate=registry")},
		"vault":   {Name: "vault", Values: map[string]state.Value{"IMAGE": {Literal: "docker.io/vaultwarden/server:latest"}}, Units: unit("vault", "")},
	}}
	var calls []string
	backup := "inactive"
	real := sysd.Run
	t.Cleanup(func() { sysd.Run = real })
	sysd.Run = func(_ context.Context, name string, args ...string) (string, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if slices.Contains(args, "is-active") && args[len(args)-1] == "laib-backup.service" {
			return backup, nil
		}
		return "", nil
	}
	if code := ImageRun(context.Background(), env, st); code != cli.ExitOK {
		t.Fatalf("a night that pulls nothing is green: %d", code)
	}
	all := strings.Join(calls, "\n")
	if strings.Contains(all, "podman pull") || strings.Contains(all, "restart") || strings.Contains(all, "podman tag") {
		t.Fatalf("the nightly run pulled or restarted (ADR 0034: a newer digest is a proposal):\n%s", all)
	}
	if !strings.Contains(all, "podman images --format json") {
		t.Fatalf("the night removes the images nothing needs (#1071):\n%s", all)
	}
	backup, calls = "active", nil
	if code := ImageRun(context.Background(), env, st); code != cli.ExitOK || strings.Contains(strings.Join(calls, "\n"), "podman images") {
		t.Fatalf("worked while the backup runs: %v", calls)
	}
}
