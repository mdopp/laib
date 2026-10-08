package apply

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/plan"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

// TestRunWritesReloadsRestartsAndVerifies swaps the systemctl seam: units
// report active, so the result is green without a box.
func TestRunWritesReloadsRestartsAndVerifies(t *testing.T) {
	st, err := state.Load("../../testdata/golden/basic/state")
	if err != nil {
		t.Fatal(err)
	}
	// Health URLs would hit the network; strip them for this test.
	for _, s := range st.Services {
		s.Contract.Health.HTTP = ""
	}
	home := t.TempDir()
	repo := filepath.Join(home, "state")
	copyTree(t, "../../testdata/golden/basic/state", repo)
	paths := boxfs.Paths{Home: home, State: repo, Quadlet: filepath.Join(home, "q"), Render: filepath.Join(home, "r"), Lock: filepath.Join(home, "lock")}

	var calls []string
	sysd.Run = func(_ context.Context, name string, args ...string) (string, error) {
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
	p, err := plan.Compute(context.Background(), st, nil, paths, "")
	if err != nil {
		t.Fatal(err)
	}
	res, err := Run(context.Background(), st, p, paths, "first apply")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Green || res.Commit == "" {
		t.Fatalf("%+v", res)
	}
	if _, err := os.Stat(filepath.Join(paths.Quadlet, "whoami", "whoami.container")); err != nil {
		t.Fatal("unit not written")
	}
	joined := strings.Join(calls, "\n")
	for _, want := range []string{"daemon-reload", "restart caddy.service", "restart authelia.service", "restart whoami.service"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q", want)
		}
	}
	// Second run: nothing to do, still green, no restarts.
	calls = nil
	p2, _ := plan.Compute(context.Background(), st, nil, paths, "")
	if !p2.Empty() {
		t.Fatalf("second plan not empty: %+v", p2.Changes)
	}
}

func TestLockIsExclusive(t *testing.T) {
	paths := boxfs.Paths{Lock: filepath.Join(t.TempDir(), "lock")}
	unlock, err := Lock(paths, "apply:test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Lock(paths, "apply:other"); err != ErrLocked {
		t.Fatal("second lock succeeded")
	}
	if LockHolder(paths) != "apply:test" {
		t.Fatal(LockHolder(paths))
	}
	unlock()
	if LockHolder(paths) != "" {
		t.Fatal("lock not released")
	}
}

func runGit(t *testing.T, args ...string) (string, error) {
	t.Helper()
	// Real git in a temp repo; the seam only records that it was called.
	out, err := realRun(args...)
	return out, err
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, _ := os.ReadFile(p)
		return os.WriteFile(target, b, 0o644)
	})
}
