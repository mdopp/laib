package main

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mdopp/laib/internal/cli"
	"github.com/mdopp/laib/internal/journal"
	"github.com/mdopp/laib/internal/mcp"
	"github.com/mdopp/laib/internal/state"
)

func schemaState() *state.State {
	return &state.State{
		Services: map[string]*state.Service{"whoami": {Name: "whoami"}, "caddy": {Name: "caddy"}},
		Policy:   state.Policy{Hands: map[string]state.Hand{"ops": {Ceiling: "apply"}}},
	}
}

// #290: a tool publishes typed properties — the service as an enum of the
// installed services — not one free-form command line.
func TestMcpToolsPublishTypedSchemas(t *testing.T) {
	var logs map[string]any
	for _, tl := range mcp.Tools(schemaState(), "ops") {
		if tl["name"] == "laib_logs" {
			logs = tl["inputSchema"].(map[string]any)
		}
	}
	props, _ := logs["properties"].(map[string]any)
	svc, _ := props["service"].(map[string]any)
	if svc == nil || props["since"] == nil || props["args"] != nil {
		t.Fatalf("laib_logs schema: %v", logs)
	}
	if enum, _ := svc["enum"].([]string); strings.Join(enum, ",") != "caddy,whoami" {
		t.Fatalf("service enum: %v", svc)
	}
	if req, _ := logs["required"].([]string); !slices.Equal(req, []string{"service"}) {
		t.Fatalf("required: %v", logs["required"])
	}
}

// #290: typed arguments become argv elements directly — no string joined
// and split again, so quotes and spaces survive; raw args still work.
func TestMcpArgvFromTypedArguments(t *testing.T) {
	st := schemaState()
	for _, c := range []struct {
		verb string
		args map[string]any
		want []string
	}{
		{"logs", map[string]any{"service": "whoami", "since": "2h"}, []string{"whoami", "--since", "2h"}},
		{"note", map[string]any{"kind": "incident", "text": `Neustart "grün" um 03:12`}, []string{"--kind", "incident", `Neustart "grün" um 03:12`}},
		{"propose", map[string]any{"service": "whoami", "key": "IMAGE", "value": "ghcr.io/x/w:1.2", "reason": "Port 8080 \"belegt\""}, []string{"Port 8080 \"belegt\"", "--set", "services/whoami/values.yml:IMAGE=ghcr.io/x/w:1.2"}},
		{"look", map[string]any{"service": "whoami", "command": []any{"cat", "/etc/a b"}}, []string{"whoami", "--", "cat", "/etc/a b"}},
		{"escalate", map[string]any{"service": "caddy", "summary": "flattert seit 03:00"}, []string{"caddy", "flattert seit 03:00"}},
		{"status", map[string]any{}, nil},
		{"logs", map[string]any{"args": `whoami --since 1h`}, []string{"whoami", "--since", "1h"}},
	} {
		got, _, err := mcp.Argv(st, c.verb, c.args)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s %v → %q, %v; want %q", c.verb, c.args, got, err, c.want)
		}
	}
	argv, _, err := mcp.Argv(st, "wish", map[string]any{"text": "Abfuhrkalender anbinden.", "saw": "9 offene Fragen", "better": "familie"})
	if err != nil || len(argv) != 1 || !strings.Contains(argv[0], "Gesehen: 9 offene Fragen") || !strings.Contains(argv[0], "Besser wird: der Komfort der Familie") {
		t.Fatalf("wish: %q %v", argv, err)
	}
}

// #290: what the schema does not allow is refused before any verb runs.
func TestMcpRefusesArgumentsOutsideTheSchema(t *testing.T) {
	st := schemaState()
	for _, c := range []struct {
		verb string
		args map[string]any
	}{
		{"logs", map[string]any{"service": "nas"}},
		{"logs", map[string]any{"since": "1h"}},
		{"logs", map[string]any{"service": "whoami", "color": "red"}},
		{"logs", map[string]any{"service": 3}},
		{"note", map[string]any{"kind": "panic", "text": "x"}},
		{"propose", map[string]any{"service": "whoami", "key": "../box.yml:DOMAIN", "value": "x", "reason": "r"}},
		{"propose", map[string]any{"service": "whoami", "key": "IMAGE", "value": "a\nb", "reason": "r"}},
		{"restart", map[string]any{"service": "whoami; rm -rf /"}},
	} {
		if argv, _, err := mcp.Argv(st, c.verb, c.args); err == nil {
			t.Errorf("%s %v passed as %q", c.verb, c.args, argv)
		}
	}
	out, code := mcp.Call(context.Background(), &cliEnv{}, st, "ops", "laib_restart", map[string]any{"service": "nas"})
	if code != cli.ExitUsage || !strings.Contains(out, "nas") {
		t.Fatalf("%d %q", code, out)
	}
}

// #290 acceptance: a note with quotes and spaces reaches the journal whole.
func TestATypedNoteKeepsItsQuotes(t *testing.T) {
	c := newCbox(t)
	st, err := cli.LoadState(c.env)
	if err != nil {
		t.Fatal(err)
	}
	text := `whoami war rot: "bind: address in use"; Neustart um 03:12, jetzt grün.`
	if out, code := mcp.Call(context.Background(), c.env, st, "ops", "laib_note", map[string]any{"kind": "incident", "text": text}); code != cli.ExitOK {
		t.Fatalf("%d %s", code, out)
	}
	es, _ := journal.Read(c.env.Paths.Journal, time.Time{})
	if len(es) == 0 || es[len(es)-1].Detail != text {
		t.Fatalf("journal: %+v", es)
	}
}
