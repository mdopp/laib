package apply

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/plan"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

// TestRunWritesReloadsRestartsAndVerifies swaps the systemctl seam: units
// report active, so the result is green without a box.
func TestRunWritesReloadsRestartsAndVerifies(t *testing.T) {
	st, err := state.Load("../../testdata/golden/basic/state")
	if err != nil {
		t.Fatal(err)
	}
	// Health URLs would hit the network; strip them for this test.
	for _, s := range st.Services {
		s.Contract.Health.HTTP = ""
	}
	home := t.TempDir()
	repo := filepath.Join(home, "state")
	copyTree(t, "../../testdata/golden/basic/state", repo)
	paths := boxfs.Paths{Home: home, State: repo, Quadlet: filepath.Join(home, "q"), Render: filepath.Join(home, "r"), Lock: filepath.Join(home, "lock")}

	var calls []string
	sysd.Run = func(_ context.Context, name string, args ...string) (string, error) {
		line := name + " " + strings.Join(args, " ")
		calls = append(calls, line)
		switch {
		case name == "git":
			return runGit(t, args...)
		case strings.Contains(line, "is-active"):
			return "active", nil
		}
		return "", nil
	}
	hook := filepath.Join(repo, "services", "whoami", "post-deploy")
	os.WriteFile(hook, []byte("#!/bin/sh\necho hello $LAIB_SERVICE\necho \"laib-secret: WHOAMI_API=abc123\"\n"), 0o755)
	// hooks run in the hooks image on a box; here the spec is executed directly
	var specs []HookSpec
	realHook := RunHook
	t.Cleanup(func() { RunHook = realHook })
	var phases []string // "<phase> after <n> systemctl starts": pre-start must see none
	RunHook = func(ctx context.Context, h HookSpec) (string, error) {
		specs = append(specs, h)
		envb, _ := os.ReadFile(h.EnvFile)
		phase := regexp.MustCompile(`LAIB_PHASE=(\S+)`).FindStringSubmatch(string(envb))
		starts := 0
		for _, c := range calls {
			if strings.Contains(c, " start ") || strings.Contains(c, " restart ") {
				starts++
			}
		}
		phases = append(phases, fmt.Sprintf("%s after %d starts", phase[1], starts))
		return runSpecLocally(t, h)
	}
	pre := filepath.Join(repo, "services", "whoami", "pre-start")
	os.WriteFile(pre, []byte("#!/bin/sh\necho pre $LAIB_PHASE\nmkdir -p $LAIB_DATA_DIR/whoami/cfg && echo made > $LAIB_DATA_DIR/whoami/cfg/by-pre-start\n"), 0o755)
	st.Services["whoami"].Dir = filepath.Join(repo, "services", "whoami")
	st.Box.DataDir = filepath.Join(home, "data") // apply creates the declared data paths; the fixture's /mnt/data is not writable here
	p, err := plan.Compute(context.Background(), st, nil, paths, "")
	if err != nil {
		t.Fatal(err)
	}
	res, err := Run(context.Background(), st, p, paths, nil, "first apply")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Green || res.Commit == "" {
		t.Fatalf("%+v", res)
	}
	if len(specs) != 2 || specs[1].Image != DefaultHooksImage || !strings.HasSuffix(specs[1].Command[0], "/post-deploy") {
		t.Fatalf("the hooks did not run in the hooks image: %+v", specs)
	}
	if phases[0] != "pre-start after 0 starts" || !strings.HasPrefix(phases[1], "post-deploy after ") || phases[1] == "post-deploy after 0 starts" {
		t.Fatalf("pre-start must run before the first start, post-deploy after it: %v", phases)
	}
	if _, err := os.Stat(filepath.Join(st.Box.DataDir, "whoami", "cfg", "by-pre-start")); err != nil {
		t.Fatal("pre-start did not write into the service's data path")
	}
	for _, m := range specs[0].Mounts {
		if strings.HasPrefix(m, st.Services["whoami"].Dir) && !strings.HasSuffix(m, ":ro") {
			t.Fatalf("the service folder is mounted writable: %v", specs[0].Mounts)
		}
	}
	if _, err := os.Stat(specs[0].EnvFile); err == nil {
		t.Fatal("the env file with the secrets was left behind")
	}
	if res.NewSecrets["WHOAMI_API"] != "abc123" {
		t.Fatalf("hook secret not captured: %+v", res.NewSecrets)
	}
	hookRan := false
	for _, c := range res.Checks {
		if c.What == "post-deploy" && c.Service == "whoami" && c.OK && strings.Contains(c.Detail, "hello whoami") {
			hookRan = true
		}
	}
	if !hookRan {
		t.Fatalf("hook check missing: %+v", res.Checks)
	}
	if _, err := os.Stat(filepath.Join(paths.Quadlet, "whoami", "whoami.container")); err != nil {
		t.Fatal("unit not written")
	}
	joined := strings.Join(calls, "\n")
	for _, want := range []string{"daemon-reload", "restart caddy.service", "restart auth-pod.service", "restart whoami.service"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(joined, "restart auth-authelia.service") || strings.Contains(joined, "restart auth-lldap.service") {
		t.Error("a pod's containers are restarted through the pod unit, never one by one (--exit-policy stop)")
	}
	// Second run: nothing to do, still green, no restarts.
	calls = nil
	p2, _ := plan.Compute(context.Background(), st, nil, paths, "")
	if !p2.Empty() {
		t.Fatalf("second plan not empty: %+v", p2.Changes)
	}
}

func TestLockIsExclusive(t *testing.T) {
	paths := boxfs.Paths{Lock: filepath.Join(t.TempDir(), "lock")}
	unlock, err := Lock(paths, "apply:test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Lock(paths, "apply:other"); err != ErrLocked {
		t.Fatal("second lock succeeded")
	}
	if LockHolder(paths) != "apply:test" {
		t.Fatal(LockHolder(paths))
	}
	unlock()
	if LockHolder(paths) != "" {
		t.Fatal("lock not released")
	}
}

func runGit(t *testing.T, args ...string) (string, error) {
	t.Helper()
	// Real git in a temp repo; the seam only records that it was called.
	out, err := realRun(args...)
	return out, err
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, _ := os.ReadFile(p)
		return os.WriteFile(target, b, 0o644)
	})
}

