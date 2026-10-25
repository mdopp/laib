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
	if NeedsHuman(state.Policy{}, "destroy", "tree") != true {
		t.Fatal("destroy must always need a human")
	}
}

func TestAutonomyDial(t *testing.T) {
	ok := state.Policy{Hands: map[string]state.Hand{"human": {Ceiling: "destroy"}}, Autonomy: map[string]string{"converge": "on", "values": "auto", "new-service": "proposal"}}
	if err := Validate(ok); err != nil {
		t.Fatal(err)
	}
	if !Auto(ok, "values") || Auto(ok, "new-service") || Auto(ok, "patch") || !ConvergeOn(ok) {
		t.Fatal("dial misread")
	}
	for _, bad := range []map[string]string{{"remove": "auto"}, {"converge": "yes"}, {"values": "maybe"}, {"nuke": "auto"}} {
		if Validate(state.Policy{Hands: ok.Hands, Autonomy: bad}) == nil {
			t.Fatalf("accepted %v", bad)
		}
	}
}

// ADR 0023 §5: policy.yml skills switches a skill active or paused, nothing else.
func TestSkillSwitchIsActiveOrPaused(t *testing.T) {
	ok := state.Policy{Skills: map[string]string{"routineblick": "paused", "nutzung": "active"}}
	if err := Validate(ok); err != nil {
		t.Fatal(err)
	}
	if err := Validate(state.Policy{Skills: map[string]string{"routineblick": "aus"}}); err == nil {
		t.Fatal("skills.routineblick: aus passed validation")
	}
}

// A policy.yml written by a newer laib may name verbs this laib does not
// know: a rollback must still load it (vm-install, train 2026-10-25d: the
// previous laib refused the whole policy over „pause“). An unknown verb in
// a list names nothing this laib can run, so it is no error.
func TestAnUnknownVerbInAHandsListIsSkippedNotFatal(t *testing.T) {
	p := state.Policy{Tiers: []string{"read", "propose", "apply", "destroy"}, Hands: map[string]state.Hand{
		"lease": {Ceiling: "apply", Verbs: []string{"status", "kommt-erst-spaeter"}}}}
	if err := Validate(p); err != nil {
		t.Fatalf("a newer verb made the policy unloadable: %v", err)
	}
}
