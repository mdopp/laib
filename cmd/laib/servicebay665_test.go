package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/backup"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/diag"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/secrets"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

// #665: ServiceBay's data dir goes once no secret has a value from it: a
// destroy, so a human's, behind a snapshot of the dir (policy.yml approvals
// destroy: snapshot required). The snapshot holds only dead values then,
// which is why it waits for the rotation. diag offers it as a repair, and
// the proposal is of tier destroy: only the Inbox approves it.

type sbBox struct {
	*rbox
	dir   string
	snaps []string
	fail  bool
}

func newSBBox(t *testing.T) *sbBox {
	b := &sbBox{rbox: newRbox(t), dir: filepath.Join(t.TempDir(), "servicebay")}
	os.MkdirAll(filepath.Join(b.dir, "history", "auth.kube"), 0o755)
	os.WriteFile(filepath.Join(b.dir, "history", "auth.kube", "1.bak"), []byte("LLDAP_LDAP_USER_PASS=carried-at-the-move\n"), 0o600)
	t.Setenv("LAIB_SERVICEBAY_DIR", b.dir)
	realRestic, realRun := backup.ResticRun, sysd.Run
	t.Cleanup(func() { backup.ResticRun = realRestic })
	backup.ResticRun = func(_ context.Context, _ *state.State, _ string, args ...string) (string, error) {
		if args[0] == "backup" {
			if _, err := os.Stat(b.dir); err != nil {
				return "", errors.New("nothing to back up")
			}
			b.snaps = append(b.snaps, strings.Join(args, " "))
			if b.fail {
				return "", errors.New("repo locked")
			}
			return "snapshot 1a2b3c4d saved", nil
		}
		return "", nil
	}
	sysd.Run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "podman" && slices.Equal(args[:3], []string{"unshare", "rm", "-rf"}) {
			return "", os.RemoveAll(args[len(args)-1])
		}
		return realRun(ctx, name, args...)
	}
	return b
}

func (b *sbBox) setLLDAP(v string) {
	sec, _ := secrets.Load(b.env.Paths.Secrets, b.env.Paths.Key)
	sec["LLDAP_ADMIN_PASSWORD"] = v
	secrets.Save(b.env.Paths.Secrets, b.env.Paths.Key, sec)
}

func (b *sbBox) remove(actor string) (int, string) {
	var w bytes.Buffer
	env := &cliEnv{Paths: b.env.Paths, Actor: actor, Out: &w, ErrOut: &w}
	return diag.ServiceBay(context.Background(), env, []string{"remove"}), w.String()
}

func TestServiceBayDirStaysWhileASecretStillHasItsValue(t *testing.T) {
	b := newSBBox(t)
	b.setLLDAP("carried-at-the-move")
	if rc, out := b.remove("human:test"); rc == cli.ExitOK || !strings.Contains(out, "LLDAP_ADMIN_PASSWORD") || strings.Contains(out, "carried-at-the-move") {
		t.Fatalf("removed with a carried secret left, or the refusal does not name it: rc %d %q", rc, out)
	}
	if _, err := os.Stat(b.dir); err != nil || len(b.snaps) != 0 {
		t.Fatal("the dir went, or a snapshot of live values was taken")
	}
}

func TestServiceBayDirGoesAfterASnapshotOnlyByAHuman(t *testing.T) {
	b := newSBBox(t)
	b.setLLDAP("rotated-665")
	if rc, _ := b.remove("ops:test"); rc == cli.ExitOK {
		t.Fatal("an agent removed the dir")
	}
	b.fail = true
	if rc, _ := b.remove("human:test"); rc == cli.ExitOK {
		t.Fatal("removed without a snapshot")
	}
	if _, err := os.Stat(b.dir); err != nil {
		t.Fatal("the dir went though its snapshot failed")
	}
	b.fail = false
	if rc, out := b.remove("human:test"); rc != cli.ExitOK {
		t.Fatalf("remove: rc %d %s", rc, out)
	}
	if _, err := os.Stat(b.dir); !os.IsNotExist(err) {
		t.Fatal("the dir is still there")
	}
	last := b.snaps[len(b.snaps)-1]
	if !strings.Contains(last, b.dir) || !strings.Contains(last, "--tag pre-destroy") {
		t.Fatalf("the snapshot before the removal: %q", last)
	}
	es, _ := journal.Read(b.env.Paths.Journal, time.Time{})
	if !slices.ContainsFunc(es, func(e journal.Entry) bool {
		return e.Verb == "servicebay" && e.Result == "green" && strings.Contains(e.Detail, "1a2b3c4d")
	}) {
		t.Fatalf("the removal is journaled with its snapshot: %+v", es)
	}
}

func TestServiceBayRemovalIsADestroyProposalForTheInbox(t *testing.T) {
	b := newSBBox(t)
	b.setLLDAP("rotated-665")
	ctx := context.Background()
	if rc := diag.Repair(ctx, &cliEnv{Paths: b.env.Paths, Actor: "ops:test"}, []string{"servicebay"}); rc != cli.ExitOK {
		t.Fatalf("laib repair servicebay: rc %d", rc)
	}
	open, _ := proposal.List(cli.InboxDir(b.env), true)
	if len(open) != 1 || open[0].Tier != "destroy" || !strings.Contains(open[0].Reason, "laib servicebay remove") {
		t.Fatalf("want one destroy-tier proposal that names the command: %+v", open)
	}
	id := strconv.Itoa(open[0].ID)
	if rc := inbox.Decide(ctx, &cliEnv{Paths: b.env.Paths, Actor: "approver:signal"}, []string{id}, true); rc == cli.ExitOK {
		t.Fatal("the Signal approver approved a destroy")
	}
	if rc := inbox.Decide(ctx, &cliEnv{Paths: b.env.Paths, Actor: "human:test"}, []string{id}, true); rc != cli.ExitOK {
		t.Fatalf("the human's approval: rc %d", rc)
	}
	if _, err := os.Stat(b.dir); !os.IsNotExist(err) || len(b.snaps) != 1 {
		t.Fatalf("approved, but the dir is there or was not snapshotted once: %v", b.snaps)
	}
}
