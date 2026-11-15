package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/apply"
	"github.com/mdopp/laib/internal/aufgaben"
	"github.com/mdopp/laib/internal/backup"
	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/inbox"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/proposal"
	"github.com/mdopp/laib/internal/state"
	"github.com/mdopp/laib/internal/sysd"
)

// #1209, the box on 2026-11-14: three independent approvals in one run,
// one of them red on a service with data. Only that one goes back, and
// the two follow-up cards (#853) name only its service; the other two
// stay applied and offer nothing.
// Ablauf L-9, L-10
func TestOneRedAmongThreeKeepsTheGreenOnesAndOffersOnlyForTheRed(t *testing.T) {
	ctx := context.Background()
	env, _ := testBox(t)
	svc := filepath.Join(env.Paths.State, "services", "vault")
	os.MkdirAll(svc, 0o755)
	data := filepath.Join(t.TempDir(), "vault")
	os.MkdirAll(data, 0o755)
	for name, content := range map[string]string{
		"laib.yml":        "name: vault\ndata:\n  - path: " + data + "\n    quiesce: true\n",
		"values.yml":      "IMAGE: docker.io/vaultwarden/server:latest\n",
		"vault.container": "[Container]\nImage=${IMAGE}\n\n[Install]\nWantedBy=default.target\n",
	} {
		os.WriteFile(filepath.Join(svc, name), []byte(content), 0o644)
	}
	if err := proposal.CommitDirty(ctx, env.Paths.State, "human:test"); err != nil {
		t.Fatal(err)
	}
	st, _ := cli.LoadState(env)
	if code := apply.ApplyLocked(ctx, &cliEnv{Paths: env.Paths, Actor: "human:test", Out: io.Discard}, st, "", "vault"); code != cli.ExitOK {
		t.Fatalf("fixture apply: %d", code)
	}
	w1 := proposeService(t, env, "w1")
	pin := "docker.io/vaultwarden/server:latest@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if out, code := mcp.Call(ctx, env, st, "ops", "laib_propose", map[string]any{"reason": "vault: neues Image", "service": "vault", "key": "IMAGE", "value": pin}); code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	open, _ := proposal.List(cli.InboxDir(env), true)
	vault := open[0]
	w3 := proposeService(t, env, "w3")
	run := sysd.Run
	sysd.Run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "systemctl" && slices.Contains(args, "is-active") && slices.Contains(args, "vault.service") {
			if b, _ := os.ReadFile(filepath.Join(env.Paths.Quadlet, "vault", "vault.container")); strings.Contains(string(b), pin) {
				return "failed", nil // the new image does not come up
			}
		}
		return run(ctx, name, args...)
	}
	realRestic := backup.ResticRun
	t.Cleanup(func() { backup.ResticRun = realRestic })
	backup.ResticRun = func(_ context.Context, _ *state.State, _ string, args ...string) (string, error) {
		if args[0] == "backup" {
			return "Files: 3 new\nsnapshot 4f9a2c1e saved", nil
		}
		return "", nil
	}
	unlock, _ := apply.TakeLock(env, st, "apply")
	for _, p := range []*proposal.Proposal{w1, vault, w3} {
		inbox.Approve(ctx, &cliEnv{Paths: env.Paths, Actor: "human:test", Out: io.Discard, ErrOut: io.Discard}, []string{fmt.Sprint(p.ID)})
	}
	unlock()
	get := func(id int) *proposal.Proposal { p, _ := proposal.Get(cli.InboxDir(env), id); return p }
	for _, p := range []*proposal.Proposal{get(w1.ID), get(w3.ID)} {
		if p.Outcome == "rolled-back" || p.Restore != 0 || p.Forward != 0 || p.Because != "" {
			t.Fatalf("the green #%d went back or got follow-ups: %+v", p.ID, p)
		}
		if _, err := os.Stat(filepath.Join(env.Paths.Quadlet, "w"+strconv.Itoa(p.ID))); err != nil {
			t.Fatalf("the green #%d is not on the box", p.ID)
		}
	}
	v := get(vault.ID)
	if v.Outcome != "rolled-back" || v.Restore == 0 || v.Forward == 0 {
		t.Fatalf("the red one has no follow-ups: %+v", v)
	}
	if b, _ := os.ReadFile(filepath.Join(env.Paths.Quadlet, "vault", "vault.container")); strings.Contains(string(b), pin) {
		t.Fatal("the old program is not back")
	}
	if !strings.Contains(v.Because, fmt.Sprintf("#%d und #%d aus demselben Lauf sind angewendet", w1.ID, w3.ID)) || !strings.Contains(v.Because, "Das alte Programm läuft wieder.") {
		t.Fatalf("the red card does not say which went through and where the data are: %q", v.Because)
	}
	var laibs []*proposal.Proposal
	all, _ := proposal.List(cli.InboxDir(env), true)
	for _, p := range all {
		if p.Actor == "laib" {
			laibs = append(laibs, p)
		}
	}
	if len(laibs) != 2 {
		t.Fatalf("want exactly the two follow-ups of the red one: %+v", laibs)
	}
	for _, q := range laibs {
		c := proposal.Card(q, "box.test")
		text := c.Summary + " " + strings.Join(c.Impact, " ") + " " + q.Reason
		if !strings.Contains(text, "Vault") && !strings.Contains(text, "vault") || strings.Contains(text, "W1") || strings.Contains(text, "W3") || strings.Contains(text, "w1") || strings.Contains(text, "w3") {
			t.Errorf("follow-up #%d names more than the red one: %s", q.ID, text)
		}
	}
	if diff := proposal.Diff(ctx, env.Paths.State, get(v.Forward)); strings.Count(diff, "laib rollback ") != 1 {
		t.Errorf("the forward puts on more than the red one's change: %s", diff)
	}
}

