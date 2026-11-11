package proposal

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/sysd"
)

// starts records each systemctl call that starts laib-notify and lets every
// other command (git) run as before; fail makes the start exit red.
func starts(t *testing.T, fail bool) *[]string {
	t.Helper()
	var got []string
	run := sysd.Run
	t.Cleanup(func() { sysd.Run = run })
	sysd.Run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name != "systemctl" {
			return run(ctx, name, args...)
		}
		got = append(got, strings.Join(args, " "))
		if fail {
			return "Failed to connect to bus", errors.New("exit status 1")
		}
		return "", nil
	}
	return &got
}

// #968: a new proposal starts laib-notify at once, without waiting for it;
// saving it again while open (anchor, step) does not; its decision does
// (#1113), so the chat line that announced it goes in seconds.
func TestFilingStartsNotifyOnce(t *testing.T) {
	ctx := context.Background()
	got := starts(t, false)
	repo, inbox := setup(t)
	os.WriteFile(filepath.Join(repo, "box.yml"), []byte("domain: y\n"), 0o644)
	p, err := Create(ctx, repo, inbox, "ops", "rename domain", nil)
	if err != nil {
		t.Fatal(err)
	}
	p.Anchored(Anchor{Finding: "probe svc"})
	if err := Save(inbox, p); err != nil {
		t.Fatal(err)
	}
	if err := Approve(ctx, repo, inbox, p, "human:mdopp"); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 2 || (*got)[0] != "--user start --no-block laib-notify.service" || (*got)[1] != (*got)[0] {
		t.Fatalf("want a non-blocking start on filing and one on the decision, got %q", *got)
	}
}

// #968: the start is a courtesy; the timer is the fallback, so a red start
// never fails the proposal.
func TestFailingStartKeepsTheProposal(t *testing.T) {
	ctx := context.Background()
	got := starts(t, true)
	repo, inbox := setup(t)
	os.WriteFile(filepath.Join(repo, "box.yml"), []byte("domain: y\n"), 0o644)
	p, err := Create(ctx, repo, inbox, "ops", "rename domain", nil)
	if err != nil {
		t.Fatal(err)
	}
	if q, err := Get(inbox, p.ID); err != nil || q.Status != "open" || len(*got) != 1 {
		t.Fatalf("%v %+v %q", err, q, *got)
	}
}

// #1113: a rejection starts laib-notify too, so its chat line is marked (#1183).
func TestRejectingStartsNotify(t *testing.T) {
	ctx := context.Background()
	got := starts(t, false)
	repo, inbox := setup(t)
	os.WriteFile(filepath.Join(repo, "box.yml"), []byte("domain: y\n"), 0o644)
	p, err := Create(ctx, repo, inbox, "ops", "rename domain", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := Reject(ctx, repo, inbox, p, "human:mdopp", ""); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 2 {
		t.Fatalf("want a start on filing and one on the rejection, got %q", *got)
	}
}
