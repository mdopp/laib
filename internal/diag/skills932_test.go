package diag

import (
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/skill"
	"github.com/mdopp/laib/skills"
)

// #932: once the update run filed the grant a skill's tool needs, diag names
// that proposal instead of the command that would file it a second time.
func TestDiagNamesTheOpenGrantProposal(t *testing.T) {
	reach := func(v string) string {
		if v == "state" {
			return "policy.yml beschränkt ops auf status, logs, wish, note"
		}
		return ""
	}
	sks := skill.Grant(skill.Load(skills.FS, t.TempDir()), nil, reach)
	open := []*proposal.Proposal{{ID: 286, Status: "open", Reason: "Neue Standard-Verben für eingebaute Hände: ops +state (Fähigkeit durchsicht)"}}
	fs := skillFindings(sks, open...)
	if len(fs) != 1 || !strings.Contains(fs[0].Detail, "durchsicht") {
		t.Fatalf("want durchsicht's finding: %+v", fs)
	}
	if !strings.Contains(fs[0].Fix, "#286") || strings.Contains(fs[0].Fix, "laib propose --grant ops state") {
		t.Fatalf("the fix should name #286, not propose it again: %s", fs[0].Fix)
	}
}
