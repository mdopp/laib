package apply

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/plan"
	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

func roleBox(t *testing.T) (*state.State, boxfs.Paths, *[]string) {
	t.Helper()
	st, err := state.Load("../../testdata/golden/basic/state")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range st.Services {
		s.Contract.Health.HTTP = ""
	}
	home := t.TempDir()
	repo := filepath.Join(home, "state")
	copyTree(t, "../../testdata/golden/basic/state", repo)
	st.Box.DataDir = filepath.Join(home, "data")
	paths := boxfs.Paths{Home: home, State: repo, Quadlet: filepath.Join(home, "q"), Render: filepath.Join(home, "r"), Systemd: filepath.Join(home, "s"), Lock: filepath.Join(home, "lock")}
	var calls []string
	real := sysd.Run
	t.Cleanup(func() { sysd.Run = real })
	sysd.Run = func(_ context.Context, name string, args ...string) (string, error) {
		line := name + " " + strings.Join(args, " ")
		calls = append(calls, line)
		switch {
		case name == "git":
			return runGit(t, args...)
		case strings.Contains(line, "is-active"):
			return "active", nil
		case strings.Contains(line, "caddy validate") && strings.Contains(os.Getenv("ROLE_TEST_BROKEN"), "caddy"):
			return "Error: adapting config using caddyfile: unrecognized directive", errors.New("exit status 1")
		}
		return "", nil
	}
	return st, paths, &calls
}

// #50 (1): a change to a role's output restarts the role's service — and
// verifies it. A Caddyfile a snippet broke must not end as a green apply.
func TestARestartedRoleIsVerified(t *testing.T) {
	st, paths, calls := roleBox(t)
	p := &plan.Plan{Output: render.Output(map[string]string{"caddy/Caddyfile": "laib.e2e.home {\n}\n"}), Changes: []plan.Change{{Path: "caddy/Caddyfile", Target: plan.Target(paths, "caddy/Caddyfile"), Kind: "add"}}}
	res, err := Run(context.Background(), st, p, paths, map[string]string{}, "caddyfile only")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(*calls, "\n")
	if !strings.Contains(joined, "restart caddy.service") {
		t.Fatalf("the proxy was not restarted:\n%s", joined)
	}
	var verified bool
	for _, c := range res.Checks {
		if c.Service == "caddy" && strings.HasPrefix(c.What, "unit ") {
			verified = true
		}
	}
	if !verified {
		t.Fatalf("the restarted proxy was not verified: %+v", res.Checks)
	}
}

// #50 (1): the rendered Caddyfile is validated before it is written.
func TestABrokenCaddyfileIsRefusedBeforeWriting(t *testing.T) {
	t.Setenv("ROLE_TEST_BROKEN", "caddy")
	st, paths, calls := roleBox(t)
	target := plan.Target(paths, "caddy/Caddyfile")
	p := &plan.Plan{Output: render.Output(map[string]string{"caddy/Caddyfile": "laib.e2e.home {\n  nonsense_directive\n}\n"}), Changes: []plan.Change{{Path: "caddy/Caddyfile", Target: target, Kind: "add"}}}
	_, err := Run(context.Background(), st, p, paths, map[string]string{}, "broken caddyfile")
	if err == nil || !strings.Contains(err.Error(), "Caddyfile") {
		t.Fatalf("a broken Caddyfile must refuse the apply: %v", err)
	}
	if _, statErr := os.Stat(target); statErr == nil {
		t.Fatal("the broken Caddyfile was written")
	}
	if strings.Contains(strings.Join(*calls, "\n"), "restart caddy.service") {
		t.Fatal("the proxy was restarted into a broken config")
	}
	if !strings.Contains(strings.Join(*calls, "\n"), "caddy validate") {
		t.Fatalf("no validation run:\n%s", strings.Join(*calls, "\n"))
	}
}
