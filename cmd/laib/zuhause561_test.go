package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/state"
)

// #561: Haus's form hands the household's address to laib (laib_zuhause
// set); it becomes a box.yml proposal only a human approves — Haus never
// decides, and neither the inbox reason nor the journal carries the street.
// Since #677 laib-haushalt serves it behind the door; the kernel's loader
// still judges the address and its review still makes it a human's.
func TestHausProposesTheAddressAndOnlyAHumanLetsItIn(t *testing.T) {
	env, _ := testBox(t)
	withHaushalt(t, env)
	call := func(hand string, args map[string]any) (string, int) {
		st, err := cli.LoadState(env)
		if err != nil {
			t.Fatal(err)
		}
		return mcp.Call(context.Background(), env, st, hand, "laib_zuhause", args)
	}
	show := func() map[string]any {
		out, code := call("haus", map[string]any{"action": "show"})
		var z map[string]any
		if code != cli.ExitOK || json.Unmarshal([]byte(out), &z) != nil {
			t.Fatalf("show: exit %d: %s", code, out)
		}
		return z
	}
	if z := show(); len(z) != 0 {
		t.Fatalf("a box without a zuhause block shows %v", z)
	}
	box, _ := os.ReadFile(filepath.Join(env.Paths.State, "box.yml"))

	out, code := call("haus", map[string]any{"action": "set", "street": "Musterweg 12", "postcode": "12345", "city": "Musterstadt",
		"country": "DE", "latitude": "52.1234", "longitude": "8.0456", "elevation": "63"})
	if code != cli.ExitOK {
		t.Fatalf("set: exit %d: %s", code, out)
	}
	var got struct {
		Proposal int    `json:"proposal"`
		Message  string `json:"message"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.Proposal == 0 || !strings.Contains(got.Message, "Freigabe") {
		t.Fatalf("%v: %s", err, out)
	}
	if now, _ := os.ReadFile(filepath.Join(env.Paths.State, "box.yml")); string(now) != string(box) {
		t.Fatal("set changed box.yml on main: a proposal waits for the owner")
	}
	if z := show(); len(z) != 0 {
		t.Fatalf("an open proposal is not yet the address: %v", z)
	}
	p, err := proposal.Get(cli.InboxDir(env), got.Proposal)
	if err != nil {
		t.Fatal(err)
	}
	if p.Class != "human" || !slices.Equal(p.Files, []string{"box.yml"}) || !strings.Contains(strings.Join(p.Reasons, " "), "Adresse") {
		t.Fatalf("the proposal must be a human's, box.yml only, saying it changes the address: %+v", p)
	}
	if strings.Contains(p.Reason, "Musterweg") || grepTree(env.Paths.Journal, "Musterweg") {
		t.Fatal("the street is in the inbox reason or the journal")
	}
	// Haus cannot let it in itself
	if _, code := mcp.Call(context.Background(), env, mustState(t, env), "haus", "laib_approve", map[string]any{"args": "1"}); code == cli.ExitOK {
		t.Fatal("the haus hand approved the address")
	}
	human := *env
	if code := inbox.Approve(context.Background(), &human, []string{"1"}); code != cli.ExitOK {
		t.Fatalf("approve: exit %d", code)
	}
	z := show()
	if z["street"] != "Musterweg 12" || z["city"] != "Musterstadt" || z["latitude"] != 52.1234 || z["longitude"] != 8.0456 || z["elevation"] != 63.0 {
		t.Fatalf("after the approval: %v", z)
	}
	st := mustState(t, env)
	if st.Box.Zuhause.Postcode != "12345" || st.Box.Domain != "box.test" {
		t.Fatalf("box.yml after the approval: %+v", st.Box)
	}
}

func mustState(t *testing.T, env *cliEnv) *state.State {
	t.Helper()
	st, err := cli.LoadState(env)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// a value the loader refuses never becomes a proposal; a resident cannot
// even read the address (#561)
func TestZuhauseRefusesBadValuesAndResidents(t *testing.T) {
	env, _ := testBox(t)
	withHaushalt(t, env)
	st := mustState(t, env)
	for _, args := range []map[string]any{
		{"action": "set", "city": "Musterstadt", "latitude": "52.1"},
		{"action": "set", "latitude": "nördlich", "longitude": "8"},
		{"action": "set", "latitude": "95", "longitude": "8"},
		{"action": "set"},
	} {
		if out, code := mcp.Call(context.Background(), env, st, "haus", "laib_zuhause", args); code == cli.ExitOK {
			t.Errorf("%v: accepted: %s", args, out)
		}
	}
	if all, _ := proposal.List(cli.InboxDir(env), false); len(all) != 0 {
		t.Fatalf("a refused address left %d proposals", len(all))
	}
	if _, code := mcp.Call(context.Background(), env, st, "resident", "laib_zuhause", map[string]any{"action": "show"}); code == cli.ExitOK {
		t.Fatal("the resident hand reads the household's address")
	}
}
