package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/mcp"
)

// ADR 0026, #598: a verb served by a program beside laib. The kernel checks
// hand, tier and verb before anything runs, --help is the kernel's; the
// program gets the args, the actor, the paths and stdin, its output and exit
// code are the verb's, it may call laib back only up to propose, and what it
// journals the kernel writes, as the caller.
func TestTheVerbDoorRunsTheProgramOnlyAfterPolicy(t *testing.T) {
	env, _ := testBox(t)
	dir := t.TempDir()
	ran := filepath.Join(dir, "ran")
	stub := filepath.Join(dir, "laib-haushalt")
	os.WriteFile(stub, []byte(`#!/bin/sh
echo "args=$* actor=$LAIB_ACTOR ceiling=$LAIB_CEILING state=$LAIB_STATE" > `+ran+`
read line; echo "stdin=$line"
echo '{"verb":"approve","actor":"human:mdopp","service":"scan","result":"info","detail":"start"}' >&3
echo nein >&2
exit 3
`), 0o755)
	t.Setenv("LAIB_HAUSHALT", stub)
	call := func(hand string, args map[string]any) (string, int) {
		st, err := cli.LoadState(env)
		if err != nil {
			t.Fatal(err)
		}
		return mcp.Call(context.Background(), env, st, hand, "laib_scan", args)
	}

	if out, code := call("resident", map[string]any{"action": "start"}); code != cli.ExitRefused {
		t.Fatalf("a read hand reached the program: exit %d: %s", code, out)
	}
	if _, err := os.Stat(ran); err == nil {
		t.Fatal("the program ran before policy refused the call")
	}

	since := time.Now().Add(-time.Minute)
	var out, errOut strings.Builder
	cenv := &cli.Env{Paths: env.Paths, Actor: "haus", JSON: true, Out: &out, ErrOut: &errOut, In: strings.NewReader("hallo\n")}
	if code := verb(t, "scan").Run(context.Background(), cenv, []string{"start", "--person", "mama"}); code != 3 {
		t.Fatalf("exit %d, want the program's 3", code)
	}
	b, _ := os.ReadFile(ran)
	if got := strings.TrimSpace(string(b)); got != "args=--json scan start --person mama actor=haus ceiling=propose state="+env.Paths.State {
		t.Fatalf("the program got %q", got)
	}
	if out.String() != "stdin=hallo\n" || errOut.String() != "nein\n" {
		t.Fatalf("stdout %q, stderr %q", out.String(), errOut.String())
	}
	es, _ := journal.Read(env.Paths.Journal, since)
	var mine []journal.Entry
	for _, e := range es {
		if e.Service == "scan" {
			mine = append(mine, e)
		}
	}
	if len(mine) != 1 || mine[0].Actor != "haus" || mine[0].Verb != "scan" || mine[0].Detail != "start" {
		t.Fatalf("the program's journal line, as the caller: %+v", mine)
	}

	os.Remove(ran)
	out.Reset()
	if code := verb(t, "scan").Run(context.Background(), cenv, []string{"--help"}); code != cli.ExitOK || !strings.Contains(out.String(), "laib scan [propose]") {
		t.Fatalf("--help: exit %d: %s", code, out.String())
	}
	if _, err := os.Stat(ran); err == nil {
		t.Fatal("--help ran the program")
	}
}

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
