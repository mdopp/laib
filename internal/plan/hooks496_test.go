package plan

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/state"
)

// #496: a template update that only changed radicale's pre-start was
// "apply … no change": a hook is no unit, so the plan had no change of the
// service and apply never ran the new hook. The hooks' hashes are an
// output of the service: a changed hook is a change of it. The first time
// the file is written (a box from before #496) nothing restarts for it.
func TestAChangedHookIsAChangeOfItsService(t *testing.T) {
	repo := t.TempDir()
	copyDir(t, "../../testdata/golden/basic/state", repo)
	hook := filepath.Join(repo, "services", "whoami", "pre-start")
	os.WriteFile(hook, []byte("#!/bin/sh\necho owner_only\n"), 0o755)
	home := t.TempDir()
	paths := boxfs.Paths{Quadlet: filepath.Join(home, "q"), Render: filepath.Join(home, "r"), Systemd: filepath.Join(home, "s")}
	compute := func() *Plan {
		st, err := state.Load(repo)
		if err != nil {
			t.Fatal(err)
		}
		p, err := Compute(context.Background(), st, nil, paths, "")
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	write := func(p *Plan, skip string) {
		for k, v := range p.Output {
			if k != skip {
				os.MkdirAll(filepath.Dir(Target(paths, k)), 0o755)
				os.WriteFile(Target(paths, k), []byte(v), 0o644)
			}
		}
	}
	write(compute(), "quadlet/whoami/hooks.laib") // a box from before #496
	if p := compute(); len(p.Changes) != 1 || p.Changes[0].Path != "quadlet/whoami/hooks.laib" || len(p.Services) != 0 {
		t.Fatalf("the first hooks file is written, and no service changes for it: %+v %v", p.Changes, p.Services)
	}
	write(compute(), "")
	if p := compute(); !p.Empty() {
		t.Fatalf("an applied box plans nothing: %+v", p.Changes)
	}
	os.WriteFile(hook, []byte("#!/bin/sh\necho from_file\n"), 0o755)
	if p := compute(); !slices.Equal(p.Services, []string{"whoami"}) || len(p.Changes) != 1 || p.Changes[0].Kind != "change" {
		t.Fatalf("a hook-only template update must change its service: %+v %v", p.Changes, p.Services)
	}
}
