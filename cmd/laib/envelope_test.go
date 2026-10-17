package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

// envBox is a box from `laib init` with one service that declares a data
// path. The host is faked at the sysd seam (git runs; systemctl says
// active; everything else is recorded) and restic at its seam.
func envBox(t *testing.T) (env *cliEnv, dataDir string, calls *[]string) {
	t.Helper()
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	env = &cliEnv{Actor: "ops", Paths: boxfs.Paths{
		Home: home, State: filepath.Join(home, "state"), Journal: filepath.Join(home, "journal"), Render: filepath.Join(home, "render"),
		Quadlet: filepath.Join(dir, "quadlet"), Systemd: filepath.Join(dir, "systemd"),
		Key: filepath.Join(home, "age.key"), Secrets: filepath.Join(home, "state", "secrets.age"), Lock: filepath.Join(home, "apply.lock"),
	}}
	var recorded []string
	calls = &recorded
	realRun, realRestic := sysd.Run, resticRun
	t.Cleanup(func() { sysd.Run, resticRun = realRun, realRestic })
	sysd.Run = func(ctx context.Context, name string, args ...string) (string, error) {
		recorded = append(recorded, name+" "+strings.Join(args, " "))
		switch {
		case name == "git":
			return realRun(ctx, name, args...)
		case name == "systemctl" && slices.Contains(args, "is-active"):
			return "active", nil
		}
		return "", nil
	}
	resticRun = func(ctx context.Context, st *state.State, pw string, args ...string) (string, error) {
		recorded = append(recorded, "restic "+strings.Join(args, " "))
		return "snapshot fake saved", nil
	}
	dataDir = filepath.Join(dir, "data")
	os.MkdirAll(filepath.Join(dataDir, "whoami", "cfg"), 0o755)
	human := &cliEnv{Actor: "human:test", Paths: env.Paths}
	if code := cmdInit(context.Background(), human, []string{"--domain", "box.test", "--lan-ip", "10.0.0.2", "--data-dir", dataDir}); code != cli.ExitOK {
		t.Fatalf("init: exit %d", code)
	}
	svc := filepath.Join(env.Paths.State, "services", "whoami")
	os.MkdirAll(svc, 0o755)
	for name, content := range map[string]string{
		"laib.yml":         "name: whoami\ndata:\n  - path: ${DATA_DIR}/whoami/cfg\n  - path: ${DATA_DIR}/whoami/cache\n    backup: false\n",
		"values.yml":       "IMAGE: docker.io/traefik/whoami:v1.10\n",
		"whoami.container": "[Container]\nImage=${IMAGE}\n\n[Install]\nWantedBy=default.target\n",
	} {
		os.WriteFile(filepath.Join(svc, name), []byte(content), 0o644)
	}
	if err := proposal.CommitDirty(context.Background(), env.Paths.State, "human:test"); err != nil {
		t.Fatal(err)
	}
	return env, dataDir, calls
}

func writeAs(env *cliEnv, svc, path, content string) int {
	e := &cliEnv{Actor: env.Actor, Paths: env.Paths, In: strings.NewReader(content)}
	return cmdWrite(context.Background(), e, []string{svc, path})
}

// #35 (2): a file that does not exist yet under a symlinked directory passed
// the lexical prefix check and was written outside the jail.
func TestWriteThroughASymlinkIsRefused(t *testing.T) {
	env, data, _ := envBox(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(data, "whoami", "cfg", "link")); err != nil {
		t.Fatal(err)
	}
	code := writeAs(env, "whoami", filepath.Join(data, "whoami", "cfg", "link", "authorized_keys"), "ssh-ed25519 AAAA attacker")
	if _, err := os.Stat(filepath.Join(outside, "authorized_keys")); err == nil {
		t.Fatalf("the write escaped the jail through the symlink (exit %d)", code)
	}
	if code == cli.ExitOK {
		t.Fatalf("a write through a symlink out of the jail was accepted")
	}
	// and the honest case still works
	if code := writeAs(env, "whoami", filepath.Join(data, "whoami", "cfg", "app.conf"), "key=value"); code != cli.ExitOK {
		t.Fatalf("a plain write inside the jail: exit %d", code)
	}
	if b, _ := os.ReadFile(filepath.Join(data, "whoami", "cfg", "app.conf")); string(b) != "key=value" {
		t.Fatalf("content: %q", b)
	}
}

