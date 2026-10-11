package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/state"
)

// resticRecorder swaps the restic seam: every call is recorded with the
// lock holder at that moment; snapshots answers one id.
func resticRecorder(t *testing.T, c *cbox) *[]string {
	t.Helper()
	real := resticRun
	t.Cleanup(func() { resticRun = real })
	var calls []string
	resticRun = func(_ context.Context, _ *state.State, _ string, args ...string) (string, error) {
		calls = append(calls, "restic "+strings.Join(args, " ")+" [lock="+apply.LockHolder(c.env.paths)+"]")
		if args[0] == "snapshots" {
			return `[{"id":"abcdef0123456789"}]`, nil
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
	b, _ := os.ReadFile(filepath.Join(c.env.paths.State, "box.yml"))
	os.WriteFile(filepath.Join(c.env.paths.State, "box.yml"), []byte(strings.Replace(string(b), "backup:\n  repo: ", "backup:\n  repo: "+repo+" #", 1)), 0o644)
	sec, _ := secrets.Load(c.env.paths.Secrets, c.env.paths.Key)
	delete(sec, "RESTIC_PASSWORD")
	secrets.Save(c.env.paths.Secrets, c.env.paths.Key, sec)
	if _, _, err := backupPrep(c.env); err == nil || !strings.Contains(err.Error(), "RESTIC_PASSWORD") {
		t.Fatalf("a repo without the password in secrets must ask, not invent: %v", err)
	}
	t.Setenv("RESTIC_PASSWORD", "from-the-sheet")
	if _, pw, err := backupPrep(c.env); err != nil || pw != "from-the-sheet" {
		t.Fatalf("the password from the sheet (RESTIC_PASSWORD) must be used: %q %v", pw, err)
	}
}

// #40 (6): a declared path with quiesce: true is backed up with its
// service stopped (stop, snapshot, start), under the lock, paused for
// converge meanwhile.
func TestBackupQuiescesDeclaredPaths(t *testing.T) {
	c := newCbox(t)
	f := filepath.Join(c.env.paths.State, "services", "caddy", "laib.yml")
	b, _ := os.ReadFile(f)
	text := strings.Replace(string(b), "  - path: ${DATA_DIR}/caddy/data\n", "  - path: ${DATA_DIR}/caddy/data\n    quiesce: true\n", 1)
	if text == string(b) {
		t.Fatal("fixture: caddy data path not found")
	}
	os.WriteFile(f, []byte(text), 0o644)
	calls := resticRecorder(t, c)
	c.calls = nil
	if rc := cmdBackup(context.Background(), c.env, nil); rc != exitOK {
		t.Fatalf("backup rc %d", rc)
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
	if len(apply.Paused(c.env.paths)) != 0 || apply.LockHolder(c.env.paths) != "" {
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
	if rc := cmdRestore(context.Background(), c.env, []string{"--box", "latest"}); rc != exitOK {
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
	if len(restores) < 3 || !strings.Contains(restores[0], c.env.paths.State) {
		t.Fatalf("state first, then the services: %v", restores)
	}
	var order []string
	for _, l := range restores[2:] {
		order = append(order, l)
	}
	// roles first: caddy (proxy) and auth (sso) before whoami
	wh, cd := -1, -1
	for i, l := range order {
		if strings.Contains(l, "/whoami/") {
			wh = i
		}
		if strings.Contains(l, "/caddy/") {
			cd = i
		}
	}
	if cd < 0 || wh < 0 || cd > wh {
		t.Fatalf("dependency order (caddy before whoami): %v", order)
	}
	joined := strings.Join(c.calls, "\n")
	if !strings.Contains(joined, "systemctl --user stop caddy.service") || !strings.Contains(joined, "daemon-reload") {
		t.Fatalf("units stopped and one apply at the end:\n%s", joined)
	}
	if apply.LockHolder(c.env.paths) != "" || len(apply.Paused(c.env.paths)) != 0 {
		t.Fatal("lock and pauses released")
	}
	if rc := cmdRestore(context.Background(), c.env, []string{"latest", "caddy"}); rc != exitOK {
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
	if rc := cmdRecoverySheet(context.Background(), &cliEnv{paths: c.env.paths, actor: "ops"}, nil); rc == exitOK {
		t.Fatal("a hand got the recovery sheet")
	}
	if rc := cmdRecoverySheet(context.Background(), &cliEnv{paths: c.env.paths, actor: "human:core", mcp: true}, nil); rc == exitOK {
		t.Fatal("the sheet went out over MCP")
	}
	out := captureStdout(t, func() {
		if rc := cmdRecoverySheet(context.Background(), c.env, nil); rc != exitOK {
			t.Errorf("rc %d", rc)
		}
	})
	key, _ := os.ReadFile(c.env.paths.Key)
	sec, _ := secrets.Load(c.env.paths.Secrets, c.env.paths.Key)
	for _, want := range []string{strings.TrimSpace(string(key)), sec["RESTIC_PASSWORD"], "restic", "1.", "2.", "3.", "4.", "laib restore --box latest"} {
		if want == "" || !strings.Contains(out, want) {
			t.Fatalf("sheet lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "backup/restic") {
		t.Fatalf("sheet lacks the repo:\n%s", out)
	}
	st, _ := loadState(c.env)
	var found bool
	for _, f := range runProbes(context.Background(), c.env, st, "") {
		if f.Probe == "recovery" && f.Severity == "gelb" {
			found = true
		}
	}
	if !found {
		t.Fatal("diag must be yellow until the sheet is confirmed as stored")
	}
	if rc := cmdRecoverySheet(context.Background(), c.env, []string{"--confirm"}); rc != exitOK {
		t.Fatal("confirm")
	}
	for _, f := range runProbes(context.Background(), c.env, st, "") {
		if f.Probe == "recovery" {
			t.Fatalf("still a recovery finding after confirm: %+v", f)
		}
	}
}

// #40 (diag): a week after install without an off-box repo is red, and a
// restore test older than 35 days is yellow.
func TestDiagWantsAnOffBoxRepoAfterAWeek(t *testing.T) {
	c := newCbox(t)
	os.RemoveAll(c.env.paths.Journal)
	journal.Append(c.env.paths.Journal, journal.Entry{T: time.Now().Add(-8 * 24 * time.Hour), Actor: "human:core", Verb: "init", Result: "info", Detail: "state repo created"})
	st, _ := loadState(c.env)
	var red, test bool
	for _, f := range runProbes(context.Background(), c.env, st, "") {
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
