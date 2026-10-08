package policy

import (
	"testing"

	"github.com/mdopp/laib/internal/state"
)

func TestCeilings(t *testing.T) {
	p := state.Policy{Hands: map[string]state.Hand{"ops": {Ceiling: "propose"}, "human": {Ceiling: "destroy"}}}
	cases := []struct {
		hand, tier string
		want       bool
	}{
		{"ops", "read", true}, {"ops", "propose", true}, {"ops", "apply", false},
		{"human", "destroy", true}, {"nobody", "read", false}, {"ops", "bogus", false},
	}
	for _, c := range cases {
		if got := Allowed(p, c.hand, c.tier); got != c.want {
			t.Errorf("%s/%s = %v", c.hand, c.tier, got)
		}
	}
}

func TestValidateRefusesEscalation(t *testing.T) {
	bad := []state.Policy{
		{Hands: map[string]state.Hand{"resident": {Ceiling: "propose"}}},
		{Hands: map[string]state.Hand{"claude": {Ceiling: "destroy"}}},
		{Tiers: []string{"god"}},
	}
	for i, p := range bad {
		if Validate(p) == nil {
			t.Errorf("case %d accepted", i)
		}
	}
	if NeedsHuman(state.Policy{}, "destroy") != true {
		t.Fatal("destroy must always need a human")
	}
}