// #35 (1): exec ran in any container the caller named; budget and snapshot
// were booked to the service it did not touch.
func TestExecOnlyInTheServicesOwnContainers(t *testing.T) {
	env, _, calls := envBox(t)
	code := cmdExec(context.Background(), env, []string{"whoami", "--container", "systemd-home-assistant", "--", "cat", "/etc/passwd"})
	if code == cli.ExitOK {
		t.Fatal("exec in another service's container was accepted")
	}
	for _, c := range *calls {
		if strings.HasPrefix(c, "podman exec") {
			t.Fatalf("podman exec ran: %s", c)
		}
	}
	if code := cmdExec(context.Background(), env, []string{"whoami", "--container", "systemd-whoami", "--", "true"}); code != cli.ExitOK {
		t.Fatalf("exec in the service's own container: exit %d", code)
	}
}

// #35 (3): the envelope promised a snapshot before the first mutation of
// the hour and silently went on without one.
func TestNoSnapshotNoMutation(t *testing.T) {
	env, data, calls := envBox(t)
	target := filepath.Join(data, "whoami", "cfg", "app.conf")
	// restic fails: the write must not happen
	resticRun = func(ctx context.Context, st *state.State, pw string, args ...string) (string, error) {
		*calls = append(*calls, "restic "+strings.Join(args, " "))
		return "Fatal: unable to open repository", os.ErrNotExist
	}
	if code := writeAs(env, "whoami", target, "x"); code == cli.ExitOK {
		t.Fatal("a write went ahead although the snapshot failed")
	}
	if _, err := os.Stat(target); err == nil {
		t.Fatal("the file was written without a snapshot")
	}
	// no backup repo at all: refused with a reason
	box := filepath.Join(env.Paths.State, "box.yml")
	b, _ := os.ReadFile(box)
	lines := strings.Split(string(b), "\n")
	var kept []string
	skip := false
	for _, l := range lines {
		if strings.HasPrefix(l, "backup:") {
			skip = true
			continue
		}
		if skip && strings.HasPrefix(l, "  ") {
			continue
		}
		skip = false
		kept = append(kept, l)
	}
	os.WriteFile(box, []byte(strings.Join(kept, "\n")), 0o644)
	if code := writeAs(env, "whoami", target, "x"); code == cli.ExitOK {
		t.Fatal("a write went ahead with no backup repo")
	}
	// unless policy.yml says the envelope's snapshot is optional
	pol := filepath.Join(env.Paths.State, "policy.yml")
	pb, _ := os.ReadFile(pol)
	os.WriteFile(pol, append(pb, []byte("envelope:\n  snapshot: optional\n")...), 0o644)
	if code := writeAs(env, "whoami", target, "x"); code != cli.ExitOK {
		t.Fatalf("with snapshot: optional the write should go ahead, exit %d", code)
	}
}

// #35 (3): only backup: true paths go into the pre-repair snapshot; the
// snapshot is pruned.
func TestSnapshotPathsSkipBackupFalse(t *testing.T) {
	env, data, calls := envBox(t)
	st, err := cli.LoadState(env)
	if err != nil {
		t.Fatal(err)
	}
	paths := snapshotPaths(st, st.Services["whoami"])
	if len(paths) != 1 || paths[0] != filepath.Join(data, "whoami", "cfg") {
		t.Fatalf("snapshot paths: %v", paths)
	}
	writeAs(env, "whoami", filepath.Join(data, "whoami", "cfg", "a"), "x")
	joined := strings.Join(*calls, "\n")
	if strings.Contains(joined, filepath.Join(data, "whoami", "cache")) {
		t.Fatalf("the cache path (backup: false) was snapshotted:\n%s", joined)
	}
	if !strings.Contains(joined, "forget --tag pre-repair") {
		t.Fatalf("pre-repair snapshots are never pruned:\n%s", joined)
	}
}

// #35 (4): one unparsable journal line reset the budget to zero.
func TestACorruptJournalLineDoesNotResetTheBudget(t *testing.T) {
	env, data, _ := envBox(t)
	for i := 0; i < RepairBudget; i++ {
		journal.Append(env.Paths.Journal, journal.Entry{T: time.Now().Add(-10 * time.Minute), Actor: "ops", Verb: "exec", Service: "whoami", Result: "info"})
	}
	day := filepath.Join(env.Paths.Journal, time.Now().UTC().Format("2006-01-02")+".jsonl")
	f, _ := os.OpenFile(day, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("{this is not json\n")
	f.Close()
	if code := writeAs(env, "whoami", filepath.Join(data, "whoami", "cfg", "a"), "x"); code != cli.ExitRefused {
		t.Fatalf("the budget was reset by a corrupt line: exit %d", code)
	}
}
