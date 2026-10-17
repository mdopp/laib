package main

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/backup"
	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/converge"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/pages"
	"github.com/mdopp/laib/internal/run"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

func runGitCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	out, err := exec.Command("git", args...).CombinedOutput()
	return string(out), err
}

// cbox is a box for converge tests: the golden state applied once through
// the sysd shim (so the plan is empty), every unit "active" unless the
// test says otherwise, every systemctl call recorded.
type cbox struct {
	env    *cliEnv
	calls  []string
	down   map[string]bool // units reported inactive
	onStop func()          // runs on every `systemctl --user stop`
}

func newCbox(t *testing.T) *cbox {
	t.Helper()
	home := t.TempDir()
	paths := boxfs.Paths{Home: home, State: filepath.Join(home, "state"), Journal: filepath.Join(home, "journal"), Render: filepath.Join(home, "render"),
		Quadlet: filepath.Join(home, "quadlet"), Systemd: filepath.Join(home, "systemd"), Key: filepath.Join(home, "age.key"), Secrets: filepath.Join(home, "state", "secrets.age"), Lock: filepath.Join(home, "apply.lock")}
	c := &cbox{env: &cliEnv{Paths: paths, Actor: "human:test"}, down: map[string]bool{}}
	real := sysd.Run
	t.Cleanup(func() { sysd.Run = real })
	podSecrets := map[string]string{} // the box's podman secrets (#224)
	sysd.Run = func(ctx context.Context, name string, args ...string) (string, error) {
		line := name + " " + strings.Join(args, " ")
		c.calls = append(c.calls, line)
		switch {
		case name == "git":
			return runGitCmd(t, args...)
		case strings.HasPrefix(line, "podman secret create"):
			podSecrets[args[len(args)-2]] = sysd.Stdin(ctx) // create [--replace] <name> -
		case strings.HasPrefix(line, "podman secret rm"):
			delete(podSecrets, args[2])
		case strings.HasPrefix(line, "podman secret ls"):
			return strings.Join(slices.Sorted(maps.Keys(podSecrets)), "\n"), nil
		case strings.HasPrefix(line, "podman secret inspect"):
			var out []string
			for _, n := range args[5:] {
				v, _ := json.Marshal(podSecrets[n])
				out = append(out, n+" "+string(v))
			}
			return strings.Join(out, "\n"), nil
		case strings.Contains(line, "is-active"):
			if c.down[args[len(args)-1]] {
				return "inactive", nil
			}
			return "active", nil
		case strings.Contains(line, "systemctl --user restart "):
			delete(c.down, args[len(args)-1]) // a restarted unit comes up
		case strings.Contains(line, "systemctl --user stop") && c.onStop != nil:
			c.onStop()
		case strings.Contains(line, "nft list table"):
			return "table inet laib {}", nil
		}
		return "", nil
	}
	if rc := cmdInit(context.Background(), c.env, []string{"--domain", "e2e.home", "--data-dir", filepath.Join(home, "data")}); rc != cli.ExitOK {
		t.Fatalf("init rc %d", rc)
	}
	golden := filepath.Join("..", "..", "testdata", "golden", "basic", "state")
	copyDir(t, filepath.Join(golden, "services"), filepath.Join(paths.State, "services"))
	// the golden box.yml (roles, carve-out) with this box's data dir, as scripts/e2e.sh does
	for _, f := range []string{"box.yml", "policy.yml"} {
		b, _ := os.ReadFile(filepath.Join(golden, f))
		text := regexp.MustCompile(`(?m)^dataDir: .*$`).ReplaceAllString(string(b), "dataDir: "+filepath.Join(home, "data"))
		os.WriteFile(filepath.Join(paths.State, f), []byte(text), 0o644)
	}
	// nothing listens here: no HTTP health probes
	files, _ := filepath.Glob(filepath.Join(paths.State, "services", "*", "laib.yml"))
	for _, f := range files {
		b, _ := os.ReadFile(f)
		os.WriteFile(f, []byte(regexp.MustCompile(`(?m)^\s+http: .*\n`).ReplaceAllString(string(b), "")), 0o644)
	}
	sec, _ := secrets.Load(paths.Secrets, paths.Key)
	if sec == nil {
		sec = map[string]string{}
	}
	sec["DNS_API_TOKEN"] = "x"
	secrets.Save(paths.Secrets, paths.Key, sec)
	st, err := cli.LoadState(c.env)
	if err != nil {
		t.Fatal(err)
	}
	if rc := run.Apply(context.Background(), c.env, st, "", "test: first apply"); rc != cli.ExitOK {
		t.Fatalf("first apply rc %d", rc)
	}
	c.calls = nil
	return c
}

