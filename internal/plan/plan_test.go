package plan

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/render"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

// Plan reads the box's podman secrets through sysd.Run (#224); this box has
// none, whatever podman the test machine runs.
func TestMain(m *testing.M) {
	sysd.Run = func(context.Context, string, ...string) (string, error) { return "", nil }
	os.Exit(m.Run())
}

func TestComputeAddsChangesDeletesAndUnmanaged(t *testing.T) {
	st, err := state.Load("../../testdata/golden/basic/state")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	paths := boxfs.Paths{Quadlet: filepath.Join(home, "q"), Render: filepath.Join(home, "r")}
	// whoami exists on the box with stale content plus a leftover file;
	// servicebay (ignored) and legacy (unmanaged) sit beside it.
	os.MkdirAll(filepath.Join(paths.Quadlet, "whoami"), 0o755)
	os.WriteFile(filepath.Join(paths.Quadlet, "whoami", "whoami.container"), []byte("old"), 0o644)
	os.WriteFile(filepath.Join(paths.Quadlet, "whoami", "leftover.volume"), []byte("x"), 0o644)
	os.MkdirAll(filepath.Join(paths.Quadlet, "servicebay"), 0o755)
	os.MkdirAll(filepath.Join(paths.Quadlet, "legacy"), 0o755)

	secrets := map[string]string{"WHOAMI_TOKEN": "s3cr3t"}
	p, err := Compute(context.Background(), st, secrets, paths, "")
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, c := range p.Changes {
		kinds[c.Path] = c.Kind
		if c.Diff != "" && contains(c.Diff, "s3cr3t") {
			t.Fatalf("secret leaked in diff of %s", c.Path)
		}
	}
	if kinds["quadlet/whoami/whoami.container"] != "change" || kinds["quadlet/whoami/leftover.volume"] != "delete" || kinds["quadlet/caddy/caddy.container"] != "add" || kinds["caddy/Caddyfile"] != "add" {
		t.Fatalf("%v", kinds)
	}
	if len(p.Unmanaged) != 1 || p.Unmanaged[0] != "legacy" {
		t.Fatalf("unmanaged %v", p.Unmanaged)
	}
	only, _ := Compute(context.Background(), st, secrets, paths, "whoami")
	for _, c := range only.Changes {
		if c.Service != "" && c.Service != "whoami" && !slices.Contains(render.Own, c.Service) { // laib's own programs are the box's (#633)
			t.Fatalf("only=whoami included %s", c.Path)
		}
	}
}

func contains(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

func TestRootKubeFileConflictsWithAService(t *testing.T) {
	st, err := state.Load("../../testdata/golden/basic/state")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	paths := boxfs.Paths{Quadlet: filepath.Join(home, "q"), Render: filepath.Join(home, "r")}
	os.MkdirAll(paths.Quadlet, 0o755)
	os.WriteFile(filepath.Join(paths.Quadlet, "whoami.kube"), []byte("[Kube]\nYaml=x\n"), 0o644)
	p, err := Compute(context.Background(), st, nil, paths, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Conflicts) != 1 || !contains(p.Conflicts[0], "whoami.service") || !contains(p.Conflicts[0], "whoami.kube") {
		t.Fatalf("%v", p.Conflicts)
	}
	for _, u := range p.Unmanaged {
		if u == "whoami.kube" {
			t.Fatal("a conflicting file must not also count as merely unmanaged")
		}
	}
}
