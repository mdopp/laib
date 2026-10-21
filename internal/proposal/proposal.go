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

	"github.com/mdopp/laib/internal/boxfs"
	"github.com/mdopp/laib/internal/sysd"
)

// Proposal is one inbox entry, stored as inbox/<id>.json.
type Proposal struct {
	ID          int       `json:"id"`
	Actor       string    `json:"actor"`
	Reason      string    `json:"reason"`
	Branch      string    `json:"branch"`
	Tier        string    `json:"tier"`
	Status      string    `json:"status"` // open | approved | rejected | stale (#41: did not merge or did not validate)
	Created     time.Time `json:"created"`
	Decided     time.Time `json:"decided,omitempty"`
	DecidedBy   string    `json:"decidedBy,omitempty"`
	Files       []string  `json:"files"`
	Warnings    []string  `json:"warnings,omitempty"`    // e.g. a second service for one purpose
	Snapshot    []string  `json:"snapshot,omitempty"`    // services laib snapshots before it applies the approved change (#528)
	Class       string    `json:"class"`                 // what the dial is keyed by: values | new-service | template-update | patch | tree | human
	Kind        string    `json:"kind,omitempty"`        // the edit's shape: tree | values | new-service | template-update | patch
	Reasons     []string  `json:"reasons,omitempty"`     // why only a human may allow it (class human), plain German
	Mints       []string  `json:"mints,omitempty"`       // hands the change mints Laib tokens for
	Adds        []string  `json:"adds,omitempty"`        // services the change adds
	Facts       []string  `json:"facts,omitempty"`       // plain facts for the decision card (#58)
	StaleReason string    `json:"staleReason,omitempty"` // why it is stale: the conflict or the validation error (#41)
	Anchor
	// what came of it (#544), recorded by laib, never by the proposer:
	// effective | no-effect | rolled-back | rejected | later | applied
	Outcome      string    `json:"outcome,omitempty"`
	RejectReason string    `json:"rejectReason,omitempty"` // the owner's short reason for a no
	Settled      time.Time `json:"settled,omitempty"`
}

// Anchor is what a proposal answers (#544): the diag finding it should make
// go away ("<probe> <service>"), its evidence, how it is undone, and the
// outside sources it rests on. A source makes it class human.
type Anchor struct {
	Finding  string   `json:"finding,omitempty"`
	Evidence string   `json:"evidence,omitempty"`
	Undo     string   `json:"undo,omitempty"`
	Sources  []string `json:"sources,omitempty"`
}

// Window is how long an approved proposal's finding has to go away (#544).
const Window = 2 * time.Hour

// Later: a no that only means "not now" (#544) — free, never counted
// against the proposer.
func Later(reason string) bool {
	r := strings.ToLower(strings.Trim(strings.TrimSpace(reason), ".!"))
	return r == "nicht jetzt" || r == "später" || r == "spaeter"
}

