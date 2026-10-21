package apply

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

// #528: nobody kept the box current — podman-auto-update.timer masked since
// the migration evening. The classification: a container the template marks
// AutoUpdate=registry (no data a new image would migrate) with a floating
// image is the nightly run's; a database's container, or a pin, is a proposal.
func TestAutoImageIsTheNoDataMarkAndAFloatingImage(t *testing.T) {
	svc := func(img, extra string) *state.Service {
		return &state.Service{Name: "x", Values: map[string]state.Value{"IMAGE": {Literal: img}},
			Units: map[string]string{"x.container": "[Container]\nImage=${IMAGE}\n" + extra + "\n[Install]\nWantedBy=default.target\n"}}
	}
	for _, c := range []struct {
		img, extra string
		auto       bool
	}{
		{"ghcr.io/mdopp/laib-frage:latest", "AutoUpdate=registry", true},        // Laib's own image
		{"docker.io/searxng/searxng:2026.10.1", "AutoUpdate=registry", true},    // a tag the template lets float
		{"docker.io/vaultwarden/server:latest", "", false},                      // a database: a proposal
		{"ghcr.io/x/llama:b1@sha256:00", "AutoUpdate=registry", false},          // a digest never floats
		{"ghcr.io/home-assistant/home-assistant:stable", "Volume=/x:/y", false}, // HA: a proposal
	} {
		if got := AutoImage(svc(c.img, c.extra), "IMAGE"); got != c.auto {
			t.Errorf("%s %q: auto %v, want %v", c.img, c.extra, got, c.auto)
		}
	}
	if AutoImage(svc("a:latest", "AutoUpdate=registry"), "OTHER_IMAGE") {
		t.Error("a key no container names is no image of the run")
	}
}

// The nightly run pulls the images without data, restarts and verifies the
// service whose image moved; a red verify tags the previous image back and
// restarts again — journaled red. A service with data is not pulled at all,
// nor is a paused one; a running backup skips the night.
func TestTheImageRunRollsBackARedServiceToItsPreviousImage(t *testing.T) {
	dir := t.TempDir()
	env := &cli.Env{Paths: boxfs.Paths{Home: dir, Journal: filepath.Join(dir, "journal"), Lock: filepath.Join(dir, "apply.lock")}, Actor: "laib"}
	unit := func(name, extra string) map[string]string {
		return map[string]string{name + ".container": "[Container]\nImage=${IMAGE}\n" + extra + "\n[Install]\nWantedBy=default.target\n"}
	}
	st := &state.State{Services: map[string]*state.Service{
		"frage": {Name: "frage", Values: map[string]state.Value{"IMAGE": {Literal: "ghcr.io/mdopp/laib-frage:latest"}}, Units: unit("frage", "AutoUpdate=registry")},
		"haus":  {Name: "haus", Values: map[string]state.Value{"IMAGE": {Literal: "ghcr.io/mdopp/haus:latest"}}, Units: unit("haus", "AutoUpdate=registry")},
		"vault": {Name: "vault", Values: map[string]state.Value{"IMAGE": {Literal: "docker.io/vaultwarden/server:latest"}}, Units: unit("vault", "")},
	}}
	ids := map[string]string{"ghcr.io/mdopp/laib-frage:latest": "old-frage", "ghcr.io/mdopp/haus:latest": "old-haus"}
	var calls []string
	backup, frageRed := "inactive", true
	real := sysd.Run
	t.Cleanup(func() { sysd.Run = real })
	sysd.Run = func(_ context.Context, name string, args ...string) (string, error) {
		line := name + " " + strings.Join(args, " ")
		calls = append(calls, line)
		switch {
		case slices.Contains(args, "is-active") && args[len(args)-1] == "laib-backup.service":
			return backup, nil
		case slices.Contains(args, "is-active"):
			if args[len(args)-1] == "frage.service" && frageRed && ids["ghcr.io/mdopp/laib-frage:latest"] == "new-frage" {
				return "failed", nil
			}
			return "active", nil
		case name == "podman" && args[0] == "image":
			return ids[args[len(args)-1]], nil
		case name == "podman" && args[0] == "pull":
			img := args[len(args)-1]
			ids[img] = strings.Replace(ids[img], "old-", "new-", 1)
			return ids[img] + "\n", nil
		case name == "podman" && args[0] == "tag":
			ids[args[2]] = args[1]
		}
		return "", nil
	}
	if code := ImageRun(context.Background(), env, st); code != cli.ExitRed {
		t.Fatalf("a rolled-back service is a red night: %d", code)
	}
	all := strings.Join(calls, "\n")
	for _, want := range []string{"podman pull -q ghcr.io/mdopp/laib-frage:latest", "podman pull -q ghcr.io/mdopp/haus:latest", "systemctl --user restart frage.service", "podman tag old-frage ghcr.io/mdopp/laib-frage:latest"} {
		if !strings.Contains(all, want) {
			t.Fatalf("missing %q in:\n%s", want, all)
		}
	}
	if strings.Contains(all, "vaultwarden") || strings.Contains(all, "podman tag old-haus") {
		t.Fatalf("a service with data was pulled, or a green one rolled back:\n%s", all)
	}
	es, _ := journal.Read(env.Paths.Journal, time.Time{})
	got := map[string]journal.Entry{}
	for _, e := range es {
		got[e.Service] = e
	}
	if e := got["frage"]; e.Result != "red" || !strings.Contains(e.Detail, "zurück auf das vorige Image: grün") {
		t.Fatalf("frage's rollback is not journaled red with its outcome: %+v", e)
	}
	if e := got["haus"]; e.Result != "green" || !strings.Contains(e.Detail, "neues Image, grün") {
		t.Fatalf("haus: %+v", e)
	}
	// nothing new: no restart; a paused service and a running backup are left alone
	calls, frageRed, ids["ghcr.io/mdopp/laib-frage:latest"] = nil, false, "new-frage"
	WritePause(env.Paths, Pause{Service: "haus", By: "human:x", Until: time.Now().Add(time.Hour).UTC()})
	if code := ImageRun(context.Background(), env, st); code != cli.ExitOK || strings.Contains(strings.Join(calls, "\n"), "restart") || strings.Contains(strings.Join(calls, "\n"), "haus") {
		t.Fatalf("an image that did not move restarts nothing, a paused one is not pulled: %d\n%s", code, strings.Join(calls, "\n"))
	}
	backup, calls = "active", nil
	if code := ImageRun(context.Background(), env, st); code != cli.ExitOK || strings.Contains(strings.Join(calls, "\n"), "pull") {
		t.Fatalf("pulled while the backup runs: %v", calls)
	}
}
