package skill

import (
	"os"
	"slices"
	"testing"
)

// #550 point 6: durchsicht starts from the units' logs, which its script
// reads itself (run.py since #839); it reaches no acting verb. #915: it
// reads ignore.yml with state, a read verb; #975: the journal, which
// accounts for a planned stop.
func TestTheLogReviewSkillIsChecked(t *testing.T) {
	d := Find(Load(os.DirFS("../../skills"), t.TempDir()), "durchsicht")
	if d == nil || d.Refused != "" || d.Run != "run.py" || d.Trigger.Daily == "" {
		t.Fatalf("the built-in durchsicht: %+v", d)
	}
	for _, tool := range d.Tools {
		if !slices.Contains([]string{"status", "logs", "state", "journal", "wish", "note"}, tool) {
			t.Errorf("durchsicht reads and writes notes or wishes only, it names %s", tool)
		}
	}
}
