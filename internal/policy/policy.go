// Package policy answers "may this hand do this tier?" from policy.yml.
package policy

import (
	"fmt"

	"github.com/mdopp/laib/internal/state"
)

// Tiers in their canonical order. policy.yml may list a subset in this order.
var Tiers = []string{"read", "propose", "apply", "destroy"}

// Index returns the position of a tier, or -1.
func Index(tier string) int {
	for i, t := range Tiers {
		if t == tier {
			return i
		}
	}
	return -1
}

// Allowed reports whether the hand's ceiling reaches the tier. An unknown
// hand or tier is never allowed. "human" is a hand like any other.
func Allowed(p state.Policy, hand, tier string) bool {
	h, ok := p.Hands[hand]
	if !ok {
		return false
	}
	want, have := Index(tier), Index(h.Ceiling)
	return want >= 0 && have >= want
}

// NeedsHuman reports whether only a human may approve a proposal: `destroy`
// always (ADR 0006), class `human` always (the content said so, #34), and
// tiers whose approvals say `by: human` in policy.yml. The dial (Auto) is
// laib's own delegation and is never consulted for these.
func NeedsHuman(p state.Policy, tier, class string) bool {
	if tier == "destroy" || class == "human" || class == "remove" {
		return true
	}
	a, ok := p.Approvals[tier]
	return ok && a.By == "human"
}

// Validate checks the tier list and ceilings.
func Validate(p state.Policy) error {
	for _, t := range p.Tiers {
		if Index(t) < 0 {
			return fmt.Errorf("policy.yml: unknown tier %q", t)
		}
	}
	for class, v := range p.Autonomy {
		switch class {
		case "converge":
			if v != "on" && v != "off" {
				return fmt.Errorf("policy.yml: autonomy.converge must be on or off")
			}
		case "values", "template-update", "new-service", "patch", "remove":
			if v != "auto" && v != "proposal" {
				return fmt.Errorf("policy.yml: autonomy.%s must be auto or proposal", class)
			}
			if class == "remove" && v == "auto" {
				return fmt.Errorf("policy.yml: autonomy.remove can never be auto (ADR 0006)")
			}
		default:
			return fmt.Errorf("policy.yml: unknown autonomy class %q", class)
		}
	}
	for name, h := range p.Hands {
		if Index(h.Ceiling) < 0 {
			return fmt.Errorf("policy.yml: hand %q has unknown ceiling %q", name, h.Ceiling)
		}
		if name == "resident" && Index(h.Ceiling) > Index("read") {
			return fmt.Errorf("policy.yml: resident may never exceed read")
		}
		if name != "human" && h.Ceiling == "destroy" {
			return fmt.Errorf("policy.yml: only human may hold destroy")
		}
	}
	return nil
}

// Auto reports whether the dial lets laib approve a proposal class itself.
// Never for human or remove, whatever the dial says.
func Auto(p state.Policy, class string) bool {
	return class != "human" && class != "remove" && p.Autonomy[class] == "auto"
}

// ConvergeOn reports whether the converge loop is enabled (default off).
func ConvergeOn(p state.Policy) bool { return p.Autonomy["converge"] == "on" }

// ConvergeBudget is how many converge applies per hour are allowed before
// laib stops and asks a human: a flapping service is a bug, not a chore.
const ConvergeBudget = 3
