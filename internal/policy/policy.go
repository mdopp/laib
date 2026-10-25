// Package policy answers "may this hand do this tier?" from policy.yml.
package policy

import (
	"fmt"
	"path"
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
		// a verb this laib does not know is no error: a policy.yml a newer laib
		// wrote must load after a rollback, and a verb that is not in the verb
		// table can never be called (`laib template lint` and verbs543 catch typos)
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
var KnownVerbs = map[string]bool{"status": true, "inbox": true, "diag": true, "repair": true, "wish": true, "plan": true, "apply": true, "inspect": true, "read": true, "look": true, "write": true, "exec": true, "restart": true, "start": true, "stop": true, "backups": true, "escalate": true, "note": true, "operator": true, "logs": true, "rollback": true, "backup": true, "restore": true, "converge": true, "journal": true, "context": true, "template": true, "add": true, "remove": true, "propose": true, "approve": true, "reject": true, "adopt": true, "ignore": true, "token": true, "secret": true, "disk": true, "init": true, "serve": true, "version": true, "skill": true, "updates": true, "net": true, "person_invite": true, "person_pending": true, "person_signal": true, "session": true, "calendar": true, "zuhause": true, "scan": true, "state": true, "pause": true, "resume": true, "aufgabe": true, "tell": true}

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

// MayPath reports whether a hand may propose a value at a path
// ("box.yml:people.mama.signal"; no file means box.yml, #598): a hand with
// propose and no verb list anything, one with a list only what its paths
// grant — a pattern names one path, * stands for one segment.
func MayPath(p state.Policy, hand, at string) bool {
	h := p.Hands[hand]
	return Allowed(p, hand, "propose") && (len(h.Verbs) == 0 || slices.ContainsFunc(h.Paths, func(pat string) bool {
		ok, _ := path.Match(slashed(pat), slashed(at))
		return ok
	}))
}

// slashed is a path with its segments split by /, the way path.Match counts them.
func slashed(at string) string {
	file, dotted, ok := strings.Cut(at, ":")
	if !ok {
		file, dotted = "box.yml", at
	}
	return file + "/" + strings.ReplaceAll(dotted, ".", "/")
}

// list is a hand's verb list, or with paths its path grants.
func list(h *state.Hand, paths bool) *[]string {
	if paths {
		return &h.Paths
	}
	return &h.Verbs
}

// Missing names, per built-in hand, the verbs (or with paths the path
// grants) state.DefaultPolicy gives it that cur lacks (#575, #598). It only
// ever adds: a hand without a verb list may already use every verb and
// propose every path (a list would narrow it), a hand the owner removed
// stays removed, a hand the owner added is no built-in.
func Missing(cur state.Policy, paths bool) map[string][]string {
	var def state.Policy
	yaml.Unmarshal([]byte(state.DefaultPolicy), &def)
	out := map[string][]string{}
	for name, h := range def.Hands {
		have, ok := cur.Hands[name]
		if !ok || len(have.Verbs) == 0 {
			continue
		}
		for _, v := range *list(&h, paths) {
			if !slices.Contains(*list(&have, paths), v) {
				out[name] = append(out[name], v)
			}
		}
	}
	return out
}

// Grant adds verbs (or with paths path grants) to one hand's one-line list in
// policy.yml's text: a line edit, every comment and every other line stays.
// A missing paths list is added to the hand's line; a hand not on one line,
// one without a verb list, or an edit that would change anything else is refused.
func Grant(text []byte, hand string, paths bool, items ...string) ([]byte, error) {
	key := map[bool]string{false: "verbs", true: "paths"}[paths]
	locs := regexp.MustCompile(`(?m)^[ \t]+`+regexp.QuoteMeta(hand)+`:[ \t]*\{[^}\n]*\}`).FindAllIndex(text, -1)
	if len(locs) != 1 {
		return nil, fmt.Errorf("policy.yml: hand %q is not on one line to add to (found %d)", hand, len(locs))
	}
	line, add := string(text[locs[0][0]:locs[0][1]]), strings.Join(items, ", ")
	if m := regexp.MustCompile(`\b` + key + `:[ \t]*\[[^\]\n]*`).FindStringIndex(line); m != nil {
		line = line[:m[1]] + ", " + add + line[m[1]:]
	} else if paths {
		line = line[:len(line)-1] + ", paths: [" + add + "]}"
	} else {
		return nil, fmt.Errorf("policy.yml: hand %q has no verb list to add to", hand)
	}
	out := slices.Concat(text[:locs[0][0]], []byte(line), text[locs[0][1]:])
	var before, after state.Policy
	if yaml.Unmarshal(text, &before) != nil || yaml.Unmarshal(out, &after) != nil || before.Hands == nil {
		return nil, fmt.Errorf("policy.yml does not parse")
	}
	h := before.Hands[hand]
	*list(&h, paths) = append(*list(&h, paths), items...)
	before.Hands[hand] = h
	if !reflect.DeepEqual(before, after) {
		return nil, fmt.Errorf("policy.yml: adding %v to %q would change more than its %s", items, hand, key)
	}
	return out, nil
}
