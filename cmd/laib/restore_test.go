package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

// rbox is a box for restore tests: the golden state applied once through
// the sysd shim (so the next plan is empty), every unit active until it
// is stopped, inactive until it is started or restarted again, every
// systemctl call recorded.
type rbox struct {
	env   *cliEnv
	calls []string
	down  map[string]bool
}

func newRbox(t *testing.T) *rbox {
	t.Helper()
	home := t.TempDir()
	paths := boxfs.Paths{Home: home, State: filepath.Join(home, "state"), Journal: filepath.Join(home, "journal"), Render: filepath.Join(home, "render"),
		Quadlet: filepath.Join(home, "quadlet"), Systemd: filepath.Join(home, "systemd"), Key: filepath.Join(home, "age.key"), Secrets: filepath.Join(home, "state", "secrets.age"), Lock: filepath.Join(home, "apply.lock")}
	b := &rbox{env: &cliEnv{Paths: paths, Actor: "human:test"}, down: map[string]bool{}}
	real := sysd.Run
	t.Cleanup(func() { sysd.Run = real })
	sysd.Run = func(_ context.Context, name string, args ...string) (string, error) {
		line := name + " " + strings.Join(args, " ")
		b.calls = append(b.calls, line)
		unit := ""
		if len(args) > 0 {
			unit = args[len(args)-1]
		}
		switch {
		case name == "git":
			out, err := exec.Command("git", args...).CombinedOutput()
			return string(out), err
		case strings.Contains(line, "is-active"):
			if b.down[unit] {
				return "inactive", nil
			}
			return "active", nil
		case strings.Contains(line, "systemctl --user stop "):
			b.down[unit] = true
		case strings.Contains(line, "systemctl --user start "), strings.Contains(line, "systemctl --user restart "):
			delete(b.down, unit)
		case strings.Contains(line, "nft list table"):
			return "table inet laib {}", nil
		}
		return "", nil
	}
	if rc := cmdInit(context.Background(), b.env, []string{"--domain", "e2e.home", "--data-dir", filepath.Join(home, "data")}); rc != cli.ExitOK {
		t.Fatalf("init rc %d", rc)
	}
	golden := filepath.Join("..", "..", "testdata", "golden", "basic", "state")
	copyGolden(t, filepath.Join(golden, "services"), filepath.Join(paths.State, "services"))
	for _, f := range []string{"box.yml", "policy.yml"} {
		raw, _ := os.ReadFile(filepath.Join(golden, f))
		text := regexp.MustCompile(`(?m)^dataDir: .*$`).ReplaceAllString(string(raw), "dataDir: "+filepath.Join(home, "data"))
		os.WriteFile(filepath.Join(paths.State, f), []byte(text), 0o644)
	}
	// nothing listens here: no HTTP health probes
	files, _ := filepath.Glob(filepath.Join(paths.State, "services", "*", "laib.yml"))
	for _, f := range files {
		raw, _ := os.ReadFile(f)
		os.WriteFile(f, []byte(regexp.MustCompile(`(?m)^\s+http: .*\n`).ReplaceAllString(string(raw), "")), 0o644)
	}
	sec, _ := secrets.Load(paths.Secrets, paths.Key)
	if sec == nil {
		sec = map[string]string{}
	}
	sec["DNS_API_TOKEN"] = "x"
	sec["RESTIC_PASSWORD"] = "pw" // the repo exists after the first backup, and so does its password (#40)
	secrets.Save(paths.Secrets, paths.Key, sec)
	st, err := cli.LoadState(b.env)
	if err != nil {
		t.Fatal(err)
	}
	if rc := runApply(context.Background(), b.env, st, "", "test: first apply"); rc != cli.ExitOK {
		t.Fatalf("first apply rc %d", rc)
	}
	b.calls = nil
	return b
}

func copyGolden(t *testing.T, src, dst string) {
	t.Helper()
	filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if info.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		raw, _ := os.ReadFile(p)
		return os.WriteFile(filepath.Join(dst, rel), raw, info.Mode())
	})
}

// VM run 11 of #70: `laib restore latest caddy` stopped caddy.service,
// replaced its data, and then ran an apply whose plan was empty — nothing
// restarted, verify waited 60 s for a unit nobody started, the restore
// ended red. Earlier runs passed only because the converge timer fired
// inside that minute. A restore starts what it stopped.
func TestRestoreStartsWhatItStopped(t *testing.T) {
	b := newRbox(t)
	realRestic := resticRun
	t.Cleanup(func() { resticRun = realRestic })
	resticRun = func(_ context.Context, _ *state.State, _ string, args ...string) (string, error) {
		if args[0] == "snapshots" {
			return `[{"id":"abcdef0123456789"}]`, nil
		}
		return "", nil
	}
	if rc := cmdRestore(context.Background(), b.env, []string{"latest", "caddy"}); rc != cli.ExitOK {
		t.Fatalf("restore rc %d: %s", rc, strings.Join(b.calls, "\n"))
	}
	stopped, started := -1, -1
	for i, l := range b.calls {
		switch {
		case l == "systemctl --user stop caddy.service":
			stopped = i
		case l == "systemctl --user start caddy.service" || l == "systemctl --user restart caddy.service":
			if started < 0 {
				started = i
			}
		}
	}
	if stopped < 0 {
		t.Fatalf("restore did not stop caddy.service:\n%s", strings.Join(b.calls, "\n"))
	}
	if started < stopped {
		t.Fatalf("restore stopped caddy.service and never started it again (plan was empty):\n%s", strings.Join(b.calls, "\n"))
	}
	if b.down["caddy.service"] {
		t.Fatal("caddy.service is still down after the restore")
	}
}
