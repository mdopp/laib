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

// #647: a hook gets a value as a unit does, its builtins rendered; a secret
// stays the secret's value.
func TestHookGetsAValueWithItsBuiltinsRendered(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "services", "haus")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "post-deploy"), []byte("#!/bin/sh\n"), 0o755)
	paths := boxfs.Paths{Home: home, Render: filepath.Join(home, "r")}
	st := &state.State{Box: state.Box{Domain: "x.home", DataDir: filepath.Join(home, "d")}, Services: map[string]*state.Service{
		"haus": {Name: "haus", Dir: dir, Values: map[string]state.Value{
			"RADICALE_PUBLIC_URL": {Literal: "https://caldav.${DOMAIN}"}, "KEY": {Secret: "HAUS_KEY"}}},
	}}
	realHook := RunHook
	defer func() { RunHook = realHook }()
	var env string
	RunHook = func(_ context.Context, h HookSpec) (string, error) {
		b, _ := os.ReadFile(h.EnvFile)
		env = string(b)
		return "", nil
	}
	if c, ran := runHook(context.Background(), st, st.Services["haus"], paths, "post-deploy", map[string]string{"HAUS_KEY": "k-1"}, map[string]string{}); !ran || !c.OK {
		t.Fatalf("the hook did not run green: %+v", c)
	}
	for _, want := range []string{"\nRADICALE_PUBLIC_URL=https://caldav.x.home\n", "\nKEY=k-1\n"} {
		if !strings.Contains(env, want) {
			t.Errorf("the hook's environment lacks %q:\n%s", strings.TrimSpace(want), env)
		}
	}
}
