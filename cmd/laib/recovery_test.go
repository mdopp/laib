package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/backup"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/diag"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/state"
)

// resticRecorder swaps the restic seam: every call is recorded with the
// lock holder at that moment; snapshots answers one id.
func resticRecorder(t *testing.T, c *cbox) *[]string {
	t.Helper()
	real := backup.ResticRun
	t.Cleanup(func() { backup.ResticRun = real })
	var calls []string
	backup.ResticRun = func(_ context.Context, _ *state.State, _ string, args ...string) (string, error) {
		calls = append(calls, "restic "+strings.Join(args, " ")+" [lock="+apply.LockHolder(c.env.Paths)+"]")
		if args[0] == "snapshots" {
			return `[{"id":"abcdef0123456789"}]`, nil
		}
		if args[0] == "cat" { // the golden repo is remote and not there yet
			return "Fatal: repository does not exist", errors.New("exit status 1")
		}
		return "", nil
	}
	return &calls
}

func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	f()
	w.Close()
	os.Stdout = old
	var b bytes.Buffer
	io.Copy(&b, r)
	return b.String()
}

// #40 (4): an existing repo has a password; inventing a new one would make
// a restore fail with "wrong password" instead of asking for the old one.
func TestBackupPrepRefusesToInventAPasswordForAnExistingRepo(t *testing.T) {
	c := newCbox(t)
	repo := filepath.Join(t.TempDir(), "restic")
	os.MkdirAll(repo, 0o755)
	os.WriteFile(filepath.Join(repo, "config"), []byte("x"), 0o644)
	b, _ := os.ReadFile(filepath.Join(c.env.Paths.State, "box.yml"))
	os.WriteFile(filepath.Join(c.env.Paths.State, "box.yml"), []byte(strings.Replace(string(b), "backup:\n  repo: ", "backup:\n  repo: "+repo+" #", 1)), 0o644)
	sec, _ := secrets.Load(c.env.Paths.Secrets, c.env.Paths.Key)
	delete(sec, "RESTIC_PASSWORD")
	secrets.Save(c.env.Paths.Secrets, c.env.Paths.Key, sec)
	if _, _, err := backup.Prep(c.env); err == nil || !strings.Contains(err.Error(), "RESTIC_PASSWORD") {
		t.Fatalf("a repo without the password in secrets must ask, not invent: %v", err)
	}
	t.Setenv("RESTIC_PASSWORD", "from-the-sheet")
	if _, pw, err := backup.Prep(c.env); err != nil || pw != "from-the-sheet" {
		t.Fatalf("the password from the sheet (RESTIC_PASSWORD) must be used: %q %v", pw, err)
	}
}

// #40 (6): a declared path with quiesce: true is backed up with its
// service stopped (stop, snapshot, start), under the lock, paused for
// converge meanwhile.
func TestBackupQuiescesDeclaredPaths(t *testing.T) {
	c := newCbox(t)
	f := filepath.Join(c.env.Paths.State, "services", "caddy", "laib.yml")
	b, _ := os.ReadFile(f)
	text := strings.Replace(string(b), "  - path: ${DATA_DIR}/caddy/data\n", "  - path: ${DATA_DIR}/caddy/data\n    quiesce: true\n", 1)
	if text == string(b) {
		t.Fatal("fixture: caddy data path not found")
	}
	os.WriteFile(f, []byte(text), 0o644)
	calls := resticRecorder(t, c)
	c.calls = nil
	var console bytes.Buffer
	c.env.Out = &console
	if rc := backup.Cmd(context.Background(), c.env, nil); rc != cli.ExitOK {
		t.Fatalf("backup rc %d", rc)
	}
	// the human at the console sees that the database was stopped for the snapshot
	if !strings.Contains(console.String(), "angehalten für den Snapshot: caddy") {
		t.Fatalf("the output does not say what was stopped: %q", console.String())
	}
	all := append([]string{}, c.calls...)
	all = append(all, *calls...)
	joined := strings.Join(c.calls, "\n")
	stop := strings.Index(joined, "systemctl --user stop caddy.service")
	start := strings.Index(joined, "systemctl --user start caddy.service")
	if stop < 0 || start < 0 || stop > start {
		t.Fatalf("caddy must be stopped before and started after the snapshot:\n%s", joined)
	}
	var backup string
	for _, l := range *calls {
		if strings.HasPrefix(l, "restic backup") {
			backup = l
		}
	}
	if backup == "" || strings.Contains(backup, "[lock=]") {
		t.Fatalf("the snapshot must run under the lock: %q", backup)
	}
	if len(apply.Paused(c.env.Paths)) != 0 || apply.LockHolder(c.env.Paths) != "" {
		t.Fatal("pause and lock must be gone after the backup")
	}
	_ = all
}

