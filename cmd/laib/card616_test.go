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
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/proposal"
)

// #616 end to end: a grant proposed with --grant carries what it changes,
// its card says it in words with a weight, laib_inbox's JSON hands the card
// over, and the box's own warning (two services for one purpose) stays off
// the proposals that do not touch those services.
func TestAGrantCardSaysWhatChangesAndLeavesTheBoxWarningOut(t *testing.T) {
	env, _ := testBox(t)
	ctx := context.Background()
	who := filepath.Join(env.Paths.State, "services", "whoami")
	echo := filepath.Join(env.Paths.State, "services", "echo")
	os.MkdirAll(echo, 0o755)
	for _, f := range []string{"laib.yml", "values.yml", "whoami.container"} {
		b, _ := os.ReadFile(filepath.Join(who, f))
		text := strings.Replace(string(b), "name: whoami\n", "name: whoami\nprovides: [demo]\n", 1)
		os.WriteFile(filepath.Join(who, f), []byte(text), 0o644)
		text = strings.NewReplacer("name: whoami", "name: echo", "WHOAMI_TOKEN", "ECHO_TOKEN").Replace(text)
		os.WriteFile(filepath.Join(echo, strings.Replace(f, "whoami", "echo", 1)), []byte(text), 0o644)
	}
	if err := proposal.CommitDirty(ctx, env.Paths.State, "human:test"); err != nil {
		t.Fatal(err)
	}
	st, err := cli.LoadState(env)
	if err != nil || len(st.Warnings) == 0 || !strings.Contains(st.Warnings[0], "echo, whoami") {
		t.Fatalf("the box needs its purpose warning for this test: %v %v", st.Warnings, err)
	}

	if code := inbox.Propose(ctx, env, []string{"--grant", "haus", "updates", "Haus soll Updates zeigen"}); code != cli.ExitOK {
		t.Fatalf("--grant: exit %d", code)
	}
	open, _ := proposal.List(cli.InboxDir(env), true)
	if len(open) != 1 {
		t.Fatalf("%+v", open)
	}
	p := open[0]
	if len(p.Warnings) != 0 {
		t.Errorf("the box's warning about echo and whoami is on a grant for haus: %v", p.Warnings)
	}
	var out bytes.Buffer
	env.JSON, env.Out = true, &out
	if code := inbox.List(ctx, env, nil); code != cli.ExitOK {
		t.Fatalf("inbox --json: exit %d", code)
	}
	var got []struct {
		Reason string            `json:"reason"`
		Class  string            `json:"class"`
		Card   proposal.Decision `json:"card"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || len(got) != 1 {
		t.Fatalf("%v: %s", err, out.String())
	}
	c := got[0].Card
	if got[0].Reason == "" || got[0].Class != "human" {
		t.Errorf("the fields Haus reads are gone: %+v", got[0])
	}
	if c.Summary != "Haus darf künftig nach Updates sehen" || c.Risk != "mittel" || c.Primary != "Später" || c.Decider == "" {
		t.Errorf("card: %+v", c)
	}
	lines := ""
	for _, l := range c.Changes {
		lines += l.Op + " " + l.Text + "\n"
	}
	if !strings.Contains(lines, "+ nach Updates sehen") || !strings.Contains(lines, "Was Haus schon darf, bleibt: den Zustand der Dienste sehen") {
		t.Errorf("changes:\n%s", lines)
	}
	if !strings.Contains(strings.Join(c.Impact, " "), "sieht dadurch mehr, ändert aber nichts") {
		t.Errorf("a read verb changes nothing, and the card says so: %v", c.Impact)
	}

	// a change to whoami is about the warning's services: it keeps it
	env.JSON, env.Out = false, &bytes.Buffer{}
	if code := inbox.Propose(ctx, env, []string{"--set", "services/whoami/values.yml:IMAGE=docker.io/traefik/whoami:v1.11", "neues Image"}); code != cli.ExitOK {
		t.Fatalf("--set: exit %d", code)
	}
	open, _ = proposal.List(cli.InboxDir(env), true)
	for _, q := range open {
		if q.ID != p.ID && len(q.Warnings) != 1 {
			t.Errorf("a change to whoami must carry the warning that names it: %v", q.Warnings)
		}
	}
}
