package skill

import (
	"os"
	"slices"
	"testing"
)

// #550 point 6: durchsicht starts from the units' logs, which its script
// reads itself (run.py since #839); it reaches no acting verb.
func TestTheLogReviewSkillIsChecked(t *testing.T) {
	d := Find(Load(os.DirFS("../../skills"), t.TempDir()), "durchsicht")
	if d == nil || d.Refused != "" || d.Run != "run.py" || d.Trigger.Daily == "" {
		t.Fatalf("the built-in durchsicht: %+v", d)
	}
	for _, tool := range d.Tools {
		if !slices.Contains([]string{"status", "logs", "wish", "note"}, tool) {
			t.Errorf("durchsicht reads and writes notes or wishes only, it names %s", tool)
		}
	}
}
