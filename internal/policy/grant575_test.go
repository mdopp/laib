package policy

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/mdopp/laib/internal/state"
)

// #575: the defaults live in one place (state.DefaultPolicy, what laib init
// writes); Missing names the verbs a built-in hand lacks, Grant adds them as
// a line edit that keeps every comment.
func TestDefaultPolicyIsTheOnePlace(t *testing.T) {
	var p state.Policy
	if err := yaml.Unmarshal([]byte(state.DefaultPolicy), &p); err != nil {
		t.Fatal(err)
	}
	if err := Validate(p); err != nil {
		t.Fatal(err)
	}
	if !MayVerb(p, "haus", "scan") || !MayVerb(p, "haus", "note") || len(Missing(p, false))+len(Missing(p, true)) != 0 { // note: Haus journals who switched in HA (#591)
		t.Fatalf("the defaults lack their own verbs: %v", Missing(p, false))
	}
	if strings.Join(p.Hands["laib"].Verbs, ",") != strings.Join(state.BoxHand.Verbs, ",") || strings.Join(p.Hands["notify"].Verbs, ",") != strings.Join(state.NotifyHand.Verbs, ",") {
		t.Fatal("the hands Load supplies are not the defaults")
	}
}

func TestMissingOnlyAddsToBuiltInVerbLists(t *testing.T) {
	cur := state.Policy{Hands: map[string]state.Hand{
		"haus":   {Ceiling: "apply", Verbs: []string{"status", "inbox"}},
		"claude": {Ceiling: "apply"},                            // no list: every verb, a list would narrow it
		"notify": {Ceiling: "propose", Verbs: []string{"diag"}}, // the owner's own extra verbs stay
		"kiosk":  {Ceiling: "read", Verbs: []string{"status"}},  // the owner's hand
	}}
	got := Missing(cur, false)
	if strings.Join(got["haus"], ",") != "journal,restart,person_invite,person_pending,person_signal,calendar,zuhause,scan,note" || strings.Join(got["notify"], ",") != "updates,inbox,note" || len(got) != 2 {
		t.Fatalf("%v", got)
	}
}

func TestGrantIsALineEdit(t *testing.T) {
	text := "tiers: [read]\n# a comment the owner wrote\nhands:\n  haus: {ceiling: apply, verbs: [status, inbox]}   # the family's pages\n  kiosk: {ceiling: read}\napprovals:\n  destroy: {by: human}\n"
	out, err := Grant([]byte(text), "haus", false, "scan", "journal")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(text, "[status, inbox]", "[status, inbox, scan, journal]", 1)
	if string(out) != want {
		t.Fatalf("got\n%s\nwant\n%s", out, want)
	}
	for hand, why := range map[string]string{"kiosk": "no verb list", "nobody": "no such hand", "destroy": "not a hand"} {
		if _, err := Grant([]byte(text), hand, false, "scan"); err == nil {
			t.Fatalf("%s (%s): granted", hand, why)
		}
	}
	block := "hands:\n  haus:\n    ceiling: apply\n    verbs:\n      - status\n"
	if _, err := Grant([]byte(block), "haus", false, "scan"); err == nil {
		t.Fatal("a block list was edited blindly")
	}
}
