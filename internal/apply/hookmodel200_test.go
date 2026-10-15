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

// #200: a hook runs in the hooks container and writes config for pods (the
// Bote's Hermes), so it gets the model builtins as a unit does: the model
// role's proxy and lease over host.containers.internal, and the role's key.
func TestHookGetsTheModelBuiltinsAsAUnitDoes(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "services", "bote")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "post-deploy"), []byte("#!/bin/sh\n"), 0o755)
	paths := boxfs.Paths{Home: home, Render: filepath.Join(home, "r")}
	hookEnv := func(st *state.State, secrets map[string]string) map[string]string {
		t.Helper()
		env := map[string]string{}
		realHook := RunHook
		defer func() { RunHook = realHook }()
		RunHook = func(_ context.Context, h HookSpec) (string, error) {
			b, _ := os.ReadFile(h.EnvFile)
			for _, l := range strings.Split(string(b), "\n") {
				if k, v, ok := strings.Cut(l, "="); ok {
					env[k] = v
				}
			}
			return "", nil
		}
		if c, ran := runHook(context.Background(), st, st.Services["bote"], paths, "post-deploy", secrets, map[string]string{}); !ran || !c.OK {
			t.Fatalf("the hook did not run green: %+v", c)
		}
		return env
	}
	st := &state.State{Box: state.Box{Domain: "x.home", DataDir: filepath.Join(home, "d")}, Services: map[string]*state.Service{
		"bote":  {Name: "bote", Dir: dir},
		"llama": {Name: "llama", Contract: state.Contract{Routes: []state.Route{{Host: "llama", Port: 11435}}}},
	}}
	st.Box.Roles.Model = "llama"
	env := hookEnv(st, map[string]string{"MODEL_API_KEY": "k-role"})
	for k, want := range map[string]string{
		"MODEL_URL":   "http://host.containers.internal:11435/v1",
		"MODEL_LEASE": "http://host.containers.internal:11435/api/model-lease",
		"MODEL_KEY":   "k-role",
	} {
		if env[k] != want {
			t.Errorf("with the model role a hook gets %s=%q, want %q", k, env[k], want)
		}
	}
	// a peer's proxy stays as box.yml names it, with its key
	st.Box.Roles.Model = ""
	st.Box.Model.URL, st.Box.Model.KeySecret = "http://10.0.0.20:11435/v1", "PEER_KEY"
	env = hookEnv(st, map[string]string{"PEER_KEY": "k-peer"})
	if env["MODEL_URL"] != "http://10.0.0.20:11435/v1" || env["MODEL_KEY"] != "k-peer" {
		t.Errorf("with box.yml model a hook gets MODEL_URL=%q MODEL_KEY=%q", env["MODEL_URL"], env["MODEL_KEY"])
	}
}
