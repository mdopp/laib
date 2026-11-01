package state

import (
	"slices"
	"testing"
)

// #978: what a service's lines may follow from is the contract's, in one
// place — the log review's hand-kept map (#975) held these by a test.
func TestDependsIsAfterUsesNeedsAndTheSSORoleTransitively(t *testing.T) {
	svc := func(c Contract) *Service { return &Service{Contract: c} }
	st := &State{Box: Box{Roles: Roles{SSO: "auth"}}, Services: map[string]*Service{
		"haus":           svc(Contract{After: []string{"auth"}, Needs: []string{"mcp"}, Uses: []string{"home-assistant"}}),
		"home-assistant": svc(Contract{After: []string{"auth", "mosquitto"}, OIDC: &OIDC{}}),
		"whoami":         svc(Contract{OIDC: &OIDC{}}),
		"auth":           svc(Contract{OIDC: &OIDC{}}), // the portal itself: not its own dependency
		"mosquitto":      svc(Contract{}),
	}}
	for name, want := range map[string][]string{
		"haus":   {"auth", "home-assistant", "laib", "mosquitto"},
		"whoami": {"auth"},
		"auth":   nil,
		"gone":   nil,
	} {
		if got := st.Depends(name); !slices.Equal(got, want) {
			t.Errorf("%s depends on %v, want %v", name, got, want)
		}
	}
	if got := st.Dependents("home-assistant"); !slices.Equal(got, []string{"haus"}) {
		t.Errorf("home-assistant's dependents: %v", got)
	}
}
