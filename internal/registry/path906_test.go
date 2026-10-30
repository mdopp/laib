package registry

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/state"
)

// #906: a registry can be a repo whose template folders sit somewhere else
// than templates/ (box.yml registries[].path); after the sync the lookup
// finds them under <clone>/templates like any registry's, and a path that
// leaves the repo or does not exist is refused, never linked.
func TestSyncFollowsTheRegistrysPath(t *testing.T) {
	repo := t.TempDir()
	tpl := filepath.Join(repo, "vorlagen", "echo")
	os.MkdirAll(tpl, 0o755)
	os.WriteFile(filepath.Join(tpl, "laib.yml"), []byte("name: echo\n"), 0o644)
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "."}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "t"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	env := &cli.Env{Paths: boxfs.Paths{Home: t.TempDir()}}
	for _, bad := range []string{"../x", "fehlt"} {
		st := &state.State{Box: state.Box{Registries: []state.Registry{{Name: "b", URL: repo, Path: bad}}}}
		if _, err := Sync(context.Background(), env, st); err == nil {
			t.Errorf("path %q: synced, want refused", bad)
		}
	}
	st := &state.State{Box: state.Box{Registries: []state.Registry{{Name: "t", URL: repo, Path: "vorlagen"}}}}
	for range 2 { // the second run fetches into the clone and keeps the link
		if lines, err := Sync(context.Background(), env, st); err != nil || len(lines) != 1 {
			t.Fatalf("sync: %v %v", lines, err)
		}
	}
	if got := Available(env)["echo"]; len(got) != 1 || got[0] != "t" {
		t.Fatalf("echo from registry t not listed: %v", got)
	}
}
