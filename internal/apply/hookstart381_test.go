package apply

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/state"
)

// #381: Authelia's default_redirection_url (where a login without rd= lands)
// followed a hard-coded laib.<domain>, not box.yml laib.host. A hook gets
// LAIB_START_URL: the family's entry (the service that provides haus) once
// it is installed, else the box page. ServiceBay landed on its portal (www.).
func TestHookGetsTheStartURL(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "services", "auth")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "pre-start"), []byte("#!/bin/sh\n"), 0o755)
	paths := boxfs.Paths{Home: home, Render: filepath.Join(home, "r")}
	startURL := func(st *state.State) string {
		t.Helper()
		var got string
		realHook := RunHook
		defer func() { RunHook = realHook }()
		RunHook = func(_ context.Context, h HookSpec) (string, error) {
			b, _ := os.ReadFile(h.EnvFile)
			for _, l := range strings.Split(string(b), "\n") {
				if v, ok := strings.CutPrefix(l, "LAIB_START_URL="); ok {
					got = v
				}
			}
			return "", nil
		}
		if c, ran := runHook(context.Background(), st, st.Services["auth"], paths, "pre-start", nil, map[string]string{}); !ran || !c.OK {
			t.Fatalf("the hook did not run green: %+v", c)
		}
		return got
	}
	st := &state.State{Box: state.Box{Domain: "x.home", DataDir: filepath.Join(home, "d")}, Services: map[string]*state.Service{
		"auth": {Name: "auth", Dir: dir},
	}}
	st.Box.Laib.Host = "admin"
	if got := startURL(st); got != "https://admin.x.home/" {
		t.Errorf("with laib.host admin and no haus, LAIB_START_URL=%q, want https://admin.x.home/", got)
	}
	st.Services["haus"] = &state.Service{Name: "haus", Contract: state.Contract{Provides: []string{"haus"}, Routes: []state.Route{{Host: "haus", Port: 8650}}}}
	if got := startURL(st); got != "https://haus.x.home/" {
		t.Errorf("with haus installed, LAIB_START_URL=%q, want the family's entry https://haus.x.home/", got)
	}
}
