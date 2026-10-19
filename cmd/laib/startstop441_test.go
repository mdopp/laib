package main

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/diag"
	"github.com/mdopp/laib/internal/envelope"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/mcp"
)

// #441, the owner: "Das Interface ist jetzt der Chat" — start and stop of a
// service must be verbs a chat hand can call, with the right tier. stop is
// a pause plus the stop (converge leaves the service alone until the pause
// ends, at most a day); start lifts the pause, starts and verifies. Both at
// restart's tier and inside its envelope; a propose hand (pi) gets neither.
func TestStartAndStopAServiceAsVerbs(t *testing.T) {
	env, _, calls := envBox(t) // the ops hand: ceiling apply
	ctx := context.Background()
	if code := envelope.Stop(ctx, env, []string{"whoami", "--until", "1h", "Wartung"}); code != cli.ExitOK {
		t.Fatalf("stop: exit %d", code)
	}
	if !slices.Contains(*calls, "systemctl --user stop whoami.service") {
		t.Fatalf("whoami was not stopped: %v", *calls)
	}
	pz, ok := apply.Paused(env.Paths)["whoami"]
	if !ok || pz.By != "ops" || time.Until(pz.Until) > time.Hour+time.Minute {
		t.Fatalf("a stop pauses the service, so converge does not start it again: %+v", pz)
	}
	if code := envelope.Start(ctx, env, []string{"whoami"}); code != cli.ExitOK {
		t.Fatalf("start: exit %d", code)
	}
	if !slices.Contains(*calls, "systemctl --user start whoami.service") {
		t.Fatalf("whoami was not started: %v", *calls)
	}
	if _, ok := apply.Paused(env.Paths)["whoami"]; ok {
		t.Fatal("start leaves the pause in place")
	}
	es, _ := journal.Read(env.Paths.Journal, time.Time{})
	var verbs []string
	for _, e := range es {
		if e.Service == "whoami" && (e.Verb == "stop" || e.Verb == "start") {
			verbs = append(verbs, e.Verb+" "+e.Result+" "+e.Actor)
		}
	}
	if strings.Join(verbs, ",") != "stop info ops,start green ops" {
		t.Fatalf("journal: %v", verbs)
	}
	pi := &cliEnv{Actor: "pi", Paths: env.Paths}
	if envelope.Stop(ctx, pi, []string{"whoami"}) != cli.ExitRefused || envelope.Start(ctx, pi, []string{"whoami"}) != cli.ExitRefused {
		t.Fatal("a propose hand may start or stop")
	}
	st, _ := cli.LoadState(env)
	names := func(hand string) []string {
		var n []string
		for _, tl := range mcp.Tools(st, hand) {
			n = append(n, tl["name"].(string))
		}
		return n
	}
	if p := names("pi"); slices.Contains(p, "laib_stop") || slices.Contains(p, "laib_start") || !slices.Contains(p, "laib_backups") {
		t.Errorf("pi's tools: %v", p)
	}
	if o := names("ops"); !slices.Contains(o, "laib_stop") || !slices.Contains(o, "laib_start") {
		t.Errorf("an apply hand lacks start/stop over MCP: %v", o)
	}
}

// #441: the backups as a read tool the chat hands call — from the journal,
// what really ran: the last nightly (green/red, when), the last off-box
// copy, the last restore test, and diag's backup findings with their fixes.
func TestTheBackupsAsAReadTool(t *testing.T) {
	env, _, _ := envBox(t)
	now := time.Now().UTC()
	for _, e := range []journal.Entry{
		{T: now.Add(-30 * time.Hour), Actor: "laib", Verb: "backup", Result: "green"},
		{T: now.Add(-6 * time.Hour), Actor: "laib", Verb: "backup", Result: "red", Detail: "restic: repo locked"},
		{T: now.Add(-29 * time.Hour), Actor: "laib", Verb: "backup", Reason: "offsite", Result: "green"},
		{T: now.Add(-240 * time.Hour), Actor: "laib", Verb: "backup", Reason: "check", Result: "green"},
	} {
		journal.Append(env.Paths.Journal, e)
	}
	var out strings.Builder
	e := &cliEnv{Actor: "pi", Paths: env.Paths, JSON: true, Out: &out}
	if code := diag.Backups(context.Background(), e, nil); code != cli.ExitOK {
		t.Fatalf("exit %d", code)
	}
	var b struct {
		Nightly struct {
			Result, Detail string
			At             time.Time
		}
		LastGreen, Offsite, RestoreTest time.Time
		Findings                        []diag.Finding
	}
	if err := json.Unmarshal([]byte(out.String()), &b); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	if b.Nightly.Result != "red" || !strings.Contains(b.Nightly.Detail, "locked") || now.Sub(b.Nightly.At) < 5*time.Hour {
		t.Errorf("the last nightly is the red one 6 h ago: %+v", b.Nightly)
	}
	if now.Sub(b.LastGreen) < 29*time.Hour || now.Sub(b.Offsite) < 28*time.Hour || now.Sub(b.RestoreTest) < 239*time.Hour {
		t.Errorf("the times are not the journal's: %+v", b)
	}
	for _, f := range b.Findings {
		if f.Probe != "backup" && f.Probe != "restore-test" && f.Probe != "recovery" {
			t.Errorf("a finding that is not about the backups: %+v", f)
		}
	}
}
