package main

import (
	"context"
	"reflect"
	"testing"

	"github.com/mdopp/laib/internal/state"
)

// A client can name any tool, listed or not: the dispatcher itself must
// refuse verbs above the hand's ceiling and verbs that are not MCP verbs at
// all, before anything runs (#52).
func TestMcpCallEnforcesTheBoundary(t *testing.T) {
	st := &state.State{Policy: state.Policy{Hands: map[string]state.Hand{"bote": {Ceiling: "propose"}, "human": {Ceiling: "destroy"}}}}
	for _, tc := range []struct {
		hand, tool string
		want       int
	}{
		{"bote", "laib_write", exitRefused},
		{"bote", "laib_exec", exitRefused},
		{"bote", "laib_approve", exitRefused},
		{"bote", "laib_restore", exitRefused},
		{"nobody", "laib_status", exitRefused},
		{"human", "laib_token", exitUsage}, // the box's own business, never over MCP
		{"human", "laib_secret", exitUsage},
		{"human", "laib_disk", exitUsage},
		{"human", "laib_remove", exitUsage},
		{"human", "laib_serve", exitUsage},
		{"human", "laib_init", exitUsage},
		{"bote", "laib_nonsense", exitUsage},
	} {
		out, code := mcpCall(context.Background(), &cliEnv{}, st, tc.hand, tc.tool, map[string]any{"args": ""})
		if code != tc.want {
			t.Errorf("%s calling %s: exit %d (%q), want %d", tc.hand, tc.tool, code, out, tc.want)
		}
	}
}

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
