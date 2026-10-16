package operator

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func names(defs []ToolDef) []string {
	var out []string
	for _, d := range defs {
		out = append(out, d.Function.Name)
	}
	return out
}

func tcall(id, name, args string) ToolCall {
	var tc ToolCall
	tc.ID, tc.Type = id, "function"
	tc.Function.Name = name
	tc.Function.Arguments = args
	return tc
}

// #53: a small model gets a menu per trigger, task-shaped and typed, never
// the whole verb table; approve/reject/restore/rollback/converge/template/
// adopt are not in any menu, and a tool whose verb the hand lacks is absent.
func TestTypedMenusPerTrigger(t *testing.T) {
	has := map[string]bool{"laib_status": true, "laib_diag": true, "laib_logs": true, "laib_inspect": true, "laib_restart": true, "laib_propose": true, "laib_escalate": true, "laib_note": true, "laib_journal": true, "laib_approve": true, "laib_converge": true, "laib_exec": true}
	services := []string{"caddy", "whoami"}
	tick := names(TypedDefs(Trigger{Kind: "tick"}, services, has))
	if strings.Join(tick, ",") != "status,diagnose,note" {
		t.Fatalf("tick menu: %v", tick)
	}
	event := names(TypedDefs(Trigger{Kind: "event", Service: "whoami"}, services, has))
	if strings.Join(event, ",") != "diagnose,logs,inspect,restart,set_value,escalate,note" {
		t.Fatalf("incident menu: %v", event)
	}
	report := names(TypedDefs(Trigger{Kind: "report"}, services, has))
	if strings.Join(report, ",") != "status,diagnose,journal,note" {
		t.Fatalf("report menu: %v", report)
	}
	for _, d := range TypedDefs(Trigger{Kind: "event"}, services, has) {
		if d.Function.Name == "restart" {
			props := d.Function.Parameters["properties"].(map[string]any)
			svc := props["service"].(map[string]any)
			if enum, _ := svc["enum"].([]string); strings.Join(enum, ",") != "caddy,whoami" {
				t.Fatalf("service is an enum of the installed services: %v", svc)
			}
			if req, _ := d.Function.Parameters["required"].([]string); strings.Join(req, ",") != "service" {
				t.Fatalf("required: %v", req)
			}
		}
	}
	// a hand without restart (ops at ceiling propose in the trial, ADR 0019) gets no restart
	delete(has, "laib_restart")
	if got := names(TypedDefs(Trigger{Kind: "event"}, services, has)); strings.Contains(strings.Join(got, ","), "restart") {
		t.Fatalf("restart offered without the verb: %v", got)
	}
	// the typed interface must cost less than the verb table
	if n := len(TypedDefs(Trigger{Kind: "event"}, services, has)); n > 8 {
		t.Fatalf("%d tools in an incident menu", n)
	}
}

// #53: typed calls become CLI arguments in code — the model never writes
// quoting, flags or the -- separator.
func TestTypedCallsTranslateToCLIArgs(t *testing.T) {
	services := []string{"caddy", "whoami"}
	cases := []struct{ name, args, mcp, cli string }{
		{"status", `{}`, "laib_status", ""},
		{"diagnose", `{"service":"whoami"}`, "laib_diag", "whoami"},
		{"diagnose", `{}`, "laib_diag", ""},
		{"logs", `{"service":"whoami","minutes":120}`, "laib_logs", "whoami --since 120m"},
		{"logs", `{"service":"whoami"}`, "laib_logs", "whoami --since 60m"},
		{"inspect", `{"service":"caddy"}`, "laib_inspect", "caddy"},
		{"restart", `{"service":"whoami"}`, "laib_restart", "whoami"},
		{"set_value", `{"service":"whoami","key":"IMAGE","value":"ghcr.io/x/whoami:1.2","reason":"Port 8080 belegt"}`, "laib_propose", `"Port 8080 belegt" --set services/whoami/values.yml:IMAGE=ghcr.io/x/whoami:1.2`},
		{"note", `{"kind":"incident","text":"03:12 whoami rot, Neustart \"grün\""}`, "laib_note", `--kind incident "03:12 whoami rot, Neustart 'grün'"`},
		{"escalate", `{"service":"whoami","summary":"flattert"}`, "laib_escalate", `whoami "flattert"`},
		{"journal", `{"hours":24}`, "laib_journal", "--since 24h"},
	}
	for _, c := range cases {
		mcp, args, err := Translate(c.name, json.RawMessage(c.args), services)
		if err != nil {
			t.Fatalf("%s %s: %v", c.name, c.args, err)
		}
		if mcp != c.mcp || args["args"] != c.cli {
			t.Fatalf("%s %s → %s %q, want %s %q", c.name, c.args, mcp, args["args"], c.mcp, c.cli)
		}
	}
}