// #1209: a hook names a precondition only the owner meets (`laib-wartet:`).
// The card does not go red: a Box-Aufgabe for the admins, the same change
// as a fresh card approved in advance, applied once the task is done; the
// green proposal of the same run stays applied.
// Ablauf L-48
func TestAPreconditionHoldsTheCardForTheOwnersTask(t *testing.T) {
	ctx := context.Background()
	env, _ := testBox(t)
	hook := filepath.Join(env.Paths.State, "services", "whoami", "post-deploy")
	os.WriteFile(hook, []byte("#!/bin/sh\n"), 0o755)
	if err := proposal.CommitDirty(ctx, env.Paths.State, "human:test"); err != nil {
		t.Fatal(err)
	}
	met := false
	realHook := apply.RunHook
	t.Cleanup(func() { apply.RunHook = realHook })
	apply.RunHook = func(_ context.Context, h apply.HookSpec) (string, error) {
		if b, _ := os.ReadFile(h.EnvFile); strings.Contains(string(b), "GATE=on") && !met {
			return "whoami: the gate is closed\nlaib-wartet: Tor öffnen | Öffne das Tor in whoami, dann abhaken.", fmt.Errorf("exit status 1")
		}
		return "", nil
	}
	st, _ := cli.LoadState(env)
	other, human := &cliEnv{Paths: env.Paths, Actor: "human:other", Out: io.Discard, ErrOut: io.Discard}, &cliEnv{Paths: env.Paths, Actor: "human:test", Out: io.Discard, ErrOut: io.Discard}
	w1 := proposeService(t, env, "w1")
	if code := inbox.Propose(ctx, other, []string{"--set", "services/whoami/values.yml:GATE=on", "Tor auf"}); code != cli.ExitOK {
		t.Fatalf("propose: %d", code)
	}
	open, _ := proposal.List(cli.InboxDir(env), true)
	gate := open[0]
	unlock, _ := apply.TakeLock(env, st, "apply")
	for _, p := range []*proposal.Proposal{w1, gate} {
		inbox.Approve(ctx, human, []string{fmt.Sprint(p.ID)})
	}
	unlock()
	get := func(id int) *proposal.Proposal { p, _ := proposal.Get(cli.InboxDir(env), id); return p }
	values := func() string {
		b, _ := os.ReadFile(filepath.Join(env.Paths.State, "services", "whoami", "values.yml"))
		return string(b)
	}
	if p := get(w1.ID); p.Outcome == "rolled-back" {
		t.Fatalf("the green one went back with the waiting one: %+v", p)
	}
	g := get(gate.ID)
	if g.Refiled == 0 || !strings.HasPrefix(g.Because, "wartet auf Box-Aufgabe #") || strings.Contains(values(), "GATE") {
		t.Fatalf("the card does not wait for the task: %+v\n%s", g, values())
	}
	list, _ := aufgaben.Load(env.Paths.State)
	i := slices.IndexFunc(list, func(a aufgaben.Aufgabe) bool { return a.Titel == "Tor öffnen" })
	if i < 0 || list[i].Warum != "Öffne das Tor in whoami, dann abhaken." || list[i].Fuer != aufgaben.Admins {
		t.Fatalf("no task for the admins: %+v", list)
	}
	q := get(g.Refiled)
	if q.Status != "open" || q.Freigabe != "human:test" || !slices.Equal(q.WartetAuf, []string{"aufgabe:" + strconv.Itoa(list[i].ID)}) {
		t.Fatalf("the fresh card is not approved in advance and held for the task: %+v", q)
	}
	body := inboxMux(env)("GET", "/inbox", nil)
	if !strings.Contains(body, "Tor öffnen") || strings.Contains(body, "Die Box ist wieder wie vorher") {
		t.Errorf("the Inbox shows the card red instead of waiting:\n%s", body)
	}
	met = true // the owner did it
	aufgaben.Update(ctx, env.Paths.State, "human:test", func(l []aufgaben.Aufgabe) ([]aufgaben.Aufgabe, string, error) {
		return l, "aufgabe erledigt", aufgaben.Done(l, list[i].ID, "human:test", "", time.Now())
	})
	inbox.Carry(ctx, &cliEnv{Paths: env.Paths, Actor: "laib", Out: io.Discard, ErrOut: io.Discard})
	if p := get(q.ID); p.Status != "approved" || p.Outcome == "rolled-back" || !strings.Contains(values(), "GATE: on") {
		t.Fatalf("the task is done: the change is applied without a second tap: %+v\n%s", p, values())
	}
}
