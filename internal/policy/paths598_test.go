package policy

import (
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/state"
)

// #598: a hand with a verb list proposes only the box.yml paths policy.yml
// grants it — * is one segment, a pattern names a path, not its subtree; a
// hand with propose and no verb list proposes anything, like claude.
func TestMayPathMatchesGrantedPathsOnly(t *testing.T) {
	p := state.Policy{Hands: map[string]state.Hand{
		"haus":     {Ceiling: "apply", Verbs: []string{"zuhause"}, Paths: []string{"zuhause", "people.*.signal", "services/*/values.yml:SCANNER"}},
		"claude":   {Ceiling: "apply"},
		"resident": {Ceiling: "read"},
	}}
	for at, want := range map[string]bool{
		"box.yml:people.mama.signal":         true,
		"people.mama.signal":                 true, // box.yml is the default file
		"box.yml:zuhause":                    true,
		"services/scan/values.yml:SCANNER":   true,
		"box.yml:people.mama.telegram":       false,
		"box.yml:people.mama":                false,
		"box.yml:people.mama.signal.x":       false,
		"box.yml:zuhause.city":               false,
		"box.yml:domain":                     false,
		"policy.yml:people.mama.signal":      false,
		"services/scan/values.yml:SCAN_PATH": false,
	} {
		if got := MayPath(p, "haus", at); got != want {
			t.Errorf("haus %s: %v, want %v", at, got, want)
		}
	}
	if !MayPath(p, "claude", "box.yml:domain") || MayPath(p, "resident", "box.yml:zuhause") || MayPath(p, "nobody", "box.yml:zuhause") {
		t.Fatal("claude proposes any path, a reader and an unknown hand none")
	}
}

// #598: the default sync names a built-in hand's missing path grants like its
// verbs, and Grant adds a paths list to a one-line hand that has none.
func TestMissingAndGrantCoverPaths(t *testing.T) {
	cur := state.Policy{Hands: map[string]state.Hand{"haus": {Ceiling: "apply", Verbs: []string{"zuhause"}}, "claude": {Ceiling: "apply"}}}
	if got := Missing(cur, true); strings.Join(got["haus"], ",") != "zuhause,people.*.signal" || len(got) != 1 {
		t.Fatalf("%v", got)
	}
	text := "hands:\n  haus: {ceiling: apply, verbs: [zuhause]}   # the family's pages\n"
	out, err := Grant([]byte(text), "haus", true, "zuhause", "people.*.signal")
	if err != nil || string(out) != "hands:\n  haus: {ceiling: apply, verbs: [zuhause], paths: [zuhause, people.*.signal]}   # the family's pages\n" {
		t.Fatalf("%v: %s", err, out)
	}
	if out, err = Grant(out, "haus", true, "notify.person"); err != nil || !strings.Contains(string(out), "paths: [zuhause, people.*.signal, notify.person]}") {
		t.Fatalf("%v: %s", err, out)
	}
}
