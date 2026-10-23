// Package policy answers "may this hand do this tier?" from policy.yml.
package policy

import (
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/mdopp/laib/internal/state"
)

// Tiers in their canonical order. policy.yml may list a subset in this order.
var Tiers = []string{"read", "propose", "apply", "destroy"}

// Index returns the position of a tier, or -1.
func Index(tier string) int { return slices.Index(Tiers, tier) }

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
	if e := p.Envelope.Snapshot; e != "" && e != "required" && e != "optional" {
		return fmt.Errorf("policy.yml: envelope.snapshot must be required or optional")
	}
	for name, v := range p.Skills {
		if v != "active" && v != "paused" {
			return fmt.Errorf("policy.yml: skills.%s must be active or paused", name)
		}
	}
	for name, h := range p.Hands {
		for _, v := range h.Verbs {
			if !KnownVerbs[v] {
				return fmt.Errorf("policy.yml: hand %q lists unknown verb %q", name, v)
			}
		}
		if Index(h.Ceiling) < 0 {
			return fmt.Errorf("policy.yml: hand %q has unknown ceiling %q", name, h.Ceiling)
		}
		if name == "resident" && Index(h.Ceiling) > Index("read") {
			return fmt.Errorf("policy.yml: resident may never exceed read")
		}
		if name != "human" && h.Ceiling == "destroy" {
			return fmt.Errorf("policy.yml: only human may hold destroy")
		}
		if h.Session != "" {
			// a session token always expires, and it is never a door to more sessions (#380)
			t, ok := p.Hands[h.Session]
			d, err := time.ParseDuration(t.TTL)
			if !ok || h.Session == "human" || t.Session != "" || err != nil || d <= 0 {
				return fmt.Errorf("policy.yml: hand %q: session %q must be a hand with a ttl, not human, without sessions of its own", name, h.Session)
			}
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

// KnownVerbs is every verb of the CLI (cmd/laib's table), so policy.yml can
// name them in a hand's verb list and be validated without importing it.
var KnownVerbs = map[string]bool{"status": true, "inbox": true, "diag": true, "repair": true, "plan": true, "apply": true, "inspect": true, "read": true, "look": true, "write": true, "exec": true, "restart": true, "start": true, "stop": true, "backups": true, "escalate": true, "note": true, "operator": true, "logs": true, "rollback": true, "backup": true, "restore": true, "converge": true, "journal": true, "context": true, "template": true, "add": true, "remove": true, "propose": true, "approve": true, "reject": true, "adopt": true, "ignore": true, "token": true, "secret": true, "disk": true, "init": true, "serve": true, "version": true, "skill": true, "updates": true, "net": true, "person_invite": true, "person_pending": true, "person_signal": true, "session": true, "calendar": true, "zuhause": true, "scan": true, "state": true}

// MayVerb reports whether a hand may use a verb at all: a hand with a verb
// list is limited to it (the approver: approve, reject, inbox, status,
// note), a hand without one may use anything its ceiling reaches (#48).
func MayVerb(p state.Policy, hand, verb string) bool {
	h, ok := p.Hands[hand]
	if !ok {
		return false
	}
	return len(h.Verbs) == 0 || slices.Contains(h.Verbs, verb)
}

// IsHuman reports whether a hand's verdicts count as a human's: the human
// on the shell, and a hand policy.yml marks `human: true` — the relay that
// acts only on the admin's verified Signal number (#48).
func IsHuman(p state.Policy, hand string) bool {
	return hand == "human" || p.Hands[hand].Human
}

// Missing names, per built-in hand, the verbs state.DefaultPolicy gives it
// that cur lacks (#575). It only ever adds: a hand without a verb list may
// already use every verb (a list would narrow it), a hand the owner removed
// stays removed, a hand the owner added is no built-in.
func Missing(cur state.Policy) map[string][]string {
	var def state.Policy
	yaml.Unmarshal([]byte(state.DefaultPolicy), &def)
	out := map[string][]string{}
	for name, h := range def.Hands {
		have, ok := cur.Hands[name]
		if !ok || len(have.Verbs) == 0 {
			continue
		}
		for _, v := range h.Verbs {
			if !slices.Contains(have.Verbs, v) {
				out[name] = append(out[name], v)
			}
		}
	}
	return out
}

// Grant adds verbs to one hand's one-line verb list in policy.yml's text:
// a line edit, every comment and every other line stays. A list it cannot
// find on one line, or an edit that would change anything else, is refused.
func Grant(text []byte, hand string, verbs ...string) ([]byte, error) {
	re := regexp.MustCompile(`(?m)^([ \t]+` + regexp.QuoteMeta(hand) + `:[ \t]*\{.*\bverbs:[ \t]*\[)([^\]\n]*)\]`)
	if n := len(re.FindAllIndex(text, -1)); n != 1 {
		return nil, fmt.Errorf("policy.yml: hand %q has no one-line verb list to add to (found %d)", hand, n)
	}
	out := re.ReplaceAll(text, []byte("${1}${2}, "+strings.Join(verbs, ", ")+"]"))
	var before, after state.Policy
	if yaml.Unmarshal(text, &before) != nil || yaml.Unmarshal(out, &after) != nil || before.Hands == nil {
		return nil, fmt.Errorf("policy.yml does not parse")
	}
	h := before.Hands[hand]
	h.Verbs = append(h.Verbs, verbs...)
	before.Hands[hand] = h
	if !reflect.DeepEqual(before, after) {
		return nil, fmt.Errorf("policy.yml: adding %v to %q would change more than its verb list", verbs, hand)
	}
	return out, nil
}
