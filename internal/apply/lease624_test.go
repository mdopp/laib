package apply

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/plan"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

// ADR 0027 (#624, part 2): a box whose llama template ran laib-lease as a
// host unit gets the container in one apply: the host unit is stopped and
// disabled before the reload and leaves the box, and the container that
// replaces it under the same unit name is started after the reload.
func TestOneApplyMovesTheLeaseFromAHostUnitIntoItsContainer(t *testing.T) {
	home := t.TempDir()
	paths := boxfs.Paths{Home: home, State: filepath.Join(home, "state"), Quadlet: filepath.Join(home, "q"), Render: filepath.Join(home, "r"), Systemd: filepath.Join(home, "s"), Lock: filepath.Join(home, "lock")}
	os.MkdirAll(paths.State, 0o755)
	os.WriteFile(filepath.Join(paths.State, "box.yml"), []byte("domain: x.home\n"), 0o644)
	st := &state.State{Box: state.Box{Domain: "x.home", DataDir: filepath.Join(home, "d")}, Services: map[string]*state.Service{
		"lm": {Name: "lm", Values: map[string]state.Value{}, Contract: state.Contract{Health: state.Health{None: "a stub"}},
			Units: map[string]string{"lm.container": "[Container]\nImage=x\n\n[Install]\nWantedBy=default.target\n", "lm-lease.container": "[Container]\nImage=ghcr.io/mdopp/laib-lease:latest\nNetwork=host\n\n[Install]\nWantedBy=default.target\n"}}}}
	st.Box.Laib.Port = 5900
	os.MkdirAll(paths.Systemd, 0o755)
	native := filepath.Join(paths.Systemd, "lm-lease.service")
	os.WriteFile(native, []byte("[Service]\nExecStart=/var/opt/laib/laib-lease\n\n[Install]\nWantedBy=default.target\n"), 0o644)
	if err := plan.WriteManifest(paths, []string{native}); err != nil {
		t.Fatal(err)
	}
	var calls []string
	real := sysd.Run
	t.Cleanup(func() { sysd.Run = real })
	sysd.Run = func(ctx context.Context, name string, args ...string) (string, error) {
		line := name + " " + strings.Join(args, " ")
		calls = append(calls, line)
		switch {
		case name == "git":
			return runGit(t, args...)
		case strings.Contains(line, "is-active"):
			return "active", nil
		}
		return "", nil
	}
	sec := map[string]string{}
	p, err := plan.Compute(context.Background(), st, sec, paths, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), st, p, paths, sec, "test"); err != nil {
		t.Fatal(err)
	}
	at := func(want string) int {
		return slices.IndexFunc(calls, func(c string) bool { return strings.Contains(c, want) })
	}
	reload, stop, disable, start := at("daemon-reload"), at("stop lm-lease.service"), at("disable lm-lease.service"), at("restart lm-lease.service")
	if stop < 0 || disable < 0 || stop > reload || disable > reload || start < reload {
		t.Fatalf("the host unit must stop and be disabled before the reload, the container start after it:\n%s", strings.Join(calls, "\n"))
	}
	if _, err := os.Stat(native); err == nil {
		t.Error("the host unit is still on the box")
	}
	if _, err := os.Stat(filepath.Join(paths.Quadlet, "lm", "lm-lease.container")); err != nil {
		t.Error("the container is not written")
	}
}
