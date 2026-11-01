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
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/sysd"
)

// #978: „was this stop planned, and what does this service depend on“ had
// two answers — diag's apply hold for the look (#907) and the log review's
// own script with a hand-kept map (#975). `laib planned` is the one: the
// journal's planned work, the units' Stopping and Started lines, the
// contract's after:, uses:, needs: and oidc:; diag holds a red by it.

// plannedBox is testBox with whoami using home-assistant (uses:, a box
// without it still loads whoami), whoami's health at url, and journalctl
// answering each unit's lines from units.
func plannedBox(t *testing.T, url string, units map[string][]string, down *bool) *cliEnv {
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
		case name == "journalctl":
			return strings.Join(units[args[2]], "\n"), nil
		case name == "systemctl" && slices.Contains(args, "is-active") && slices.Contains(args, "whoami.service") && down != nil && *down:
			return "failed", nil
		}
		return prev(ctx, name, args...)
	}
	return env
}

func stopped(unit string, at ...time.Time) []string {
	var out []string
	for i := 0; i+1 < len(at); i += 2 {
		out = append(out, at[i].UTC().Format(time.RFC3339)+" atHome-Server systemd[1373]: Stopping "+unit+" - x...",
			at[i+1].UTC().Format("2006-01-02T15:04:05-0700")+" atHome-Server systemd[1373]: Started "+unit+" - x.") // both forms journalctl writes
	}
	return out
}

func utc(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		panic(err)
	}
	return t
}

func record(t *testing.T, env *cliEnv, es ...journal.Entry) {
	t.Helper()
	for _, e := range es {
		if err := journal.Append(env.Paths.Journal, e); err != nil {
			t.Fatal(err)
		}
	}
}

func plannedOf(t *testing.T, env *cliEnv, args ...string) diag.Planned {
	t.Helper()
	var buf bytes.Buffer
	env.JSON, env.Out = true, &buf
	defer func() { env.JSON, env.Out = false, nil }()
	if code := diag.PlannedCmd(context.Background(), env, args); code != cli.ExitOK {
		t.Fatalf("laib planned %v: exit %d", args, code)
	}
	var p diag.Planned
	if err := json.Unmarshal(buf.Bytes(), &p); err != nil {
		t.Fatalf("%v: %s", err, buf.String())
	}
	return p
}

func said(ws []diag.Window) []string {
	var out []string
	for _, w := range ws {
		out = append(out, w.Service+" "+w.Label+" "+w.From.UTC().Format("01-02T15:04:05")+"–"+w.To.UTC().Format("15:04:05"))
	}
	return out
}

