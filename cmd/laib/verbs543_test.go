package main

import (
	"testing"

	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/policy"
)

// #543: diag told the owner to give ops `wish`, and the policy loader
// refused it: "hand "ops" lists unknown verb "wish"". net and scan were the
// first time (#598). Every verb an agent may reach is one the verb table
// runs and one policy.yml may name in a hand's verb list.
func TestEveryAgentVerbIsOnePolicyMayName(t *testing.T) {
	table := map[string]bool{}
	for _, v := range mcp.Verbs {
		table[v.Name] = true
	}
	for verb := range mcp.Allowed {
		if !table[verb] {
			t.Errorf("mcp.Allowed has %q, the verb table does not", verb)
		}
		if !policy.KnownVerbs[verb] {
			t.Errorf("policy.KnownVerbs lacks %q: a hand's verb list naming it does not load", verb)
		}
	}
}

// #751: ServiceBay's data dir went 2026-10-26, and its verb with it.
func TestServiceBayIsAnUnknownVerb(t *testing.T) {
	for _, v := range mcp.Verbs {
		if v.Name == "servicebay" {
			t.Fatal("the servicebay verb is still in the table")
		}
	}
	if rc := runArgs([]string{"servicebay", "remove"}); rc != 1 {
		t.Fatalf("laib servicebay remove: exit %d, want 1 (unknown verb)", rc)
	}
}