func (c *cbox) restarts() []string {
	var out []string
	for _, l := range c.calls {
		if strings.Contains(l, "systemctl --user restart ") {
			out = append(out, strings.TrimPrefix(l, "systemctl --user restart "))
		}
	}
	return out
}

func (c *cbox) journalHas(t *testing.T, want string) bool {
	t.Helper()
	es, _ := journal.Read(c.env.Paths.Journal, time.Now().Add(-time.Hour))
	for _, e := range es {
		if strings.Contains(e.Detail, want) || strings.Contains(e.Reason, want) {
			return true
		}
	}
	return false
}

func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if info.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, _ := os.ReadFile(p)
		return os.WriteFile(filepath.Join(dst, rel), b, info.Mode())
	})
}

// #39 (1): converge never runs beside another apply; a lock someone else
// holds means this tick is skipped, journaled, and nothing is restarted.
func TestConvergeSkipsWhenSomeoneHoldsTheLock(t *testing.T) {
	c := newCbox(t)
	c.down["whoami.service"] = true
	unlock, _, err := apply.Lock(c.env.Paths, "restore:human:core")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if rc := converge.Converge(context.Background(), c.env, []string{"--force"}); rc != cli.ExitOK {
		t.Fatalf("a locked tick is skipped, not red: rc %d", rc)
	}
	if len(c.restarts()) > 0 {
		t.Fatalf("converge restarted units under a foreign lock: %v", c.restarts())
	}
	if !c.journalHas(t, "übersprungen") || !c.journalHas(t, "restore:human:core") {
		t.Fatal("the skip and the holder must be in the journal")
	}
}

// #39 (3): a paused service is left alone — a human's stop, a restore in
// progress, the lease's full window.
func TestConvergeLeavesAPausedServiceAlone(t *testing.T) {
	c := newCbox(t)
	c.down["whoami.service"] = true
	if rc := cmdPause(context.Background(), c.env, []string{"whoami", "--until", "2h", "wartung"}); rc != cli.ExitOK {
		t.Fatalf("pause rc %d", rc)
	}
	if rc := converge.Converge(context.Background(), c.env, []string{"--force"}); rc != cli.ExitOK {
		t.Fatalf("rc %d", rc)
	}
	for _, u := range c.restarts() {
		if strings.HasPrefix(u, "whoami") {
			t.Fatalf("a paused service was restarted: %v", c.restarts())
		}
	}
	if !c.journalHas(t, "pausiert") {
		t.Fatal("the journal must say the service is paused")
	}
	st, _ := cli.LoadState(c.env)
	r, rows := pages.CollectStatus(context.Background(), c.env, st)
	var paused bool
	for _, row := range rows {
		if row.Name == "whoami" && row.State == "pausiert" {
			paused = true
		}
	}
	if !paused || r.Red != 0 {
		t.Fatalf("readiness shows a paused service as pausiert, not rot: %+v %+v", r, rows)
	}
	if rc := cmdResume(context.Background(), c.env, []string{"whoami"}); rc != cli.ExitOK {
		t.Fatalf("resume rc %d", rc)
	}
	c.calls = nil
	converge.Converge(context.Background(), c.env, []string{"--force"})
	if len(c.restarts()) == 0 {
		t.Fatal("after resume converge restarts the inactive unit again")
	}
}

