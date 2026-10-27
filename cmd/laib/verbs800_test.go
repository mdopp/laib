package main

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/proposal"
)

// #800: three lists named the verbs (mcp.Verbs, mcp.Allowed,
// policy.KnownVerbs) and drifted: policy.yml could not name flash, self or
// recovery-sheet. The verb table is the one list; what an agent reaches
// over MCP is a field on its row, what policy.yml may name is the table.
func TestTheVerbTableIsTheOnlyVerbList(t *testing.T) {
	c := newCbox(t)
	st, err := cli.LoadState(c.env)
	if err != nil {
		t.Fatal(err)
	}
	var table, exposed []string
	for _, v := range mcp.Verbs {
		table = append(table, v.Name)
		if v.MCP {
			exposed = append(exposed, v.Name)
		}
		if proposal.VerbTier[v.Name] != v.Tier {
			t.Errorf("proposal.VerbTier[%q] = %q, the table says %q", v.Name, proposal.VerbTier[v.Name], v.Tier)
		}
	}
	slices.Sort(table)
	slices.Sort(exposed)
	if known := slices.Sorted(maps.Keys(policy.KnownVerbs)); !slices.Equal(known, table) {
		t.Errorf("policy.KnownVerbs is not the verb table:\n known %v\n table %v", known, table)
	}
	var tools []string // human: ceiling destroy, no verb list, so every MCP verb
	for _, tool := range mcp.Tools(st, "human") {
		tools = append(tools, strings.TrimPrefix(tool["name"].(string), "laib_"))
	}
	slices.Sort(tools)
	if !slices.Equal(tools, exposed) {
		t.Errorf("the MCP tool list is not the table's MCP rows:\n tools %v\n rows  %v", tools, exposed)
	}
}

// What stays off MCP is a decision per verb, not a default: the box's own
// business (init, serve), a value into a transcript (secret, token), the
// humans-only verbs and the destroy tier at the console.
func TestTheVerbsNoAgentReaches(t *testing.T) {
	var off []string
	for _, v := range mcp.Verbs {
		if !v.MCP {
			off = append(off, v.Name)
		}
	}
	slices.Sort(off)
	want := []string{"add", "disk", "flash", "init", "operator", "recovery-sheet", "remove", "retrain", "secret", "self", "serve", "token"} // retrain: humans only, like flash (#828)
	if !slices.Equal(off, want) {
		t.Errorf("verbs off MCP: %v, want %v", off, want)
	}
}