// The #975 shapes, as the Kraftwerk wrote them: wish #305 (haus refused by
// Home Assistant, which three applies restarted), #304 (Music Assistant's
// 502 while the backup stopped Home Assistant and a restart followed) and
// #294 (each updates run stops the lease) — and a stop no entry accounts for.
func TestPlannedAnswersTheWindowsOfAServiceAndWhatItDependsOn(t *testing.T) {
	units := map[string][]string{
		"home-assistant.service": slices.Concat(
			stopped("home-assistant.service", utc("2026-10-31T09:08:47Z"), utc("2026-10-31T09:08:57Z"), utc("2026-10-31T09:11:03Z"), utc("2026-10-31T09:11:10Z"),
				utc("2026-10-31T11:11:22Z"), utc("2026-10-31T11:11:27Z")),
			stopped("home-assistant.service", utc("2026-11-01T03:07:28Z"), utc("2026-11-01T03:08:21Z"), utc("2026-11-01T03:09:27Z"), utc("2026-11-01T03:09:33Z")),
			stopped("home-assistant.service", utc("2026-11-01T05:00:00Z"), utc("2026-11-01T05:00:30Z"))), // a crash cascade or a hand at systemctl
		"whoami.service": stopped("whoami.service", utc("2026-10-30T08:22:22Z"), utc("2026-10-30T08:22:23Z"), utc("2026-10-30T11:05:45Z"), utc("2026-10-30T11:05:46Z")),
	}
	env := plannedBox(t, "", units, nil)
	record(t, env,
		journal.Entry{T: utc("2026-10-30T08:22:01Z"), Actor: "laib", Verb: "updates", Result: "info", Detail: "Lauf gestartet (--now)"},
		journal.Entry{T: utc("2026-10-30T11:05:01Z"), Actor: "laib", Verb: "updates", Result: "info", Detail: "Lauf gestartet (--now)"},
		journal.Entry{T: utc("2026-10-31T09:08:44.7787766Z"), Actor: "human:mdopp", Verb: "apply", Result: "green", MS: 8273},
		journal.Entry{T: utc("2026-10-31T09:11:37.352074069Z"), Actor: "human:mdopp", Verb: "apply", Result: "green", MS: 125563},
		journal.Entry{T: utc("2026-10-31T11:11:55.484557492Z"), Actor: "laib", Verb: "apply", Result: "green", MS: 36291},
		journal.Entry{T: utc("2026-11-01T03:08:22.751834666Z"), Actor: "laib", Verb: "backup", Result: "green", MS: 47085, Detail: "angehalten für den Snapshot: home-assistant"},
		journal.Entry{T: utc("2026-11-01T03:09:39.5430544Z"), Actor: "ops", Verb: "restart", Service: "home-assistant", Result: "green"},
		journal.Entry{T: utc("2026-11-01T05:00:10Z"), Actor: "ops", Verb: "restart", Service: "whoami", Result: "green"}, // another service's restart stopped nothing here
	)
	p := plannedOf(t, env, "whoami", "--since", "100000h")
	if !slices.Equal(p.Depends, []string{"home-assistant"}) {
		t.Fatalf("whoami uses home-assistant: depends %v", p.Depends)
	}
	want := []string{
		"whoami Update 10-30T08:20:22–08:25:23",
		"whoami Update 10-30T11:03:45–11:08:46",
		"home-assistant Anwenden 10-31T09:06:47–09:11:57",
		"home-assistant Anwenden 10-31T09:09:03–09:14:10",
		"home-assistant Anwenden 10-31T11:09:22–11:14:27",
		"home-assistant Sicherung 11-01T03:05:28–03:11:21",
		"home-assistant laib restart 11-01T03:07:27–03:12:33",
	}
	if got := said(p.Windows); !slices.Equal(got, want) {
		t.Fatalf("windows:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if p.Findings == nil || len(p.Unread) > 0 || p.Now != "" {
		t.Fatalf("findings must be a list, nothing unread, nothing planned now: %+v", p)
	}

	// the same answer through MCP, for the Operator's skills, typed
	st, err := cli.LoadState(env)
	if err != nil {
		t.Fatal(err)
	}
	out, code := mcp.Call(context.Background(), env, st, "ops", "laib_planned", map[string]any{"service": "whoami", "since": "100000h"})
	if code != cli.ExitOK || !strings.Contains(out, `"label": "Sicherung"`) {
		t.Fatalf("laib_planned for ops: exit %d: %s", code, out)
	}
}

// One source (#978 acceptance): a red inside a planned window of what the
// service depends on is expected to diag's look and to `laib planned`
// alike, with the same words; once the window is over it is red to both.
func TestDiagAndPlannedAgreeOnARedInAPlannedWindow(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	units := map[string][]string{"home-assistant.service": stopped("home-assistant.service", now.Add(-90*time.Second), now.Add(-60*time.Second))}
	down := true
	env := plannedBox(t, sickWhoami(t), units, &down)
	unit := func() diag.Finding { return whoamiUnit(t, env) }
	if f := unit(); f.Severity != "rot" {
		t.Fatalf("no journal entry accounts for the stop: red, %+v", f)
	}
	record(t, env, journal.Entry{T: now.Add(-50 * time.Second), Actor: "human:mdopp", Verb: "apply", Result: "green", MS: 45000})
	f, p := unit(), plannedOf(t, env, "whoami")
	if f.Severity != "info" || f.Repair != nil || !strings.HasPrefix(f.Wartet, "geplant: Anwenden von home-assistant um ") {
		t.Fatalf("diag: a red in the window of home-assistant's apply: %+v", f)
	}
	if "geplant: "+p.Now != f.Wartet {
		t.Fatalf("laib planned says %q, diag %q", p.Now, f.Wartet)
	}
	if !slices.ContainsFunc(p.Findings, func(g diag.Finding) bool { return g.Probe == "unit" && g.Wartet == f.Wartet }) {
		t.Fatalf("laib planned's findings are diag's: %+v", p.Findings)
	}

	// over: three minutes after the start
	units["home-assistant.service"] = stopped("home-assistant.service", now.Add(-10*time.Minute), now.Add(-9*time.Minute))
	if f, p := unit(), plannedOf(t, env, "whoami"); f.Severity != "rot" || p.Now != "" {
		t.Fatalf("after the window: diag %+v, planned now %q", f, p.Now)
	}

	// before the entry is written: an apply that holds what whoami uses (#907)
	unlock, _, err := apply.Lock(env.Paths, "apply:human:mdopp")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	apply.WritePending(env.Paths, []string{"home-assistant"}, "#978")
	want := "ein Apply richtet home-assistant gerade ein (apply:human:mdopp"
	if f, p := unit(), plannedOf(t, env, "whoami"); f.Severity != "info" || !strings.HasPrefix(f.Wartet, want) || !strings.HasPrefix(p.Now, want) {
		t.Fatalf("an apply of a dependency: diag %+v, planned now %q", f, p.Now)
	}
}
