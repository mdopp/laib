package apply

import (
	"context"
	"encoding/json"
	"errors"
	"os"
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

// #1071: every nightly pull left the image it replaced in podman's storage,
// ~350 GiB on the data disk. A green run removes what nothing needs: not an
// image a container uses, one a unit or an *IMAGE value names (a stopped
// service has no container), the hooks image, nor what the last green run
// kept (a rollback's local copy, one more night). The first run only records.
func TestAGreenRunRemovesTheImagesNothingNeedsAndKeepsTheRollbacksForANight(t *testing.T) {
	dir := t.TempDir()
	env := &cli.Env{Paths: boxfs.Paths{Home: dir, Journal: filepath.Join(dir, "journal"), Quadlet: filepath.Join(dir, "quadlet")}, Actor: "laib"}
	os.MkdirAll(filepath.Join(env.Paths.Quadlet, "paused"), 0o755)
	os.WriteFile(filepath.Join(env.Paths.Quadlet, "paused", "paused.container"), []byte("[Container]\nImage=ghcr.io/x/paused:1\n"), 0o644)
	st := &state.State{Services: map[string]*state.Service{"auth": {Name: "auth", Values: map[string]state.Value{"ANMELDUNGEN_IMAGE": {Literal: "ghcr.io/x/script:1"}, "PORT": {Literal: "ghcr.io/x/not-an-image"}}}}}
	images := []StoredImage{
		{ID: "running", Names: []string{"ghcr.io/x/run:latest"}, Containers: 1},
		{ID: "paused", Names: []string{"ghcr.io/x/paused:1"}},
		{ID: "script", Names: []string{"ghcr.io/x/script:1"}},
		{ID: "hooks", Names: []string{"ghcr.io/mdopp/laib-hooks:latest"}},
		{ID: "lastnight", Names: nil},
		{ID: "old", Names: []string{"ghcr.io/x/a:1", "ghcr.io/x/b:1"}},
		{ID: "dangling"},
	}
	ids := map[string]string{}
	for _, img := range images {
		for _, n := range img.Names {
			ids[n] = img.ID
		}
	}
	var calls []string
	var psErr error
	real := sysd.Run
	t.Cleanup(func() { sysd.Run = real })
	sysd.Run = func(_ context.Context, name string, args ...string) (string, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		switch {
		case args[0] == "images":
			b, _ := json.Marshal(images)
			return string(b), nil
		case args[0] == "image" && args[1] == "inspect":
			if id, ok := ids[args[len(args)-1]]; ok {
				return id + "\n", nil
			}
			return "Error: no such image", errors.New("exit 125")
		case args[0] == "ps":
			return "running\n", psErr
		}
		return "", nil
	}
	rmis := func() (out []string) {
		for _, c := range calls {
			if strings.HasPrefix(c, "podman rmi") {
				out = append(out, c)
			}
		}
		return out
	}
	PruneImages(context.Background(), env, st, "ghcr.io/mdopp/laib-hooks:latest")
	if r := rmis(); len(r) != 0 {
		t.Fatalf("the first run removed before it knew what ran before: %v", r)
	}
	os.WriteFile(filepath.Join(dir, keptFile), []byte(`["lastnight","running"]`), 0o644)
	calls = nil
	PruneImages(context.Background(), env, st, "ghcr.io/mdopp/laib-hooks:latest")
	if got, want := rmis(), []string{"podman rmi ghcr.io/x/a:1 ghcr.io/x/b:1", "podman rmi dangling"}; !slices.Equal(got, want) {
		t.Fatalf("removed %v, want %v", got, want)
	}
	for _, c := range calls {
		if strings.Contains(c, "prune") || strings.Contains(c, "volume") || strings.Contains(c, " -f") {
			t.Fatalf("a prune, a volume or a forced rmi: %s", c)
		}
	}
	var kept []string
	b, _ := os.ReadFile(filepath.Join(dir, keptFile))
	json.Unmarshal(b, &kept)
	if want := []string{"running", "paused", "script", "hooks"}; !slices.Equal(kept, want) {
		t.Fatalf("kept for the next night %v, want %v (last night's copy goes then)", kept, want)
	}
	es, _ := journal.Read(env.Paths.Journal, time.Time{})
	if len(es) == 0 || es[len(es)-1].Verb != "updates" || !strings.Contains(strings.ToLower(es[len(es)-1].Detail), "alte abbilder entfernt") {
		t.Fatalf("not journaled: %+v", es)
	}
	// without the containers' images nothing is known to be unused
	calls, psErr = nil, errors.New("podman down")
	PruneImages(context.Background(), env, st)
	if r := rmis(); len(r) != 0 {
		t.Fatalf("removed without knowing the containers: %v", r)
	}
}

func TestGigabytesReadsAsPeopleReadIt(t *testing.T) {
	for b, want := range map[uint64]string{300 << 20: "300 MB", 3<<30 + 1<<29: "3,5 GB", 12 << 30: "12 GB"} {
		if got := Gigabytes(b); got != want {
			t.Errorf("%d: %q, want %q", b, got, want)
		}
	}
}
