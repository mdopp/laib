package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/policy"
)

// ADR 0026: what a program behind the door calls back stops at its ceiling.
func TestACallBackFromBehindTheDoorStopsAtItsCeiling(t *testing.T) {
	env, _ := testBox(t)
	t.Setenv("LAIB_HOME", env.Paths.Home)
	t.Setenv("LAIB_STATE", env.Paths.State)
	t.Setenv("LAIB_ACTOR", "haus")
	t.Setenv("LAIB_CEILING", "propose")
	for _, argv := range [][]string{{"approve", "1"}, {"apply", "whoami"}, {"secret", "list"}} {
		if code := runArgs(argv); code != cli.ExitRefused {
			t.Errorf("%v under ceiling propose: exit %d", argv, code)
		}
	}
	t.Setenv("LAIB_CEILING", "read")
	if code := runArgs([]string{"note", "x"}); code != cli.ExitRefused {
		t.Errorf("note under ceiling read: exit %d", code)
	}
}

// #577 through the door (#598): laib_net with view geraete reaches
// laib-haushalt with the box's state and answers its JSON; the devices
// themselves are haushalt/netdevices577_test.go's.
func TestNetGeraeteIsServedBehindTheDoor(t *testing.T) {
	env, _ := testBox(t)
	withHaushalt(t, env)
	st, err := cli.LoadState(env)
	if err != nil {
		t.Fatal(err)
	}
	out, code := mcp.Call(context.Background(), env, st, "ops", "laib_net", map[string]any{"view": "geraete"})
	var got struct {
		Subnet  string   `json:"subnet"`
		Woken   bool     `json:"woken"`
		Limits  []string `json:"limits"`
		Devices []any    `json:"devices"`
	}
	if code != cli.ExitOK || json.Unmarshal([]byte(out), &got) != nil || got.Subnet != "10.0.0.0/24" || got.Woken || got.Devices == nil || len(got.Limits) < 3 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !policy.KnownVerbs["net"] || !policy.KnownVerbs["scan"] {
		t.Fatal("net and scan are not in policy.KnownVerbs")
	}
}
