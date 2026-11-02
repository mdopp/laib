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
)

// laib#1009: a service declares a secret as a service account's password
// (account:), and the identity service seeds that account. Its hooks get
// each account's password, run again whenever identity/accounts.json
// changes, and no other service's hook sees one.
func TestTheIdentityServiceGetsTheAccountsPasswordsAndRunsAgainOnAChange(t *testing.T) {
	home := t.TempDir()
	paths := boxfs.Paths{Home: home, Render: filepath.Join(home, "r")}
	st := &state.State{Box: state.Box{Domain: "x.home", DataDir: filepath.Join(home, "d"), Roles: state.Roles{SSO: "auth", Identity: "auth"}}, Services: map[string]*state.Service{}}
	for _, name := range []string{"auth", "archiv-anna", "haus"} {
		dir := filepath.Join(home, "services", name)
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "post-deploy"), []byte("#!/bin/sh\n"), 0o755)
		st.Services[name] = &state.Service{Name: name, Dir: dir}
	}
	st.Services["archiv-anna"].Contract.Secrets = []state.Secret{{Name: "ARCHIV_RADICALE_PASSWORD_ANNA", Account: "archiv-anna"}}
	secrets := map[string]string{"ARCHIV_RADICALE_PASSWORD_ANNA": "pw-v1009"}
	hookEnv := func(name string) map[string]string {
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
		if c, ran := runHook(context.Background(), st, st.Services[name], paths, "post-deploy", secrets, map[string]string{}); !ran || !c.OK {
			t.Fatalf("%s's hook did not run green: %+v", name, c)
		}
		return env
	}
	if got := hookEnv("auth")["LAIB_SECRET_ARCHIV_RADICALE_PASSWORD_ANNA"]; got != "pw-v1009" {
		t.Errorf("the identity service's hook gets the account's password: %q", got)
	}
	if _, ok := hookEnv("haus")["LAIB_SECRET_ARCHIV_RADICALE_PASSWORD_ANNA"]; ok {
		t.Error("another service's hook sees the account's password")
	}
	p := &plan.Plan{Changes: []plan.Change{{Path: "identity/accounts.json"}}}
	if got := hookTargets(st, p); len(got) != 1 || got[0] != "auth" {
		t.Errorf("a changed account list runs the identity service's hooks: %v", got)
	}
	if d := st.Depends("archiv-anna"); len(d) != 1 || d[0] != "auth" {
		t.Errorf("a service with an account depends on the identity service: %v", d)
	}
}
