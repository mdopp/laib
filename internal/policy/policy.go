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

// NeedsHuman reports whether a tier always needs a human approval.
// `destroy` always does (ADR 0006); others follow approvals in policy.yml.
func NeedsHuman(p state.Policy, tier string) bool {
	if tier == "destroy" {
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
