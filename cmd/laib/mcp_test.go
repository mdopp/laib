package main

import (
	"reflect"
	"testing"

	"github.com/mdopp/laib/internal/state"
)

func TestMcpToolsFollowTheCeiling(t *testing.T) {
	st := &state.State{Policy: state.Policy{Hands: map[string]state.Hand{"ops": {Ceiling: "propose"}, "claude": {Ceiling: "apply"}}}}
	names := func(hand string) []string {
		var out []string
		for _, tl := range mcpTools(st, hand) {
			out = append(out, tl["name"].(string))
		}
		return out
	}
	ops := names("ops")
	for _, n := range ops {
		if n == "laib_apply" || n == "laib_approve" || n == "laib_rollback" || n == "laib_secret" || n == "laib_token" || n == "laib_serve" {
			t.Fatalf("ops sees %s", n)
		}
	}
	claude := names("claude")
	if len(claude) <= len(ops) {
		t.Fatalf("claude %v should see more than ops %v", claude, ops)
	}
	if names("nobody") != nil {
		t.Fatal("unknown hand sees tools")
	}
}

func TestSplitArgs(t *testing.T) {
	got := splitArgs(`media --since 2h "zwei Wörter" x`)
	want := []string{"media", "--since", "2h", "zwei Wörter", "x"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%q", got)
	}
	if splitArgs("") != nil {
		t.Fatal("empty must be nil")
	}
}
