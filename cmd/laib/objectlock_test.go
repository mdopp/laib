package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/backup"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/state"
)

// Decision K (owner, 2026-11-09): the copy off the box goes to an S3 bucket
// with Object Lock, so a burglar with root cannot delete or encrypt it. The
// box only adds: no forget and no prune ever runs there (the lock keeps the
// versions, the retention is the bucket's), the bucket's keys reach restic
// from secrets.age as environment, never argv or box.yml.
func TestALockedOffsiteRepoIsOnlyAddedTo(t *testing.T) {
	b := newRbox(t)
	primary := filepath.Join(b.env.Paths.Home, "restic")
	boxYml := filepath.Join(b.env.Paths.State, "box.yml")
	raw, _ := os.ReadFile(boxYml)
	text := strings.Replace(string(raw), "  repo: sftp:nas:/backup/laib\n", "  repo: "+primary+"\n  offsite:\n    repo: s3:https://s3.example.test/laib-box\n    objectLock: true\n    secrets: [AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY]\n", 1)
	if text == string(raw) {
		t.Fatal("the golden box.yml has no backup.repo to replace")
	}
	os.WriteFile(boxYml, []byte(text), 0o644)
	sec, _ := secrets.Load(b.env.Paths.Secrets, b.env.Paths.Key)
	sec["AWS_ACCESS_KEY_ID"], sec["AWS_SECRET_ACCESS_KEY"] = "key-id", "key-secret"
	secrets.Save(b.env.Paths.Secrets, b.env.Paths.Key, sec)

	realRestic := backup.ResticRun
	t.Cleanup(func() { backup.ResticRun = realRestic })
	var calls []string
	backup.ResticRun = func(_ context.Context, st *state.State, pw string, args ...string) (string, error) {
		bk := st.Box.Backup
		calls = append(calls, bk.Repo+" "+strings.Join(args, " "))
		if strings.HasPrefix(bk.Repo, "s3:") {
			if bk.Env["AWS_ACCESS_KEY_ID"] != "key-id" || bk.Env["AWS_SECRET_ACCESS_KEY"] != "key-secret" || !bk.Plain {
				t.Errorf("the bucket's keys must reach restic as environment: %+v", bk)
			}
			if args[0] == "cat" {
				return "repository does not exist", errors.New("exit 10")
			}
		}
		return fakeRestoreTest(args), nil
	}
	if rc := backup.Cmd(context.Background(), b.env, nil); rc != cli.ExitOK {
		t.Fatalf("backup rc %d: %s", rc, strings.Join(calls, "\n"))
	}
	joined := strings.Join(calls, "\n")
	for _, w := range []string{"s3:https://s3.example.test/laib-box init --from-repo", "s3:https://s3.example.test/laib-box copy --from-repo " + primary + " --tag laib"} {
		if !strings.Contains(joined, w) {
			t.Fatalf("missing %q in\n%s", w, joined)
		}
	}
	for _, c := range calls {
		if strings.HasPrefix(c, "s3:") && (strings.Contains(c, " forget") || strings.Contains(c, " prune")) {
			t.Fatalf("the box must never forget or prune in the locked bucket: %q", c)
		}
		if strings.Contains(c, "key-secret") {
			t.Fatalf("a key in argv: %q", c)
		}
	}
	if !strings.Contains(joined, primary+" forget --tag laib") {
		t.Fatalf("the local repo keeps its retention:\n%s", joined)
	}
}