// #53 acceptance: a call with missing or invalid arguments is rejected,
// never executed — an unknown service, a kind outside the enum, a key
// with a path in it, a tool outside the menu.
func TestTypedInvalidArgumentsAreRefused(t *testing.T) {
	services := []string{"caddy", "whoami"}
	bad := []struct{ name, args, why string }{
		{"restart", `{}`, "service"},
		{"restart", `{"service":"nas"}`, "nas"},
		{"restart", `{"service":"whoami; rm -rf /"}`, "whoami; rm"},
		{"note", `{"kind":"panic","text":"x"}`, "kind"},
		{"note", `{"kind":"watch"}`, "text"},
		{"set_value", `{"service":"whoami","key":"../box.yml:DOMAIN","value":"x","reason":"r"}`, "key"},
		{"logs", `{"service":"whoami","minutes":"viele"}`, "minutes"},
		{"logs", `{"service":"whoami","minutes":100000}`, "minutes"},
		{"approve", `{"id":17}`, "approve"},
		{"restart", `not json`, "JSON"},
	}
	for _, c := range bad {
		if _, _, err := Translate(c.name, json.RawMessage(c.args), services); err == nil || !strings.Contains(err.Error(), c.why) {
			t.Fatalf("%s %s must be refused naming %q: %v", c.name, c.args, c.why, err)
		}
	}
}

// #53: the loop in typed mode — the model calls the typed tools, laib sees
// CLI arguments, the session records the typed names, and a refused call
// is answered to the model but never reaches the MCP.
func TestTypedIncidentSessionRunsEndToEnd(t *testing.T) {
	mcp, calls := fakeMCP(t, map[string]string{"laib_logs": "Oct 11 03:12 whoami[1]: bind: address already in use", "laib_restart": "grün: whoami"})
	llm := fakeLLM(t, []Message{
		{Role: "assistant", ToolCalls: []ToolCall{tcall("1", "logs", `{"service":"whoami","minutes":30}`)}},
		{Role: "assistant", ToolCalls: []ToolCall{tcall("2", "restart", `{"service":"nas"}`)}},
		{Role: "assistant", ToolCalls: []ToolCall{tcall("3", "restart", `{"service":"whoami"}`)}},
		{Role: "assistant", ToolCalls: []ToolCall{tcall("4", "note", `{"kind":"incident","text":"03:12 whoami: Port belegt, Neustart grün."}`)}},
		{Role: "assistant", Content: "fertig"},
	})
	op := &Operator{MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL, Model: "test"}, Home: t.TempDir(), Typed: true, Services: []string{"caddy", "whoami"}}
	s, err := op.Run(context.Background(), Trigger{Kind: "event", Service: "whoami", Detail: "unit whoami.service failed"})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(*calls, "\n")
	for _, want := range []string{"laib_logs whoami --since 30m", "laib_restart whoami", `laib_note --kind incident "03:12 whoami`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "nas") {
		t.Fatalf("the refused call reached laib:\n%s", got)
	}
	if s.Outcome != "green" || len(s.Calls) != 3 || !strings.HasPrefix(s.Calls[0], "logs ") {
		t.Fatalf("%+v", s)
	}
}

// #53: the eval runs the typed interface over the same incidents.
func TestEvalTypedIncidents(t *testing.T) {
	incidents, err := LoadIncidents(filepath.Join("..", "werkstatt", "evals", "incidents-typed.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(incidents) < 6 {
		t.Fatalf("%d incidents", len(incidents))
	}
	rep := RunEval(context.Background(), "scripted", incidents, func(ctx context.Context, inc Incident) (Session, error) {
		mcp, _ := fakeMCP(t, inc.Replies)
		srv := fakeLLM(t, inc.Script)
		op := &Operator{MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: srv.URL, Model: "test"}, Home: t.TempDir(), Typed: true, Services: []string{"whoami", "frage", "bote", "file-share"}}
		return op.Run(ctx, inc.Trigger)
	})
	if rep.Wrong != 0 || rep.Passed != rep.Total {
		t.Fatalf("%+v", rep)
	}
}

// the service enum comes from laib status — JSON over MCP, text on the CLI
func TestServicesFromStatus(t *testing.T) {
	got := ServicesFromStatus(`{"ready":{"state":"bereit"},"services":[{"name":"whoami","state":"rot"},{"name":"caddy","state":"grün"}]}`)
	if strings.Join(got, ",") != "caddy,whoami" {
		t.Fatalf("json: %v", got)
	}
	got = ServicesFromStatus("Laib · bereit · 2 Dienste\n  grün  caddy      \n  rot   whoami  unit failed\nLetzte Änderung …")
	if strings.Join(got, ",") != "caddy,whoami" {
		t.Fatalf("text: %v", got)
	}
	if got := ServicesFromStatus("kaputt"); len(got) != 0 {
		t.Fatalf("nothing: %v", got)
	}
}
