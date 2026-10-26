package pages

import (
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/proposal"
)

// #716: a stale proposal that a later proposal on the same file or intent
// answered is not shown as something to do; one that really needs a new
// proposal says so in German, git's output only behind the details.
func TestStaleProposalsSupersededByALaterOneAreNotShown(t *testing.T) {
	gitOut := "Konflikt mit dem aktuellen State in policy.yml Auto-merging policy.yml CONFLICT (content): Merge conflict in policy.yml"
	p90 := &proposal.Proposal{ID: 90, Status: "stale", Reason: "claude darf apply", Files: []string{"policy.yml"}, StaleReason: gitOut}
	p92 := &proposal.Proposal{ID: 92, Status: "approved", Reason: "claude darf apply, neu", Files: []string{"policy.yml"}}
	if got := staleLines([]*proposal.Proposal{p90, p92}); got != "" {
		t.Errorf("#90 was answered by the approved #92 on the same file, still shown: %s", got)
	}
	// the same intent, another file: answered too
	p93 := &proposal.Proposal{ID: 93, Status: "rejected", Reason: "claude darf apply", Files: []string{"box.yml"}}
	if got := staleLines([]*proposal.Proposal{p90, p93}); got != "" {
		t.Errorf("#90 was answered by #93 with the same reason, still shown: %s", got)
	}
	// nothing later: it needs a new proposal, in German, git only in the details
	got := staleLines([]*proposal.Proposal{p90, {ID: 91, Status: "approved", Files: []string{"box.yml"}}})
	head, details, _ := strings.Cut(got, "<details")
	if !strings.Contains(head, "#90") || !strings.Contains(head, "neu vorschlagen") || strings.Contains(head, "CONFLICT") || strings.Contains(head, "policy.yml") {
		t.Errorf("the line must say in German what to do, without git's output: %s", head)
	}
	if !strings.Contains(details, "Merge conflict in policy.yml") {
		t.Errorf("git's output belongs in the details: %s", got)
	}
	// it changes nothing any more: nothing to do
	done := &proposal.Proposal{ID: 96, Status: "stale", StaleReason: "ändert auf dem aktuellen Stand nichts mehr: das steht schon so"}
	if got := staleLines([]*proposal.Proposal{done}); got != "" {
		t.Errorf("a proposal whose change is already there is not something to do: %s", got)
	}
	// re-filed as #95, still open: the line says why the owner decides again
	p94 := &proposal.Proposal{ID: 94, Status: "stale", Refiled: 95, Files: []string{"policy.yml"}, StaleReason: "geändert seit deiner Freigabe — neu vorgeschlagen als #95"}
	p95 := &proposal.Proposal{ID: 95, Status: "open", Files: []string{"policy.yml"}}
	if got := staleLines([]*proposal.Proposal{p94, p95}); !strings.Contains(got, "#94 nicht angewendet") {
		t.Errorf("the re-filed proposal's line is gone while #95 waits: %q", got)
	}
	p95.Status = "approved"
	if got := staleLines([]*proposal.Proposal{p94, p95}); got != "" {
		t.Errorf("#95 is decided, #94's line stays: %s", got)
	}
}
