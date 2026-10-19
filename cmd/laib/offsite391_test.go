package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/backup"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/diag"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/pages"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/state"
)

// #391: the second repo off the data disk (the Fritz!Box NAS). After the
// nightly backup its new snapshots are copied there (restic copy: the
// services stop once), with the backend's settings from box.yml and its
// password from secrets.age as environment. The box page counts two places
// only while the off-box copy is fresh; diag is red after 48 h without one.
func TestTheOffsiteRepoGetsACopyOfEveryBackup(t *testing.T) {
	b := newRbox(t)
	primary := filepath.Join(b.env.Paths.Home, "restic")
	boxYml := filepath.Join(b.env.Paths.State, "box.yml")
	raw, _ := os.ReadFile(boxYml)
	text := strings.Replace(string(raw), "  repo: sftp:nas:/backup/laib\n", "  repo: "+primary+"\n  offsite:\n    repo: rclone:nas:laib-restic\n    env: {RCLONE_CONFIG_NAS_TYPE: ftp, RCLONE_CONFIG_NAS_HOST: 192.168.178.1}\n    secrets: [RCLONE_CONFIG_NAS_PASS]\n", 1)
	if text == string(raw) {
		t.Fatal("the golden box.yml has no backup.repo to replace")
	}
	os.WriteFile(boxYml, []byte(text), 0o644)
	sec, _ := secrets.Load(b.env.Paths.Secrets, b.env.Paths.Key)
	sec["RCLONE_CONFIG_NAS_PASS"] = "obscured-nas-password"
	secrets.Save(b.env.Paths.Secrets, b.env.Paths.Key, sec)

	realRestic := backup.ResticRun
	t.Cleanup(func() { backup.ResticRun = realRestic })
	var calls []string
	offsiteFails := false
	backup.ResticRun = func(_ context.Context, st *state.State, pw string, args ...string) (string, error) {
		bk := st.Box.Backup
		calls = append(calls, bk.Repo+" "+strings.Join(args, " "))
		if bk.Repo == "rclone:nas:laib-restic" {
			if bk.Env["RCLONE_CONFIG_NAS_PASS"] != "obscured-nas-password" || bk.Env["RCLONE_CONFIG_NAS_HOST"] != "192.168.178.1" || !bk.Plain || pw != "pw" {
				t.Errorf("the offsite run lacks its environment, its password or runs inside podman unshare: %+v", bk)
			}
			if offsiteFails || args[0] == "cat" {
				return "repository does not exist", errors.New("exit 10")
			}
		}
		return "ok", nil
	}
	if rc := backup.Cmd(context.Background(), b.env, nil); rc != cli.ExitOK {
		t.Fatalf("backup rc %d: %s", rc, strings.Join(calls, "\n"))
	}
	want := []string{
		"rclone:nas:laib-restic init --from-repo " + primary + " --copy-chunker-params",
		"rclone:nas:laib-restic copy --from-repo " + primary + " --tag laib",
		"rclone:nas:laib-restic forget --tag laib --keep-daily 14 --keep-weekly 8 --keep-monthly 12 --prune",
	}
	joined := strings.Join(calls, "\n")
	last := strings.Index(joined, primary+" backup --tag laib")
	for _, w := range want {
		i := strings.Index(joined, w)
		if i < 0 || i < last {
			t.Fatalf("missing or before the backup: %q in\n%s", w, joined)
		}
		last = i
	}
	st, err := cli.LoadState(b.env)
	if err != nil {
		t.Fatal(err)
	}
	if lb, _ := pages.Guarantees(b.env, st); lb.Repos != 2 {
		t.Fatalf("a fresh off-box copy is a second place: %+v", lb)
	}
	if f := backupFinding(b, st, "außer Haus"); f != nil {
		t.Fatalf("a fresh off-box copy is no finding: %+v", *f)
	}

	// the NAS does not answer: the run is red, the box page says one place, diag red after 48 h
	offsiteFails = true
	if rc := backup.Cmd(context.Background(), b.env, nil); rc == cli.ExitOK {
		t.Fatal("a failed off-box copy must fail the run")
	}
	os.RemoveAll(b.env.Paths.Journal)
	journal.Append(b.env.Paths.Journal, journal.Entry{T: time.Now().Add(-72 * time.Hour), Actor: "laib", Verb: "backup", Reason: "offsite", Result: "green"})
	journal.Append(b.env.Paths.Journal, journal.Entry{Actor: "laib", Verb: "backup", Result: "green"})
	if lb, _ := pages.Guarantees(b.env, st); lb.Repos != 1 {
		t.Fatalf("a stale off-box copy is no second place: %+v", lb)
	}
	if f := backupFinding(b, st, "außer Haus"); f == nil || f.Severity != "rot" {
		t.Fatalf("diag must be red when the off-box copy is older than 48 h: %+v", f)
	}
}

func backupFinding(b *rbox, st *state.State, word string) *diag.Finding {
	for _, f := range diag.Probes(context.Background(), b.env, st, "") {
		if f.Probe == "backup" && strings.Contains(f.Detail, word) {
			return &f
		}
	}
	return nil
}
