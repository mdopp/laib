package diag

import (
	"slices"
	"testing"

	"github.com/mdopp/laib/internal/state"
)

// #978: what a service's lines may follow from is the contract's, in one
// place — the log review's hand-kept map (#975) held these by a test.
func TestDependsIsAfterUsesNeedsAndTheSSORoleTransitively(t *testing.T) {
	svc := func(c state.Contract) *state.Service { return &state.Service{Contract: c} }
	st := &state.State{Box: state.Box{Roles: state.Roles{SSO: "auth"}}, Services: map[string]*state.Service{
		"haus":           svc(state.Contract{After: []string{"auth"}, Needs: []string{"mcp"}, Uses: []string{"home-assistant"}}),
		"home-assistant": svc(state.Contract{After: []string{"auth", "mosquitto"}, OIDC: &state.OIDC{}}),
		"whoami":         svc(state.Contract{OIDC: &state.OIDC{}}),
		"auth":           svc(state.Contract{OIDC: &state.OIDC{}}), // the portal itself: not its own dependency
		"mosquitto":      svc(state.Contract{}),
	}}
	for name, want := range map[string][]string{
		"haus":   {"auth", "home-assistant", "laib", "mosquitto"},
		"whoami": {"auth"},
		"auth":   nil,
		"gone":   nil,
	} {
		if got := Depends(st, name); !slices.Equal(got, want) {
			t.Errorf("%s depends on %v, want %v", name, got, want)
		}
	}
}