// #40 (1, 5): laib restore --box <snapshot> restores the state repo and the
// journal first, then every service in dependency order with its units
// stopped and its paths replaced (--delete), then one apply — all under
// the lock.
func TestRestoreBoxRestoresStateThenEveryServiceWithDelete(t *testing.T) {
	c := newCbox(t)
	calls := resticRecorder(t, c)
	c.calls = nil
	if rc := backup.Restore(context.Background(), c.env, []string{"--box", "latest"}); rc != cli.ExitOK {
		t.Fatalf("restore --box rc %d", rc)
	}
	var restores []string
	for _, l := range *calls {
		if strings.HasPrefix(l, "restic restore ") {
			if !strings.Contains(l, "--delete") {
				t.Fatalf("a restore must replace, not overlay: %s", l)
			}
			if strings.Contains(l, "[lock=]") {
				t.Fatalf("restore outside the lock: %s", l)
			}
			restores = append(restores, l)
		}
	}
	if len(restores) < 3 || !strings.Contains(restores[0], c.env.Paths.State) {
		t.Fatalf("state first, then the services: %v", restores)
	}
	// every service with data (auth: three paths, caddy: one, usage-metrics: one; whoami declares none)
	order := strings.Join(restores[2:], "\n")
	if strings.Count(order, "restic restore") != 5 || !strings.Contains(order, "/auth/lldap") || !strings.Contains(order, "/caddy/data") || !strings.Contains(order, "/usage-metrics") {
		t.Fatalf("every declared path of every service, each with --delete: %v", restores)
	}
	joined := strings.Join(c.calls, "\n")
	if !strings.Contains(joined, "systemctl --user stop caddy.service") || !strings.Contains(joined, "daemon-reload") {
		t.Fatalf("units stopped and one apply at the end:\n%s", joined)
	}
	if apply.LockHolder(c.env.Paths) != "" || len(apply.Paused(c.env.Paths)) != 0 {
		t.Fatal("lock and pauses released")
	}
	if rc := backup.Restore(context.Background(), c.env, []string{"latest", "caddy"}); rc != cli.ExitOK {
		t.Fatalf("single-service restore rc %d", rc)
	}
	last := (*calls)[len(*calls)-1]
	for _, l := range *calls {
		if strings.HasPrefix(l, "restic restore ") && strings.Contains(l, "/caddy/") && !strings.Contains(l, "--delete") {
			t.Fatalf("single-service restore overlays: %s (last %s)", l, last)
		}
	}
}

// #40 (2, 3): the recovery sheet is the one page a human prints: key,
// repo, password, four steps. Never for a hand, never over MCP.
func TestRecoverySheetIsForHumansAndListsEverything(t *testing.T) {
	c := newCbox(t)
	resticRecorder(t, c)
	if rc := backup.RecoverySheet(context.Background(), &cliEnv{Paths: c.env.Paths, Actor: "ops"}, nil); rc == cli.ExitOK {
		t.Fatal("a hand got the recovery sheet")
	}
	if rc := backup.RecoverySheet(context.Background(), &cliEnv{Paths: c.env.Paths, Actor: "human:core", MCP: true}, nil); rc == cli.ExitOK {
		t.Fatal("the sheet went out over MCP")
	}
	out := captureStdout(t, func() {
		if rc := backup.RecoverySheet(context.Background(), c.env, nil); rc != cli.ExitOK {
			t.Errorf("rc %d", rc)
		}
	})
	keyFile, _ := os.ReadFile(c.env.Paths.Key)
	key := ""
	for _, line := range strings.Split(string(keyFile), "\n") {
		if strings.HasPrefix(line, "AGE-SECRET-KEY-") {
			key = line
		}
	}
	sec, _ := secrets.Load(c.env.Paths.Secrets, c.env.Paths.Key)
	for _, want := range []string{key, sec["RESTIC_PASSWORD"], "restic", "1.", "2.", "3.", "4.", "laib restore --box latest"} {
		if want == "" || !strings.Contains(out, want) {
			t.Fatalf("sheet lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "sftp:nas:/backup/laib") {
		t.Fatalf("sheet lacks the repo:\n%s", out)
	}
	st, _ := cli.LoadState(c.env)
	var found bool
	for _, f := range diag.Probes(context.Background(), c.env, st, "") {
		if f.Probe == "recovery" && f.Severity == "gelb" {
			found = true
		}
	}
	if !found {
		t.Fatal("diag must be yellow until the sheet is confirmed as stored")
	}
	if rc := backup.RecoverySheet(context.Background(), c.env, []string{"--confirm"}); rc != cli.ExitOK {
		t.Fatal("confirm")
	}
	for _, f := range diag.Probes(context.Background(), c.env, st, "") {
		if f.Probe == "recovery" {
			t.Fatalf("still a recovery finding after confirm: %+v", f)
		}
	}
}

// #40 (diag): a week after install without an off-box repo is red, and a
// restore test older than 35 days is yellow.
func TestDiagWantsAnOffBoxRepoAfterAWeek(t *testing.T) {
	c := newCbox(t)
	b, _ := os.ReadFile(filepath.Join(c.env.Paths.State, "box.yml"))
	os.WriteFile(filepath.Join(c.env.Paths.State, "box.yml"), []byte(strings.Replace(string(b), "repo: sftp:nas:/backup/laib", "repo: "+filepath.Join(c.env.Paths.Home, "backup", "restic"), 1)), 0o644)
	os.RemoveAll(c.env.Paths.Journal)
	journal.Append(c.env.Paths.Journal, journal.Entry{T: time.Now().Add(-8 * 24 * time.Hour), Actor: "human:core", Verb: "init", Result: "info", Detail: "state repo created"})
	st, _ := cli.LoadState(c.env)
	var red, test bool
	for _, f := range diag.Probes(context.Background(), c.env, st, "") {
		if f.Probe == "backup" && f.Severity == "rot" && strings.Contains(f.Detail, "Datenplatte") {
			red = true
		}
		if f.Probe == "restore-test" && f.Severity == "gelb" {
			test = true
		}
	}
	if !red {
		t.Fatal("eight days on the data disk alone must be red")
	}
	if !test {
		t.Fatal("no restore test ever run must be yellow")
	}
}
