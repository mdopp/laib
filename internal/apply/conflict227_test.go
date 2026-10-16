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

// ServiceBay's auth.kube still in the Quadlet root: apply writes nothing,
// so the running Authelia's configuration.yml and LLDAP stay untouched (#227).
func TestApplyRefusesWhileServiceBaysAuthKubeIsThere(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "state")
	copyTree(t, "../../testdata/golden/basic/state", repo)
	st, err := state.Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	paths := boxfs.Paths{Home: home, State: repo, Quadlet: filepath.Join(home, "q"), Render: filepath.Join(home, "r"), Systemd: filepath.Join(home, "s"), Lock: filepath.Join(home, "lock")}
	os.MkdirAll(paths.Quadlet, 0o755)
	os.WriteFile(filepath.Join(paths.Quadlet, "auth.kube"), []byte("[Kube]\nYaml=auth.yml\n"), 0o644)
	var calls []string
	sysd.Run = func(_ context.Context, name string, args ...string) (string, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return "", nil
	}
	p, err := plan.Compute(context.Background(), st, nil, paths, "auth")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), st, p, paths, nil, "test"); err == nil || !strings.Contains(err.Error(), "auth.kube") {
		t.Fatalf("apply did not refuse: %v", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(paths.Quadlet, "auth")); len(entries) > 0 || len(calls) > 0 {
		t.Fatalf("apply touched the box: %v %v", entries, calls)
	}
}
