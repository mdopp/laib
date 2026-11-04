package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/state"
	"gopkg.in/yaml.v3"
)

// #1036 (homes ADR 0001 §5), through the real laib-haushalt behind the
// door: Homes' switch, laib module aus podcasts, becomes a proposal for
// services/homes/values.yml MODULE_AUS, filed by the haushalt hand under the
// door's ceiling. The box's dial says values: auto, and still it waits in the
// Inbox: nothing on main changes until someone approves it.
func TestHomesSwitchIsAProposalThatWaits(t *testing.T) {
	c := newCbox(t)
	ctx := context.Background()
	dst := filepath.Join(c.env.Paths.State, "services", "homes")
	copyDir(t, filepath.Join("..", "..", "templates", "homes"), dst)
	os.RemoveAll(filepath.Join(dst, "tests"))
	ex, _ := os.ReadFile(filepath.Join(dst, "values.example.yml"))
	os.WriteFile(filepath.Join(dst, "values.yml"), ex, 0o644)
	box := filepath.Join(c.env.Paths.State, "box.yml")
	b, _ := os.ReadFile(box)
	os.WriteFile(box, append(b, "laib: {listen: 0.0.0.0}\n"...), 0o644)
	if err := proposal.CommitDirty(ctx, c.env.Paths.State, "human:test"); err != nil {
		t.Fatal(err)
	}
	if st := mustState(t, c.env); st.Policy.Autonomy["values"] != "auto" {
		t.Fatalf("the test box's dial: %v", st.Policy.Autonomy)
	}
	c.intake(t, "", newFakeGitHub(t))
	c.env.Actor = "homes"
	var out bytes.Buffer
	c.env.Out = &out
	if rc := verb(t, "module").Run(ctx, c.env, []string{"aus", "podcasts,energie"}); rc != cli.ExitOK {
		t.Fatalf("rc %d: %s", rc, out.String())
	}
	all, _ := proposal.List(cli.InboxDir(c.env), false)
	if len(all) != 1 {
		t.Fatalf("one proposal: %d", len(all))
	}
	p := all[0]
	if p.Status != "open" || p.Actor != "haushalt" || p.Class != "values" || !slices.Equal(p.Files, []string{"services/homes/values.yml"}) {
		t.Fatalf("an open values proposal of the haushalt hand: %+v", p)
	}
	if len(p.Changes) != 1 || p.Changes[0].Key != "MODULE_AUS" || p.Changes[0].New != "podcasts,energie" {
		t.Fatalf("the one change: %+v", p.Changes)
	}
	if v, _ := os.ReadFile(filepath.Join(dst, "values.yml")); !bytes.Equal(v, ex) {
		t.Fatal("main changed before anyone approved")
	}
	if !strings.Contains(out.String(), "#1") {
		t.Fatalf("the answer names the proposal: %q", out.String())
	}
}

// #1036: the haushalt hand proposes that one value and no other of Homes'
// values.yml; the homes hand gets the verb, not the path, and the defaults
// sync brings the verb to a box whose homes line lacks it (#575).
func TestTheSwitchIsTheOnlyHomesValueTheDoorProposes(t *testing.T) {
	p := state.Policy{Tiers: policy.Tiers, Hands: map[string]state.Hand{"haushalt": state.HaushaltHand}}
	if !policy.MayPath(p, "haushalt", "services/homes/values.yml:MODULE_AUS") {
		t.Fatal("haushalt may not propose MODULE_AUS")
	}
	for _, at := range []string{"services/homes/values.yml:REITTI_URL", "services/homes/values.yml:HOMES_IMAGE", "services/reitti/values.yml:MODULE_AUS", "box.yml:domain"} {
		if policy.MayPath(p, "haushalt", at) {
			t.Errorf("haushalt may propose %s", at)
		}
	}
	var def state.Policy
	yaml.Unmarshal([]byte(state.DefaultPolicy), &def)
	if !policy.MayVerb(def, "homes", "module") || slices.Contains(def.Hands["homes"].Paths, "services/homes/values.yml:MODULE_AUS") {
		t.Fatalf("homes: the verb, not the path: %+v", def.Hands["homes"])
	}
	old := state.Policy{Hands: map[string]state.Hand{"homes": {Ceiling: "apply", Verbs: []string{"status"}}}}
	if !slices.Contains(policy.Missing(old, false)["homes"], "module") {
		t.Fatal("the defaults sync does not name module for homes")
	}
	env, _ := testBox(t)
	if argv, _, err := mcp.Argv(mustState(t, env), "module", map[string]any{"action": "aus", "liste": "podcasts,energie"}); err != nil || !slices.Equal(argv, []string{"aus", "podcasts,energie"}) {
		t.Fatalf("argv %q %v", argv, err)
	}
	if argv, _, err := mcp.Argv(mustState(t, env), "module", map[string]any{"action": "aus", "liste": "podcasts", "grund": "Podcasts verschwinden."}); err != nil || !slices.Equal(argv, []string{"aus", "podcasts", "--grund", "Podcasts verschwinden."}) {
		t.Fatalf("grund: argv %q %v", argv, err)
	}
	if argv, _, err := mcp.Argv(mustState(t, env), "module", map[string]any{"action": "aus"}); err != nil || !slices.Equal(argv, []string{"aus"}) {
		t.Fatalf("empty: argv %q %v", argv, err)
	}
}
