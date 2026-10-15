package apply

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

// #214: a `laib-exec:` line becomes `podman exec <container> <words…>`, and
// ps shows that argv on the box to every local user. A line that carries a
// secret is refused: podman never sees it, the hook is red, and the check
// that says so names the secret, never its value.
func TestLaibExecNeverPutsASecretOnArgv(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "services", "auth")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "post-deploy"), []byte("#!/bin/sh\n"), 0o755)
	const pw = "-6op_pw-generated"
	real, realHook := sysd.Run, RunHook
	t.Cleanup(func() { sysd.Run, RunHook = real, realHook })
	var argvs []string
	sysd.Run = func(_ context.Context, name string, args ...string) (string, error) {
		argvs = append(argvs, name+" "+strings.Join(args, " "))
		return "", nil
	}
	RunHook = func(context.Context, HookSpec) (string, error) {
		return "laib-exec: systemd-auth-lldap /app/lldap_set_password --username=core --password=" + pw + "\n" +
			"laib-exec: systemd-auth-lldap /app/lldap_set_password --username=core\n", nil
	}
	st := &state.State{Box: state.Box{Domain: "x.home", DataDir: filepath.Join(home, "d")}, Services: map[string]*state.Service{
		"auth": {Name: "auth", Dir: dir, Units: map[string]string{"auth-lldap.container": "[Container]\n[Install]\n"}},
	}}
	c, ran := runHook(context.Background(), st, st.Services["auth"], boxfs.Paths{Home: home, Render: filepath.Join(home, "r")},
		"post-deploy", map[string]string{"OPERATOR_PASSWORD": pw}, map[string]string{})
	if !ran || c.OK {
		t.Fatalf("a laib-exec line with a secret must make the hook red: %+v", c)
	}
	for _, a := range argvs {
		if strings.Contains(a, pw) {
			t.Fatalf("a secret went onto podman's argv, where ps shows it: %s", a)
		}
	}
	if strings.Contains(c.Detail, pw) || !strings.Contains(c.Detail, "OPERATOR_PASSWORD") {
		t.Fatalf("the check must name the secret, never show it: %s", c.Detail)
	}
	if len(argvs) != 1 || !strings.HasPrefix(argvs[0], "podman exec systemd-auth-lldap ") {
		t.Fatalf("a line without a secret still runs, once: %v", argvs)
	}
}
