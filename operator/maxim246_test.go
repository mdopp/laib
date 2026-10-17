package operator

import (
	"strings"
	"testing"
)

func defsNamed(names ...string) []ToolDef {
	var out []ToolDef
	for _, n := range names {
		var d ToolDef
		d.Function.Name = n
		out = append(out, d)
	}
	return out
}

// The owner's maxim (#246): a correction or proposal is weighed against what
// worked before; at least one of maintenance and the family's comfort gets
// better, neither noticeably worse, and a proposal says which.
func TestOperatorWeighsAChangeAgainstWhatWorkedBefore(t *testing.T) {
	for name, p := range map[string]string{
		"free":  SystemPrompt(defsNamed("laib_status", "laib_restart", "laib_propose", "laib_note")),
		"typed": TypedSystemPrompt(defsNamed("restart", "set_value", "escalate")),
	} {
		for _, want := range []string{"was vorher funktioniert hat", "leichter zu warten", "angenehmer", "keins von beiden spürbar schlechter"} {
			if !strings.Contains(p, want) {
				t.Errorf("%s prompt lacks %q:\n%s", name, want, p)
			}
		}
	}
	// a session that can only look changes nothing, so it is not told to weigh changes
	if p := SystemPrompt(defsNamed("laib_status", "laib_note")); strings.Contains(p, "was vorher funktioniert hat") {
		t.Errorf("a look-only prompt carries the change rule:\n%s", p)
	}
}
