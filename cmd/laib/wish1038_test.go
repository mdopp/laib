package main

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/aufgaben"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/policy"
	"github.com/mdopp/laib/internal/state"
	"gopkg.in/yaml.v3"
)

// #1038 (homes ADR 0001 §10), through the real laib-haushalt behind the
// door: Homes' wish for a module lands on the admins' Box-Aufgaben with the
// module's page as its weg; laib wish stand reads it back as the haushalt
// hand (the Box-Aufgaben and the Inbox) and answers angefordert; once Homes
// itself ticks it off, umgesetzt.
func TestHomesWishForAModuleAndItsStandThroughTheDoor(t *testing.T) {
	c := newCbox(t)
	c.intake(t, "", newFakeGitHub(t))
	c.env.Actor = "homes"
	if rc := c.wish(t, "--modul", "standort", "--von", "lena", "Standortverlauf"); rc != cli.ExitOK {
		t.Fatalf("rc %d", rc)
	}
	list, err := aufgaben.Load(c.env.Paths.State)
	if err != nil || len(list) != 1 {
		t.Fatalf("one Box-Aufgabe: %v %v", list, err)
	}
	if a := list[0]; a.Titel != "Wunsch aus Homes: Standortverlauf" || a.Von != "homes:lena" || a.Fuer != aufgaben.Admins || a.Weg == nil || a.Weg.Seite != "/module/standort" {
		t.Fatalf("the admins' task for the module: %+v", a)
	}
	stand := func() []map[string]any {
		t.Helper()
		var out bytes.Buffer
		c.env.Out, c.env.JSON = &out, true
		defer func() { c.env.Out, c.env.JSON = nil, false }()
		if rc := c.wish(t, "stand", "standort=reitti", "podcasts=audiobookshelf"); rc != cli.ExitOK {
			t.Fatalf("stand rc %d: %s", rc, out.String())
		}
		var got []map[string]any
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatalf("%v: %s", err, out.String())
		}
		return got
	}
	if got := stand(); len(got) != 1 || got[0]["modul"] != "standort" || got[0]["stand"] != "angefordert" || got[0]["aufgabe"] != float64(1) || got[0]["von"] != "homes:lena" {
		t.Fatalf("stand: %v", got)
	}
	if rc := verb(t, "aufgabe").Run(context.Background(), c.env, []string{"erledigt", "1"}); rc != cli.ExitOK { // as the homes hand
		t.Fatalf("Homes ticks it off: rc %d", rc)
	}
	if got := stand(); len(got) != 1 || got[0]["stand"] != "umgesetzt" {
		t.Fatalf("after Homes ticked it off: %v", got)
	}
}

// #1038: laib_wish takes modul and stand, the argv built in code; text is
// needed unless stand is given, as before for every other caller.
func TestWishMCPTakesModulAndStand(t *testing.T) {
	env, _ := testBox(t)
	st := mustState(t, env)
	for _, c := range []struct {
		args map[string]any
		want []string
	}{
		{map[string]any{"text": "Standortverlauf", "modul": "standort", "von": "lena"}, []string{"--modul", "standort", "--von", "lena", "Standortverlauf"}},
		{map[string]any{"text": "Podcasts", "modul": "podcasts"}, []string{"--modul", "podcasts", "Podcasts"}},
		{map[string]any{"stand": "standort=reitti  werkstatt= gesundheit=gesundheit"}, []string{"stand", "standort=reitti", "werkstatt=", "gesundheit=gesundheit"}},
		{map[string]any{"text": "ein Kalender", "von": "lena"}, []string{"--von", "lena", "ein Kalender"}},
	} {
		if argv, _, err := mcp.Argv(st, "wish", c.args); err != nil || !slices.Equal(argv, c.want) {
			t.Errorf("%v: %q %v", c.args, argv, err)
		}
	}
	if _, _, err := mcp.Argv(st, "wish", map[string]any{"von": "lena"}); err == nil || !strings.Contains(err.Error(), "text") {
		t.Fatalf("no text, no stand: %v", err)
	}
}

// #1038: the homes hand may wish (DefaultPolicy, and so the run's proposal
// for missing built-in grants, #575, on a box whose homes line lacks it);
// the haushalt hand reads the Inbox and still proposes only its paths.
func TestHomesMayWishAndHaushaltReadsTheInbox(t *testing.T) {
	var def state.Policy
	if err := yaml.Unmarshal([]byte(state.DefaultPolicy), &def); err != nil {
		t.Fatal(err)
	}
	if !policy.MayVerb(def, "homes", "wish") {
		t.Fatal("DefaultPolicy: homes may not wish")
	}
	old := state.Policy{Hands: map[string]state.Hand{"homes": {Ceiling: "apply", Verbs: []string{"status", "aufgabe"}}}}
	if !slices.Contains(policy.Missing(old, false)["homes"], "wish") {
		t.Fatalf("the defaults sync does not name wish for homes: %v", policy.Missing(old, false))
	}
	h := state.HaushaltHand
	if !slices.Contains(h.Verbs, "inbox") || slices.Contains(h.Verbs, "approve") || h.Ceiling != "propose" {
		t.Fatalf("haushalt: %+v", h)
	}
}
