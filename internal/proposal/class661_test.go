package proposal_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/review"
)

// #661: grouping changes the words, not the gate. A template update of
// #102's shape — 126 script lines Laib cannot judge — is still class human.
func TestGroupedReviewKeepsTheGate(t *testing.T) {
	base, changed := t.TempDir(), t.TempDir()
	for _, d := range []string{base, changed} {
		if err := os.CopyFS(d, os.DirFS("../../testdata/golden/basic/state")); err != nil {
			t.Fatal(err)
		}
	}
	var py []byte
	for i := range 107 {
		py = append(py, []byte("COUNT_"+string(rune('a'+i%26))+string(rune('a'+i/26))+" = 2\n")...)
	}
	if err := os.WriteFile(filepath.Join(changed, "services/auth/anmeldungen.py"), py, 0o644); err != nil {
		t.Fatal(err)
	}
	r := review.Dirs(base, changed)
	v := proposal.Verdict{Human: r.Human, Lines: r.Lines}
	if len(r.Human) != 1 {
		t.Errorf("want one grouped reason, got %d: %q", len(r.Human), r.Human)
	}
	if got := proposal.ClassOf("template-update", v); got != "human" {
		t.Errorf("class %q, want human", got)
	}
	if got := proposal.ClassOf("template-update", proposal.Verdict{}); got != "template-update" {
		t.Errorf("without a reason the kind stays the class: %q", got)
	}
}