// #39 (3): restart-only runs are converge runs; the fourth in an hour is
// refused, like a run with drift.
func TestRestartOnlyConvergeRunsCountAgainstTheBudget(t *testing.T) {
	c := newCbox(t)
	c.down["whoami.service"] = true
	for i := 0; i < 3; i++ {
		c.calls = nil
		c.down["whoami.service"] = true // down again: a flapping unit
		converge.Converge(context.Background(), c.env, []string{"--force"})
		if len(c.restarts()) == 0 {
			t.Fatalf("run %d did not restart", i+1)
		}
	}
	c.calls = nil
	c.down["whoami.service"] = true
	if rc := converge.Converge(context.Background(), c.env, []string{"--force"}); rc != cli.ExitRed {
		t.Fatalf("the fourth restart-only run must exhaust the budget: rc %d", rc)
	}
	if len(c.restarts()) > 0 || !c.journalHas(t, "Budget erschöpft") {
		t.Fatalf("restarted anyway: %v", c.restarts())
	}
}

// #39 (5): an apply killed after writing files but before restart left the
// old config running and a clean-looking plan. The pending marker makes
// the next converge finish the activation.
func TestConvergeCompletesAKilledApply(t *testing.T) {
	c := newCbox(t)
	if err := apply.WritePending(c.env.Paths, []string{"whoami"}, "test: killed apply"); err != nil {
		t.Fatal(err)
	}
	if rc := converge.Converge(context.Background(), c.env, []string{"--force"}); rc != cli.ExitOK {
		t.Fatalf("rc %d", rc)
	}
	var hit bool
	for _, u := range c.restarts() {
		if u == "whoami.service" {
			hit = true
		}
	}
	if !hit {
		t.Fatalf("whoami not restarted although its activation was pending: %v", c.calls)
	}
	if len(apply.Pending(c.env.Paths)) != 0 {
		t.Fatal("the pending marker must be cleared after verify")
	}
}

// #39 (2): restore holds the lock from the first stop to the final verify.
func TestRestoreHoldsTheLockFromStopToVerify(t *testing.T) {
	c := newCbox(t)
	realRestic := backup.ResticRun
	t.Cleanup(func() { backup.ResticRun = realRestic })
	backup.ResticRun = func(_ context.Context, _ *state.State, _ string, args ...string) (string, error) {
		if args[0] == "snapshots" {
			return `[{"id":"abcdef0123456789"}]`, nil
		}
		if args[0] == "cat" { // the golden repo is remote and not there yet
			return "Fatal: repository does not exist", errors.New("exit status 1")
		}
		return "", nil
	}
	var lockedAtStop []string
	c.onStop = func() { lockedAtStop = append(lockedAtStop, apply.LockHolder(c.env.Paths)) }
	b, _ := os.ReadFile(filepath.Join(c.env.Paths.State, "box.yml"))
	if !strings.Contains(string(b), "backup:") {
		t.Fatal("init writes a backup repo")
	}
	if rc := backup.Restore(context.Background(), c.env, []string{"latest", "caddy"}); rc != cli.ExitOK { // caddy declares a data path
		t.Fatalf("restore rc %d", rc)
	}
	if len(lockedAtStop) == 0 {
		t.Fatal("restore did not stop the service's units")
	}
	for _, h := range lockedAtStop {
		if !strings.HasPrefix(h, "restore:") {
			t.Fatalf("the lock was not held at stop time: %q", h)
		}
	}
	if apply.LockHolder(c.env.Paths) != "" {
		t.Fatal("the lock must be released after restore")
	}
	if len(apply.Paused(c.env.Paths)) != 0 {
		t.Fatal("restore's own pause must be lifted when it is done")
	}
}

// #39 (4): a lock whose process is gone is taken over, and the journal
// says so.
func TestAStaleLockIsTakenOverWithAJournalLine(t *testing.T) {
	c := newCbox(t)
	os.WriteFile(c.env.Paths.Lock, []byte("apply:human:core pid 999999999 since 2026-10-11T10:00:00Z"), 0o644)
	st, _ := cli.LoadState(c.env)
	if rc := run.Apply(context.Background(), c.env, st, "", "after a crash"); rc != cli.ExitOK {
		t.Fatalf("rc %d", rc)
	}
	if !c.journalHas(t, "übernommen") || !c.journalHas(t, "999999999") {
		t.Fatal("the takeover of the stale lock must be journaled with the old holder")
	}
}
