package policy

import (
	"testing"

	"github.com/mdopp/laib/internal/state"
)

// The mutants scripts/ratchet.py mutation found alive here (#1167): each
// case below goes red when its condition is flipped.

// Invariant (e): who needs a human is exact in both directions.
func TestNeedsHumanIsExact(t *testing.T) {
	p := state.Policy{Approvals: map[string]state.Approval{"apply": {By: "human"}, "propose": {By: "any"}}}
	cases := []struct {
		tier, class string
		want        bool
	}{
		{"destroy", "values", true}, {"propose", "human", true}, {"propose", "remove", true},
		{"apply", "values", true}, {"propose", "values", false}, {"read", "values", false},
	}
	for _, c := range cases {
		if got := NeedsHuman(p, c.tier, c.class); got != c.want {
			t.Errorf("NeedsHuman(%s, %s) = %v, want %v", c.tier, c.class, got, c.want)
		}
	}
}

func TestValidateTakesItsBoundsExactly(t *testing.T) {
	ttl := func(d string) state.Policy {
		return state.Policy{Hands: map[string]state.Hand{"claude": {Ceiling: "apply", Session: "s"}, "s": {Ceiling: "propose", TTL: d}}}
	}
	ok := []state.Policy{
		{Envelope: state.Envelope{Snapshot: "required"}}, {Envelope: state.Envelope{Snapshot: "optional"}},
		{Envelope: state.Envelope{Budget: 1000}}, ttl("1s"),
	}
	for i, p := range ok {
		if err := Validate(p); err != nil {
			t.Errorf("ok case %d refused: %v", i, err)
		}
	}
	bad := []state.Policy{
		{Envelope: state.Envelope{Snapshot: "never"}}, {Envelope: state.Envelope{Budget: 1001}},
		{Envelope: state.Envelope{Budget: -1}}, ttl("0s"),
	}
	for i, p := range bad {
		if Validate(p) == nil {
			t.Errorf("bad case %d accepted", i)
		}
	}
}

func TestBudgetDefaultsToEight(t *testing.T) {
	if Budget(state.Policy{}) != DefaultBudget || Budget(state.Policy{Envelope: state.Envelope{Budget: 20}}) != 20 {
		t.Fatal("budget misread")
	}
}

func TestGrantRefusesAHandOnTwoLines(t *testing.T) {
	text := "hands:\n  homes: {ceiling: apply, verbs: [status]}\nother:\n  homes: {ceiling: read, verbs: [inbox]}\n"
	if _, err := Grant([]byte(text), "homes", false, "scan"); err == nil {
		t.Fatal("granted to one of two lines")
	}
}