// Settle records what came of every approved proposal not yet settled
// (#544): its finding gone from diag is effective, still there Window after
// the approval is no effect; one without a finding is applied, nothing to
// measure. present says whether diag shows a finding now. It returns the
// proposals it measured, for the journal.
func Settle(inbox string, present func(finding string) bool, now time.Time) []*Proposal {
	all, _ := List(inbox, false)
	var out []*Proposal
	for _, p := range all {
		if p.Status != "approved" || p.Outcome != "" {
			continue
		}
		switch {
		case p.Finding == "":
			p.Outcome = "applied"
		case !present(p.Finding):
			p.Outcome = "effective"
		case now.Sub(p.Decided) > Window:
			p.Outcome = "no-effect"
		default:
			continue
		}
		p.Settled = now
		if Save(inbox, p) == nil && p.Finding != "" {
			out = append(out, p)
		}
	}
	return out
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

// CommitDirty records uncommitted edits in the main checkout as their own
// commit before a branch is cut or merged. A human's shell edits (a dns
// block in box.yml, a new secrets.age from laib secret set) are desired
// state and must not block a merge; agents have no file access, so what
// is dirty was done by a human on the box.
func CommitDirty(ctx context.Context, repo, actor string) error {
	if err := EnsureRepo(ctx, repo); err != nil {
		return err
	}
	status, err := git(ctx, repo, "status", "--porcelain")
	if err != nil {
		return err
	}
	if strings.TrimSpace(status) == "" {
		return nil
	}
	if _, err := git(ctx, repo, "add", "-A"); err != nil {
		return err
	}
	_, err = git(ctx, repo, "commit", "-q", "-m", "edits on the box", "--author", actor+" <"+actor+"@laib>")
	return err
}

// CommitSecrets commits secrets.age on main when it changed (#328): a
// secret a hook handed back, or one `laib secret set` wrote, is the box's
// truth, never part of a proposal a reject would drop.
func CommitSecrets(ctx context.Context, repo, why string) error {
	if st, err := git(ctx, repo, "status", "--porcelain", "--", "secrets.age"); err != nil || st == "" {
		return err
	}
	if _, err := git(ctx, repo, "add", "--", "secrets.age"); err != nil {
		return err
	}
	_, err := git(ctx, repo, "commit", "-q", "-m", why, "--", "secrets.age")
	return err
}

// Create commits the working tree to proposal/<id> and returns to main with
// a clean tree. The inbox entry lists the files the branch changes. The
// judge reads the dirty tree against a detached checkout of HEAD first.
func Create(ctx context.Context, repo, inbox, actor, reason string, judge Judge) (*Proposal, error) {
	if err := EnsureRepo(ctx, repo); err != nil {
		return nil, err
	}
	if err := CommitSecrets(ctx, repo, "secrets.age as the box has it"); err != nil {
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
	var verdict Verdict
	if judge != nil {
		head, err := os.MkdirTemp("", "laib-head")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(head)
		if _, err := git(ctx, repo, "worktree", "add", "-q", "--detach", head, "HEAD"); err != nil {
			return nil, err
		}
		verdict, err = judge(head, repo)
		git(ctx, repo, "worktree", "remove", "--force", head)
		git(ctx, repo, "worktree", "prune")
		if err != nil {
			return nil, err
		}
	}
	p := &Proposal{ID: id, Actor: actor, Reason: reason, Branch: fmt.Sprintf("proposal/%d", id), Tier: "apply", Status: "open", Created: now(),
		Kind: "tree", Class: classOf("tree", verdict), Reasons: verdict.Human, Mints: verdict.Mints, Adds: verdict.Added, Facts: verdict.Facts}
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
	// the same change is already open (a timer proposing again, #50): reuse
	// it, drop this branch — the inbox holds one proposal per change
	if open, _ := List(inbox, true); len(open) > 0 {
		mine := Diff(ctx, repo, p)
		for _, o := range open {
			if o.Branch != p.Branch && Diff(ctx, repo, o) == mine {
				git(ctx, repo, "branch", "-q", "-D", p.Branch)
				return o, nil
			}
		}
	}
	return p, Save(inbox, p)
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

// ApproveChecked merges in a temporary worktree (#41): a conflict never
// reaches main (no markers, no half-merged state), the merged result is
// validated by check (state loads, renders) before main fast-forwards to
// it, and a proposal that fails either way is marked stale with the
// reason — "veraltet — neu vorschlagen".
func ApproveChecked(ctx context.Context, repo, inbox string, p *Proposal, by string, check func(dir string) error) error {
	if p.Status != "open" {
		return fmt.Errorf("proposal %d is %s", p.ID, p.Status)
	}
	// A hand never approves its own proposal — except the human: on the
	// shell a human is the authority, and --add is how they install.
	// … and the box itself (the template-sync timer, actor laib) is not a
	// hand with a token: its proposals are approved by the box when the dial
	// says so (#50), the policy check is the caller's.
	if Hand(by) == Hand(p.Actor) && Hand(by) != "human" && by != "laib" {
		return ErrOwnProposal
	}
	if err := CommitDirty(ctx, repo, by); err != nil {
		return err
	}
	wt, err := os.MkdirTemp("", "laib-approve-*")
	if err != nil {
		return err
	}
	os.Remove(wt)
	defer func() {
		git(ctx, repo, "worktree", "remove", "--force", wt)
		os.RemoveAll(wt)
		git(ctx, repo, "worktree", "prune")
	}()
	if _, err := git(ctx, repo, "worktree", "add", "-q", "--detach", wt, "main"); err != nil {
		return err
	}
	stale := func(reason string) error {
		p.Status, p.StaleReason, p.Decided, p.DecidedBy = "stale", reason, time.Now().UTC(), by
		Save(inbox, p)
		return fmt.Errorf("Vorschlag #%d ist veraltet — neu vorschlagen: %s", p.ID, reason)
	}
	if out, err := git(ctx, wt, "merge", "-q", "--no-ff", "-m", fmt.Sprintf("approve #%d by %s: %s", p.ID, by, p.Reason), p.Branch); err != nil {
		git(ctx, wt, "merge", "--abort")
		files, _ := git(ctx, wt, "diff", "--name-only", "--diff-filter=U")
		git(ctx, wt, "reset", "-q", "--hard")
		return stale("Konflikt mit dem aktuellen State in " + strings.TrimSpace(strings.ReplaceAll(files+" "+out, "\n", " ")))
	}
	if check != nil {
		if err := check(wt); err != nil {
			return stale("das Ergebnis lädt nicht: " + err.Error())
		}
	}
	sha, err := git(ctx, wt, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if out, err := git(ctx, repo, "merge", "-q", "--ff-only", strings.TrimSpace(sha)); err != nil {
		return fmt.Errorf("main did not fast-forward to the merged result: %s", out)
	}
	git(ctx, repo, "branch", "-q", "-D", p.Branch)
	p.Status, p.Decided, p.DecidedBy = "approved", time.Now().UTC(), by
	return Save(inbox, p)
}

// Reject drops the branch and keeps the owner's reason (#544).
func Reject(ctx context.Context, repo, inbox string, p *Proposal, by, reason string) error {
	if p.Status != "open" {
		return fmt.Errorf("proposal %d is %s", p.ID, p.Status)
	}
	git(ctx, repo, "branch", "-q", "-D", p.Branch)
	p.Status, p.Decided, p.DecidedBy = "rejected", time.Now().UTC(), by
	p.Outcome, p.RejectReason, p.Settled = "rejected", strings.TrimSpace(reason), p.Decided
	if Later(reason) {
		p.Outcome = "later"
	}
	return Save(inbox, p)
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

// Save writes one inbox entry.
func Save(inbox string, p *Proposal) error {
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(inbox, strconv.Itoa(p.ID)+".json")
	return boxfs.WriteAtomic(path+".tmp", path, b, 0o644)
}

func now() time.Time { return time.Now().UTC() }
