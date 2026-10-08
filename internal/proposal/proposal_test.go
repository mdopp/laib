package proposal

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setup(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	repo := filepath.Join(home, "state")
	os.MkdirAll(repo, 0o755)
	os.WriteFile(filepath.Join(repo, "box.yml"), []byte("domain: x\n"), 0o644)
	if err := EnsureRepo(context.Background(), repo); err != nil {
		t.Fatal(err)
	}
	return repo, filepath.Join(home, "inbox")
}

func TestProposeApproveReject(t *testing.T) {
	ctx := context.Background()
	repo, inbox := setup(t)
	if _, err := Create(ctx, repo, inbox, "ops", "nothing changed"); err == nil {
		t.Fatal("clean tree must not propose")
	}
	os.WriteFile(filepath.Join(repo, "box.yml"), []byte("domain: y\n"), 0o644)
	p, err := Create(ctx, repo, inbox, "ops", "rename domain")
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != 1 || p.Branch != "proposal/1" || len(p.Files) != 1 || p.Files[0] != "box.yml" {
		t.Fatalf("%+v", p)
	}
	// Working tree is back on main and clean; the change lives on the branch.
	if b, _ := os.ReadFile(filepath.Join(repo, "box.yml")); string(b) != "domain: x\n" {
		t.Fatal("main tree changed")
	}
	if d := Diff(ctx, repo, p); !strings.Contains(d, "+domain: y") {
		t.Fatal(d)
	}
	if err := Approve(ctx, repo, inbox, p, "ops:other"); err != ErrOwnProposal {
		t.Fatal("same hand approved its own proposal")
	}
	if err := Approve(ctx, repo, inbox, p, "human:mdopp"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "box.yml")); string(b) != "domain: y\n" {
		t.Fatal("merge did not land on main")
	}
	got, _ := Get(inbox, 1)
	if got.Status != "approved" || got.DecidedBy != "human:mdopp" {
		t.Fatalf("%+v", got)
	}
	if err := Approve(ctx, repo, inbox, got, "human:mdopp"); err == nil {
		t.Fatal("approved twice")
	}

	os.WriteFile(filepath.Join(repo, "box.yml"), []byte("domain: z\n"), 0o644)
	p2, _ := Create(ctx, repo, inbox, "pi", "another")
	if p2.ID != 2 {
		t.Fatal(p2.ID)
	}
	if err := Reject(ctx, repo, inbox, p2, "human:mdopp"); err != nil {
		t.Fatal(err)
	}
	open, _ := List(inbox, true)
	if len(open) != 0 {
		t.Fatalf("%d open", len(open))
	}
	if out, _ := Diff(ctx, repo, p2), ""; out != "" {
		t.Fatal("branch survived reject")
	}
}
