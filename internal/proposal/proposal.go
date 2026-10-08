// Package proposal is the unit of change for hands below the apply tier: a
// branch in the state repo plus an inbox entry (docs/INTERFACE.md, ADR 0001).
package proposal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mdopp/laib/internal/sysd"
)

// Proposal is one inbox entry, stored as inbox/<id>.json.
type Proposal struct {
	ID        int       `json:"id"`
	Actor     string    `json:"actor"`
	Reason    string    `json:"reason"`
	Branch    string    `json:"branch"`
	Tier      string    `json:"tier"`
	Status    string    `json:"status"` // open | approved | rejected
	Created   time.Time `json:"created"`
	Decided   time.Time `json:"decided,omitempty"`
	DecidedBy string    `json:"decidedBy,omitempty"`
	Files     []string  `json:"files"`
}

// ErrOwnProposal: a hand never approves its own proposal (ADR 0006).
var ErrOwnProposal = errors.New("a hand cannot approve its own proposal")

// Hand returns the hand part of an actor ("human:mdopp" → "human").
func Hand(actor string) string { return strings.SplitN(actor, ":", 2)[0] }

func git(ctx context.Context, repo string, args ...string) (string, error) {
	out, err := sysd.Run(ctx, "git", append([]string{"-C", repo, "-c", "user.name=laib", "-c", "user.email=laib@localhost"}, args...)...)
	if err != nil {
		return out, fmt.Errorf("git %s: %s", strings.Join(args, " "), out)
	}
	return out, nil
}

// EnsureRepo makes sure the state dir is a Git repo with a main branch and
// at least one commit, so branches have something to start from.
func EnsureRepo(ctx context.Context, repo string) error {
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		if _, err := git(ctx, repo, "init", "-q", "-b", "main"); err != nil {
			return err
		}
	}
	if _, err := git(ctx, repo, "rev-parse", "--verify", "HEAD"); err != nil {
		if _, err := git(ctx, repo, "add", "-A"); err != nil {
			return err
		}
		if _, err := git(ctx, repo, "commit", "-q", "--allow-empty", "-m", "init: state repo"); err != nil {
			return err
		}
	}
	return nil
}

// Create commits the working tree to proposal/<id> and returns to main with
// a clean tree. The inbox entry lists the files the branch changes.
func Create(ctx context.Context, repo, inbox, actor, reason string) (*Proposal, error) {
	if err := EnsureRepo(ctx, repo); err != nil {
		return nil, err
	}
	status, err := git(ctx, repo, "status", "--porcelain")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(status) == "" {
		return nil, errors.New("nothing to propose: the state repo has no uncommitted change")
	}
	id, err := nextID(inbox)
	if err != nil {
		return nil, err
	}
	p := &Proposal{ID: id, Actor: actor, Reason: reason, Branch: fmt.Sprintf("proposal/%d", id), Tier: "apply", Status: "open", Created: time.Now().UTC()}
	for _, line := range strings.Split(strings.TrimSpace(status), "\n") {
		// Run trims the output, so the two status columns may be gone; the
		// path is always the last field.
		if f := strings.Fields(line); len(f) > 0 {
			p.Files = append(p.Files, f[len(f)-1])
		}
	}
	if _, err := git(ctx, repo, "checkout", "-q", "-b", p.Branch); err != nil {
		return nil, err
	}
	if _, err := git(ctx, repo, "add", "-A"); err != nil {
		return nil, err
	}
	if _, err := git(ctx, repo, "commit", "-q", "-m", "proposal: "+reason, "--author", actor+" <"+actor+"@laib>"); err != nil {
		return nil, err
	}
	if _, err := git(ctx, repo, "checkout", "-q", "main"); err != nil {
		return nil, err
	}
	return p, save(inbox, p)
}

// Diff returns the proposal's diff against main. values.yml carries secret
// references only, so this is safe to show.
func Diff(ctx context.Context, repo string, p *Proposal) string {
	out, err := sysd.Run(ctx, "git", "-C", repo, "diff", "main..."+p.Branch)
	if err != nil {
		return ""
	}
	return out
}

// Approve merges the branch into main. The caller then runs apply.
func Approve(ctx context.Context, repo, inbox string, p *Proposal, by string) error {
	if p.Status != "open" {
		return fmt.Errorf("proposal %d is %s", p.ID, p.Status)
	}
	if Hand(by) == Hand(p.Actor) {
		return ErrOwnProposal
	}
	if _, err := git(ctx, repo, "merge", "-q", "--no-ff", "-m", fmt.Sprintf("approve #%d by %s: %s", p.ID, by, p.Reason), p.Branch); err != nil {
		return err
	}
	git(ctx, repo, "branch", "-q", "-D", p.Branch)
	p.Status, p.Decided, p.DecidedBy = "approved", time.Now().UTC(), by
	return save(inbox, p)
}

// Reject drops the branch.
func Reject(ctx context.Context, repo, inbox string, p *Proposal, by string) error {
	if p.Status != "open" {
		return fmt.Errorf("proposal %d is %s", p.ID, p.Status)
	}
	git(ctx, repo, "branch", "-q", "-D", p.Branch)
	p.Status, p.Decided, p.DecidedBy = "rejected", time.Now().UTC(), by
	return save(inbox, p)
}

// Get loads one proposal.
func Get(inbox string, id int) (*Proposal, error) {
	b, err := os.ReadFile(filepath.Join(inbox, strconv.Itoa(id)+".json"))
	if err != nil {
		return nil, fmt.Errorf("proposal %d: %w", id, err)
	}
	var p Proposal
	return &p, json.Unmarshal(b, &p)
}

// List returns every proposal, newest first; openOnly filters.
func List(inbox string, openOnly bool) ([]*Proposal, error) {
	files, _ := filepath.Glob(filepath.Join(inbox, "*.json"))
	var out []*Proposal
	for _, f := range files {
		id, err := strconv.Atoi(strings.TrimSuffix(filepath.Base(f), ".json"))
		if err != nil {
			continue
		}
		p, err := Get(inbox, id)
		if err != nil {
			return nil, err
		}
		if openOnly && p.Status != "open" {
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

func nextID(inbox string) (int, error) {
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		return 0, err
	}
	all, err := List(inbox, false)
	if err != nil {
		return 0, err
	}
	if len(all) == 0 {
		return 1, nil
	}
	return all[0].ID + 1, nil
}

func save(inbox string, p *Proposal) error {
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(inbox, strconv.Itoa(p.ID)+".json.tmp")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, strings.TrimSuffix(tmp, ".tmp"))
}
