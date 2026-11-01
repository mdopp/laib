package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/sysd"
)

// #978, the owner: „im Vorfeld einen Marker setzen, dass jetzt bis x Uhr
// ein Neustart durchgeführt wird und bis dahin bestimmte Tests einfach
// nicht laufen müssen … sollte der Marker nicht nach der Uhrzeit gelöscht
// werden, erst dann gibt es eine Problemanalyse“. Before, „planned“ had two
// answers inferred afterwards — diag's apply hold (#907) and the log
// review's reading of the journal and the units' Stopping lines (#975).
// Now an action announces itself; diag, `laib planned` and the skills read
// the one marker.

// markerBox is testBox with whoami using home-assistant (uses:, a box
// without it still loads whoami) and whoami's health at url; down makes
// whoami's unit failed, during runs inside home-assistant's restart.
func markerBox(t *testing.T, url string, down *bool, during func()) *cliEnv {
	t.Helper()
	env, _ := testBox(t)
	ha := filepath.Join(env.Paths.State, "services", "home-assistant")
	os.MkdirAll(ha, 0o755)
	for path, content := range map[string]string{
		filepath.Join(ha, "laib.yml"):                                    "name: home-assistant\n",
		filepath.Join(ha, "values.yml"):                                  "IMAGE: ghcr.io/home-assistant/home-assistant:2026.10\n",
		filepath.Join(ha, "home-assistant.container"):                    "[Container]\nImage=${IMAGE}\n",
		filepath.Join(env.Paths.State, "services", "whoami", "laib.yml"): "name: whoami\nsecrets:\n  - name: WHOAMI_TOKEN\nhealth: {http: " + url + "}\nuses: [home-assistant]\n",
	} {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := proposal.CommitDirty(context.Background(), env.Paths.State, "human:test"); err != nil {
		t.Fatal(err)
	}
	prev := sysd.Run
	t.Cleanup(func() { sysd.Run = prev })
	sysd.Run = func(ctx context.Context, name string, args ...string) (string, error) {
		switch {
		case name == "systemctl" && slices.Contains(args, "restart") && slices.Contains(args, "home-assistant.service") && during != nil:
			during()
		case name == "systemctl" && slices.Contains(args, "is-active") && slices.Contains(args, "whoami.service") && down != nil && *down:
			return "failed", nil
		}
		return prev(ctx, name, args...)
	}
	return env
}

func plannedOf(t *testing.T, env *cliEnv, svc string) diag.Planned {
	t.Helper()
	var buf bytes.Buffer
	env.JSON, env.Out = true, &buf
	defer func() { env.JSON, env.Out = false, nil }()
	if code := diag.PlannedCmd(context.Background(), env, []string{svc}); code != cli.ExitOK {
		t.Fatalf("laib planned %s: exit %d", svc, code)
	}
	var p diag.Planned
	if err := json.Unmarshal(buf.Bytes(), &p); err != nil {
		t.Fatalf("%v: %s", err, buf.String())
	}
	return p
}

// #305, #304: haus' and Music Assistant's errors while Home Assistant was
// restarted on purpose. The restart announces itself before it acts: what
// depends on Home Assistant is expected red while it runs, to diag and to
// laib planned alike, and a little after it, for the reconnect; its window
// is what the log review drops lines by.
func TestARestartIsAnnouncedAndDiagAndPlannedAgree(t *testing.T) {
	down := true
	var during diag.Finding
	var seen diag.Planned
	var env *cliEnv
	env = markerBox(t, sickWhoami(t), &down, func() { during, seen = whoamiUnit(t, env), plannedOf(t, env, "whoami") })
	if f := whoamiUnit(t, env); f.Severity != "rot" {
		t.Fatalf("nothing announced: whoami's red is red, %+v", f)
	}
	env.Actor = "ops"
	if code := envelope.Restart(context.Background(), env, []string{"home-assistant"}); code != cli.ExitOK {
		t.Fatalf("restart: exit %d", code)
	}
	if during.Severity != "info" || during.Repair != nil || !strings.HasPrefix(during.Wartet, "geplant: laib restart von home-assistant bis ") {
		t.Fatalf("diag during home-assistant's restart: %+v", during)
	}
	if "geplant: "+strings.TrimPrefix(seen.Now, "geplant: ") != during.Wartet || !slices.ContainsFunc(seen.Findings, func(g diag.Finding) bool { return g.Wartet == during.Wartet }) {
		t.Fatalf("laib planned says %q with %+v, diag %q", seen.Now, seen.Findings, during.Wartet)
	}

	// after: the dependent three minutes longer, Home Assistant's own window ends with it
	p := plannedOf(t, env, "whoami")
	if !slices.Equal(p.Depends, []string{"home-assistant"}) || len(p.Windows) != 1 {
		t.Fatalf("whoami depends on home-assistant, one window: %+v", p)
	}
	w := p.Windows[0]
	if w.Label != "laib restart" || w.Result != "green" || w.Overdue || !slices.Equal(w.Services, []string{"home-assistant"}) || !slices.Equal(w.Dependents, []string{"whoami"}) ||
		!w.From.Equal(w.Since) || !w.To.Equal(w.End.Add(3*time.Minute)) || w.End.Before(w.Since) {
		t.Fatalf("the restart's window for whoami: %+v", w)
	}
	if h := plannedOf(t, env, "home-assistant").Windows; len(h) != 1 || !h[0].To.Equal(h[0].End) {
		t.Fatalf("home-assistant's own window ends with the restart: %+v", h)
	}
	if f := whoamiUnit(t, env); f.Severity != "info" || p.Now == "" {
		t.Fatalf("a dependent within three minutes of the end is still expected: %+v, now %q", f, p.Now)
	}
	es, _ := journal.Read(env.Paths.Journal, time.Now().Add(-time.Hour))
	if n := len(slices.DeleteFunc(es, func(e journal.Entry) bool { return e.Verb != "marker" })); n != 2 {
		t.Fatalf("the marker is journaled at its start and its end: %d lines", n)
	}

	// the same answer through MCP, for the Operator's skills
	st, err := cli.LoadState(env)
	if err != nil {
		t.Fatal(err)
	}
	out, code := mcp.Call(context.Background(), env, st, "ops", "laib_planned", map[string]any{"service": "whoami"})
	if code != cli.ExitOK || !strings.Contains(out, `"label": "laib restart"`) {
		t.Fatalf("laib_planned for ops: exit %d: %s", code, out)
	}
}

// #907's apply hold, now a marker: while an apply holds whoami its red is
// expected; once it ends, red again at once (its own service gets no
// grace). An apply that dies leaves its marker; past its until that is red
// itself — the restart did not finish cleanly — and a later green restart
// of the service closes it.
func TestAMarkerNotClearedByItsUntilIsTheFinding(t *testing.T) {
	down := true
	env := markerBox(t, "", &down, nil)
	st, err := cli.LoadState(env)
	if err != nil {
		t.Fatal(err)
	}
	done := apply.Mark(st, env.Paths, "apply", "human:mdopp", "#261", []string{"whoami"}, time.Hour)
	if f := whoamiUnit(t, env); f.Severity != "info" || f.Repair != nil || !strings.Contains(f.Wartet, "Anwenden von whoami bis") {
		t.Fatalf("a red the apply causes is red to the look, or offers a repair: %+v", f)
	}
	done(true)
	if f := whoamiUnit(t, env); f.Severity != "rot" || !slices.Equal(f.Repair, []string{"restart", "whoami"}) {
		t.Fatalf("a red that outlives the apply is not red again: %+v", f)
	}

	apply.Mark(st, env.Paths, "apply", "human:mdopp", "#262", []string{"whoami"}, time.Millisecond) // the apply dies
	time.Sleep(5 * time.Millisecond)
	fs := diag.Probes(context.Background(), env, st, "whoami")
	i := slices.IndexFunc(fs, func(f diag.Finding) bool { return f.Probe == "wartung" })
	if i < 0 || fs[i].Severity != "rot" || !strings.HasPrefix(fs[i].Detail, "Der Neustart von whoami ist bis ") || !strings.Contains(fs[i].Detail, "nicht sauber fertig geworden") {
		t.Fatalf("an overdue marker is the finding: %+v", fs)
	}
	if p := plannedOf(t, env, "whoami"); p.Now != "" || !p.Windows[len(p.Windows)-1].Overdue {
		t.Fatalf("an overdue marker holds nothing: %+v", p)
	}
	down = false
	env.Actor = "ops"
	if code := envelope.Restart(context.Background(), env, []string{"whoami"}); code != cli.ExitOK {
		t.Fatalf("restart: exit %d", code)
	}
	if fs := diag.Probes(context.Background(), env, st, "whoami"); slices.ContainsFunc(fs, func(f diag.Finding) bool { return f.Probe == "wartung" }) {
		t.Fatalf("a green restart of whoami closes its overdue marker: %+v", fs)
	}
}

// Every apply announces itself: converge and the Inbox's approve run
// through the same ApplyPlan.
func TestAnApplyLeavesItsMarker(t *testing.T) {
	c := newCbox(t)
	ms := apply.Markers(c.env.Paths)
	if len(ms) != 1 || ms[0].Verb != "apply" || ms[0].Result != "green" || ms[0].End.IsZero() || !slices.Contains(ms[0].Services, "whoami") || ms[0].Until.Sub(ms[0].Since) != 15*time.Minute {
		t.Fatalf("the first apply's marker: %+v", ms)
	}
}
