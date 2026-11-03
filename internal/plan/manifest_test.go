package plan

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/state"
)

// #41 (3): apply records every file it rendered; plan deletes what the
// manifest has but render no longer produces — a host unit of a disabled
// operator, the lease unit of a removed llama, the quadlet folder of a
// service a rollback took out of the state.
func TestPlanDeletesWhatTheManifestHasButRenderNoLonger(t *testing.T) {
	st, err := state.Load("../../testdata/golden/basic/state")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	paths := boxfs.Paths{Home: home, Quadlet: filepath.Join(home, "q"), Render: filepath.Join(home, "r"), Systemd: filepath.Join(home, "s")}
	stale := filepath.Join(paths.Systemd, "laib-operator.service")
	gone := filepath.Join(paths.Quadlet, "gone", "gone.container")
	os.MkdirAll(paths.Systemd, 0o755)
	os.MkdirAll(filepath.Dir(gone), 0o755)
	os.WriteFile(stale, []byte("[Service]\n"), 0o644)
	os.WriteFile(gone, []byte("[Container]\n"), 0o644)
	if err := WriteManifest(paths, []string{stale, gone, filepath.Join(paths.Quadlet, "whoami", "whoami.container")}); err != nil {
		t.Fatal(err)
	}
	p, err := Compute(context.Background(), st, map[string]string{"WHOAMI_TOKEN": "x"}, paths, "")
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, c := range p.Changes {
		kinds[c.Target] = c.Kind
	}
	if kinds[stale] != "delete" || kinds[gone] != "delete" {
		t.Fatalf("manifest leftovers must be deleted: %v", kinds)
	}
	for _, name := range p.Unmanaged {
		if name == "gone" {
			t.Fatal("a folder the manifest owns is not unmanaged")
		}
	}
	// a service the state has keeps its files, nothing in the manifest is deleted twice
	if kinds[filepath.Join(paths.Quadlet, "whoami", "whoami.container")] == "delete" {
		t.Fatal("a rendered file was marked for deletion")
	}
}

// #1024, ADR 0030: 0.x's render/persona tree is in the manifest of a box
// that ran 0.x, and 1.x renders no persona; the tree stays on disk until the
// owner deletes it, since a service from before 1.0 mounts it until its
// template update.
func TestPlanLeavesThePersonaTreeOf0xOnDisk(t *testing.T) {
	st, err := state.Load("../../testdata/golden/basic/state")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	paths := boxfs.Paths{Home: home, Quadlet: filepath.Join(home, "q"), Render: filepath.Join(home, "r"), Systemd: filepath.Join(home, "s")}
	kept := filepath.Join(paths.Render, "persona", "joshua", "persona.json")
	os.MkdirAll(filepath.Dir(kept), 0o755)
	os.WriteFile(kept, []byte("{}\n"), 0o644)
	WriteManifest(paths, []string{kept})
	p, err := Compute(context.Background(), st, map[string]string{"WHOAMI_TOKEN": "x"}, paths, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range p.Changes {
		if c.Target == kept {
			t.Fatalf("the persona tree must stay on disk (ADR 0030): %s %s", c.Kind, c.Target)
		}
	}
}
