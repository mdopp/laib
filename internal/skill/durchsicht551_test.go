package skill

import (
	"os"
	"slices"
	"strings"
	"testing"
)

// #550 point 6: durchsicht starts from the units' logs, which the runner
// reads itself (context logs); the kernel checks that it may (status for
// the services, logs for their lines) and that it reaches no acting verb.
func TestTheLogReviewSkillIsChecked(t *testing.T) {
	d := Find(Load(os.DirFS("../../skills"), t.TempDir()), "durchsicht")
	if d == nil || d.Refused != "" || d.Context != "logs" || d.Trigger.Daily == "" {
		t.Fatalf("the built-in durchsicht: %+v", d)
	}
	for _, tool := range d.Tools {
		if !slices.Contains([]string{"status", "logs", "wish", "note"}, tool) {
			t.Errorf("durchsicht reads and writes notes or wishes only, it names %s", tool)
		}
	}
	for yml, why := range map[string]string{
		"description: x\ntrigger: {daily: \"06:30\"}\ntools: [note, status]\ncontext: logs\noutput: note\nprompt: p\n":       "logs",
		"description: x\ntrigger: {daily: \"06:30\"}\ntools: [note, logs]\ncontext: logs\noutput: note\nprompt: p\n":         "status",
		"description: x\ntrigger: {daily: \"06:30\"}\ntools: [note, logs, status]\ncontext: logs\noutput: note\nprompt: p\n": "",
	} {
		s := parse("x", "state", []byte(yml), nil)
		if why == "" && s.Refused != "" || why != "" && !strings.Contains(s.Refused, why) {
			t.Errorf("%q: refused %q, want %q", yml, s.Refused, why)
		}
	}
}
