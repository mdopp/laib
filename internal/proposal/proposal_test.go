package proposal

import (
	"context"
	"errors"
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

// #41 (5): two proposals that touch the same lines — the second approval
// is refused, main stays what it was, no conflict markers, the proposal is
// marked stale with a plain reason.
func TestApproveRefusesAConflictAndLeavesMainClean(t *testing.T) {
	ctx := context.Background()
	repo, inbox := setup(t)
	os.WriteFile(filepath.Join(repo, "box.yml"), []byte("domain: y\n"), 0o644)
	p1, err := Create(ctx, repo, inbox, "ops", "y")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(repo, "box.yml"), []byte("domain: z\n"), 0o644)
	p2, err := Create(ctx, repo, inbox, "pi", "z")
	if err != nil {
		t.Fatal(err)
	}
	if err := Approve(ctx, repo, inbox, p1, "human:mdopp"); err != nil {
		t.Fatal(err)
	}
	err = Approve(ctx, repo, inbox, p2, "human:mdopp")
	if err == nil || !strings.Contains(err.Error(), "veraltet") {
		t.Fatalf("a conflicting proposal must be refused as stale: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(repo, "box.yml"))
	if string(b) != "domain: y\n" || strings.Contains(string(b), "<<<<") {
		t.Fatalf("main changed or carries markers:\n%s", b)
	}
	if out, _ := git(ctx, repo, "status", "--porcelain"); strings.TrimSpace(out) != "" {
		t.Fatalf("main is not clean after the refused approve:\n%s", out)
	}
	got, _ := Get(inbox, p2.ID)
	if got.Status != "stale" || got.StaleReason == "" {
		t.Fatalf("%+v", got)
	}
	if err := Approve(ctx, repo, inbox, got, "human:mdopp"); err == nil {
		t.Fatal("a stale proposal approved")
	}
}

// #41 (6): two proposals that are each valid can merge into an invalid
// main; the merged result is checked before main moves.
func TestApproveValidatesTheMergedResult(t *testing.T) {
	ctx := context.Background()
	repo, inbox := setup(t)
	os.WriteFile(filepath.Join(repo, "box.yml"), []byte("domain: y\n"), 0o644)
	p, err := Create(ctx, repo, inbox, "ops", "y")
	if err != nil {
		t.Fatal(err)
	}
	refuse := func(dir string) error {
		if b, _ := os.ReadFile(filepath.Join(dir, "box.yml")); strings.Contains(string(b), "domain: y") {
			return errors.New("route host echo is declared twice")
		}
		return nil
	}
	err = ApproveChecked(ctx, repo, inbox, p, "human:mdopp", refuse)
	if err == nil || !strings.Contains(err.Error(), "declared twice") {
		t.Fatalf("an invalid merge result must be refused with its reason: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "box.yml")); string(b) != "domain: x\n" {
		t.Fatalf("main moved although the result was invalid:\n%s", b)
	}
	if out, _ := git(ctx, repo, "worktree", "list"); strings.Count(out, "\n") > 1 {
		t.Fatalf("a worktree was left behind:\n%s", out)
	}
	if got, _ := Get(inbox, p.ID); got.Status != "stale" {
		t.Fatalf("%+v", got)
	}
}

// #50 (3): the box's own proposals (the template-sync timer runs as laib)
// are approved by the box when the dial says auto — "own proposal" is a
// rule for hands with tokens, not for the box; and an identical open
// proposal is reused, not duplicated.
func TestTheBoxApprovesItsOwnTemplateUpdateAndDoesNotDuplicateIt(t *testing.T) {
	ctx := context.Background()
	repo, inbox := setup(t)
	os.WriteFile(filepath.Join(repo, "box.yml"), []byte("domain: y\n"), 0o644)
	p1, err := Create(ctx, repo, inbox, "laib", "template update")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(repo, "box.yml"), []byte("domain: y\n"), 0o644)
	p2, err := Create(ctx, repo, inbox, "laib", "template update")
	if err != nil {
		t.Fatal(err)
	}
	if p2.ID != p1.ID {
		t.Fatalf("the same change proposed twice must reuse the open proposal: #%d and #%d", p1.ID, p2.ID)
	}
	open, _ := List(inbox, true)
	if len(open) != 1 {
		t.Fatalf("%d open proposals", len(open))
	}
	if err := Approve(ctx, repo, inbox, p1, "laib"); err != nil {
		t.Fatalf("the box could not approve its own template update: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "box.yml")); string(b) != "domain: y\n" {
		t.Fatal("not merged")
	}
}
