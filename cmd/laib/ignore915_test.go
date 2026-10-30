package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/proposal"
)

// #915: a log shape the owner or a ticket answered as harmless is recorded
// once, with its reason, in ignore.yml on main, where the log review reads
// it (laib state, parsed); an unmanaged unit is still ignored the same way.
func TestIgnoreRecordsAHarmlessLogShapeWithItsReason(t *testing.T) {
	env, _ := testBox(t)
	ctx := context.Background()
	if err := proposal.CommitDirty(ctx, env.Paths.State, "human:test"); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) int {
		e := *env
		var errOut bytes.Buffer
		e.Out, e.ErrOut, e.Actor = &bytes.Buffer{}, &errOut, "claude"
		rc := cmdIgnore(ctx, &e, args)
		t.Logf("ignore %q: %d %s", args, rc, errOut.String())
		return rc
	}
	for _, bad := range [][]string{
		{"--log", "whoami", `IN PTR`},                     // no reason
		{"--log", "whoami", `(`, "kaputtes Muster"},       // no RE2
		{"--log", "nas", `IN PTR`, "kein solcher Dienst"}, // no such service
		{"--log", "whoami", ``, "leeres Muster"},
	} {
		if run(bad...) == cli.ExitOK {
			t.Errorf("ignore %q went through", bad)
		}
	}
	if run("servicebay.service") != cli.ExitOK || run("--log", "whoami", `in-addr\.arpa\. IN PTR`, "FritzBox beantwortet den PTR nicht (#901)") != cli.ExitOK ||
		run("--log", "whoami", `in-addr\.arpa\. IN PTR`, "noch einmal") != cli.ExitOK || run("old.service") != cli.ExitOK {
		t.Fatal("ignore failed")
	}

	var out bytes.Buffer
	e := *env
	e.Out, e.ErrOut, e.JSON = &out, &bytes.Buffer{}, true
	if rc := cmdState(ctx, &e, []string{"ignore.yml"}); rc != cli.ExitOK {
		t.Fatalf("ignore.yml is not on main: rc %d", rc)
	}
	var j struct {
		Data struct {
			Units []string `json:"units"`
			Logs  []struct {
				Service, Match, Reason string
			} `json:"logs"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &j); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	if strings.Join(j.Data.Units, ",") != "servicebay.service,old.service" {
		t.Errorf("units: %v", j.Data.Units)
	}
	if len(j.Data.Logs) != 1 || j.Data.Logs[0].Service != "whoami" || j.Data.Logs[0].Match != `in-addr\.arpa\. IN PTR` || !strings.Contains(j.Data.Logs[0].Reason, "#901") {
		t.Errorf("logs: %+v", j.Data.Logs)
	}
	// the plan still reads the units
	b, _ := os.ReadFile(filepath.Join(env.Paths.State, "ignore.yml"))
	if !strings.Contains(string(b), "- old.service") {
		t.Errorf("ignore.yml:\n%s", b)
	}
}
