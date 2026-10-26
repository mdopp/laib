package policy

import (
	"testing"

	"github.com/mdopp/laib/internal/state"
)

// #48: a hand may be limited to a few verbs (notify: diag, updates, inbox,
// note). Since #778 only the human counts as one: a policy.yml from before
// that marks the relay's approver `human: true` loads, and the mark is ignored.
func TestHandVerbsAndHumanStatus(t *testing.T) {
	p := state.Policy{Hands: map[string]state.Hand{
		"ops":    {Ceiling: "apply"},
		"notify": {Ceiling: "propose", Verbs: []string{"diag", "updates", "inbox", "note"}},
		"human":  {Ceiling: "destroy"},
	}}
	if !MayVerb(p, "ops", "exec") || !MayVerb(p, "ops", "approve") {
		t.Fatal("a hand without a verb list may use every verb its ceiling reaches")
	}
	if !MayVerb(p, "notify", "inbox") || !MayVerb(p, "notify", "note") {
		t.Fatal("notify may read the inbox and note")
	}
	for _, v := range []string{"exec", "write", "restore", "apply", "propose", "rollback", "approve"} {
		if MayVerb(p, "notify", v) {
			t.Fatalf("notify may %s", v)
		}
	}
	if MayVerb(p, "nobody", "status") {
		t.Fatal("an unknown hand may use verbs")
	}
	if !IsHuman("human") || IsHuman("approver") || IsHuman("ops") {
		t.Fatal("human status wrong")
	}
	// an unknown verb no longer fails the policy: a rollback must load what a newer laib wrote
	// (TestAnUnknownVerbInAHandsListIsSkippedNotFatal); --grant still refuses one (#575)
}
