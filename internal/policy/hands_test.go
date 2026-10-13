package policy

import (
	"testing"

	"github.com/mdopp/laib/internal/state"
)

// #48: a hand may be limited to a few verbs (the approver: approve, reject,
// inbox, status, note), and a hand may count as a human — the relay behind
// the admin's verified Signal number — so its verdicts decide what only a
// human may allow.
func TestHandVerbsAndHumanStatus(t *testing.T) {
	p := state.Policy{Hands: map[string]state.Hand{
		"ops":      {Ceiling: "apply"},
		"approver": {Ceiling: "apply", Human: true, Verbs: []string{"approve", "reject", "inbox", "status", "note"}},
		"human":    {Ceiling: "destroy"},
	}}
	if !MayVerb(p, "ops", "exec") || !MayVerb(p, "ops", "approve") {
		t.Fatal("a hand without a verb list may use every verb its ceiling reaches")
	}
	if !MayVerb(p, "approver", "approve") || !MayVerb(p, "approver", "inbox") {
		t.Fatal("the approver may approve and read the inbox")
	}
	for _, v := range []string{"exec", "write", "restore", "apply", "propose", "rollback"} {
		if MayVerb(p, "approver", v) {
			t.Fatalf("the approver may %s", v)
		}
	}
	if MayVerb(p, "nobody", "status") {
		t.Fatal("an unknown hand may use verbs")
	}
	if !IsHuman(p, "human") || !IsHuman(p, "approver") || IsHuman(p, "ops") {
		t.Fatal("human status wrong")
	}
	if err := Validate(state.Policy{Tiers: []string{"read"}, Hands: map[string]state.Hand{"x": {Ceiling: "read", Verbs: []string{"fly"}}}}); err == nil {
		t.Fatal("an unknown verb in a hand's list passed validation")
	}
}