// runSpecLocally executes a hook spec on this machine: the env file as
// environment, the hook as the command — what the hooks image does.
func runSpecLocally(t *testing.T, h HookSpec) (string, error) {
	t.Helper()
	envb, err := os.ReadFile(h.EnvFile)
	if err != nil {
		return "", err
	}
	cmd := exec.Command(h.Command[0], h.Command[1:]...)
	cmd.Dir = h.Dir
	cmd.Env = append(os.Environ(), strings.Split(strings.TrimSpace(string(envb)), "\n")...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// A hook that is on disk but not executable is a red check, never a
// silent skip: that is how Authelia started without its configuration.yml
// on the first VM run of #70.
func TestANonExecutableHookIsRed(t *testing.T) {
	svc := &state.Service{Name: "x", Dir: t.TempDir()}
	os.WriteFile(filepath.Join(svc.Dir, "pre-start"), []byte("#!/bin/sh\n"), 0o644)
	c, ran := runHook(context.Background(), &state.State{Box: state.Box{DataDir: t.TempDir()}}, svc, boxfs.Paths{Home: t.TempDir()}, "pre-start", nil, map[string]string{})
	if !ran || c.OK || !strings.Contains(c.Detail, "not executable") {
		t.Fatalf("ran=%v check=%+v", ran, c)
	}
	if _, ran := runHook(context.Background(), &state.State{}, svc, boxfs.Paths{}, "post-deploy", nil, nil); ran {
		t.Fatal("no file, nothing to run")
	}
}

// A pod service restarts through its pod unit alone: the containers are
// bound to it, and a single container's restart can stop the pod
// (--exit-policy stop) under the others.
func TestPodServiceRestartsThePodUnitOnly(t *testing.T) {
	got := PodRestartSet([]string{"auth-authelia.service", "auth-lldap.service", "auth-pod.service"})
	if len(got) != 1 || got[0] != "auth-pod.service" {
		t.Fatalf("%v", got)
	}
	if got := PodRestartSet([]string{"whoami.service"}); len(got) != 1 || got[0] != "whoami.service" {
		t.Fatalf("%v", got)
	}
}

// A role holder's rendered files (authelia/*.yml, the Caddyfile, the
// rewrites) belong to no quadlet service, but their change is the role
// holder's business: its pre-start must run again — Authelia's
// configuration.yml carries the OIDC block only once a client exists
// (#36, VM run 38161974424).
func TestHookTargetsIncludeRoleHoldersOfChangedRenderFiles(t *testing.T) {
	st := &state.State{Box: state.Box{Roles: state.Roles{Proxy: "caddy", SSO: "auth", DNS: "adguard"}}}
	p := &plan.Plan{Services: []string{"whoami"}, Changes: []plan.Change{{Path: "authelia/laib-clients.yml"}, {Path: "quadlet/whoami/whoami.container"}}}
	got := hookTargets(st, p)
	if len(got) != 2 || got[0] != "whoami" || got[1] != "auth" {
		t.Fatalf("%v", got)
	}
}
