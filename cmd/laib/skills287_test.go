package main

import (
	"slices"
	"testing"

	"github.com/mdopp/laib/internal/mcp"
)

// #287: the maintenance skills are built in, accepted and granted what
// they need at the default ceiling — never apply: updates are proposals.
func TestTheMaintenanceSkillsAreBuiltIn(t *testing.T) {
	env, st := skillBox(t, "apply", nil, nil)
	sks := mcp.SkillsFor(env, st, "ops")
	for name, want := range map[string][]string{"aktualisierung": {"updates", "propose", "note"}, "sicherung": {"backup", "journal", "note"}} {
		sk := findSkill(sks, name)
		if sk == nil || sk.Status != "active" {
			t.Fatalf("%s: %+v", name, sk)
		}
		for _, w := range want {
			if !slices.Contains(sk.Granted, w) {
				t.Errorf("%s lacks %s: %v", name, w, sk.Granted)
			}
		}
		if slices.Contains(sk.Granted, "apply") || slices.Contains(sk.Granted, "restart") {
			t.Errorf("%s may change the box itself: %v", name, sk.Granted)
		}
	}
}
