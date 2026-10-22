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

// #561: a hook gets the household's address only when its service's units
// name a ZUHAUSE_ value (Home Assistant's post-deploy sets HA's location
// from it); every other service's hooks never see it.
func TestOnlyAServiceThatReadsZuhauseGetsItInItsHooks(t *testing.T) {
	home := t.TempDir()
	paths := boxfs.Paths{Home: home, Render: filepath.Join(home, "r")}
	lat, lon := 52.1234, 8.0456
	st := &state.State{Box: state.Box{Domain: "x.home", DataDir: filepath.Join(home, "d"),
		Zuhause: state.Zuhause{Street: "Musterweg 12", City: "Musterstadt", Latitude: &lat, Longitude: &lon}}, Services: map[string]*state.Service{}}
	for name, unit := range map[string]string{"home-assistant": "[Container]\nEnvironment=ZUHAUSE_REV=${ZUHAUSE_REV}\n", "whoami": "[Container]\nImage=x\n"} {
		dir := filepath.Join(home, "services", name)
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "post-deploy"), []byte("#!/bin/sh\n"), 0o755)
		st.Services[name] = &state.Service{Name: name, Dir: dir, Units: map[string]string{name + ".container": unit}}
	}
	hookEnv := func(svc string) string {
		t.Helper()
		var env string
		realHook := RunHook
		defer func() { RunHook = realHook }()
		RunHook = func(_ context.Context, h HookSpec) (string, error) {
			b, _ := os.ReadFile(h.EnvFile)
			env = string(b)
			return "", nil
		}
		if c, ran := runHook(context.Background(), st, st.Services[svc], paths, "post-deploy", map[string]string{}, map[string]string{}); !ran || !c.OK {
			t.Fatalf("the hook did not run green: %+v", c)
		}
		return env
	}
	ha := hookEnv("home-assistant")
	for _, want := range []string{"ZUHAUSE_LATITUDE=52.1234\n", "ZUHAUSE_LONGITUDE=8.0456\n", "ZUHAUSE_CITY=Musterstadt\n", "ZUHAUSE_ELEVATION=\n"} {
		if !strings.Contains(ha, want) {
			t.Errorf("home-assistant's hook lacks %q", want)
		}
	}
	if strings.Contains(hookEnv("whoami"), "Musterweg") {
		t.Fatal("a service that does not read zuhause got the address in its hook")
	}
}
