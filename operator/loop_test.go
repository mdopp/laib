package operator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeMCP answers tools/list with a few laib_* tools and records calls.
func fakeMCP(t *testing.T, replies map[string]string) (*httptest.Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "unauthorized", 401)
			return
		}
		var req struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		reply := func(res any) {
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": res})
		}
		switch req.Method {
		case "initialize":
			reply(map[string]any{"protocolVersion": "2025-03-26"})
		case "tools/list":
			var tools []map[string]any
			for _, n := range []string{"laib_status", "laib_diag", "laib_logs", "laib_inspect", "laib_restart", "laib_journal", "laib_propose", "laib_note", "laib_escalate", "laib_context", "laib_exec"} {
				tools = append(tools, map[string]any{"name": n, "description": n, "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"args": map[string]any{"type": "string"}}}})
			}
			reply(map[string]any{"tools": tools})
		case "tools/call":
			var p struct {
				Name string            `json:"name"`
				Args map[string]string `json:"arguments"`
			}
			json.Unmarshal(req.Params, &p)
			mu.Lock()
			calls = append(calls, p.Name+" "+p.Args["args"])
			mu.Unlock()
			text, ok := replies[p.Name]
			if !ok {
				text = "ok"
			}
			reply(map[string]any{"content": []map[string]string{{"type": "text", "text": text}}, "isError": strings.Contains(text, "Budget")})
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// fakeLLM plays a script: each entry is either tool calls or a final text.
func fakeLLM(t *testing.T, script []Message) *httptest.Server {
	t.Helper()
	i := 0
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		var req struct {
			Messages []Message `json:"messages"`
			Tools    []ToolDef `json:"tools"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if len(req.Tools) == 0 || req.Messages[0].Role != "system" {
			http.Error(w, "no tools or no system prompt", 400)
			return
		}
		mu.Lock()
		m := script[len(script)-1]
		if i < len(script) {
			m = script[i]
		}
		i++
		mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": m}}})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func call(id, name, args string) ToolCall {
	var tc ToolCall
	tc.ID, tc.Type = id, "function"
	tc.Function.Name = name
	tc.Function.Arguments = `{"args":` + strconvQuote(args) + `}`
	return tc
}

func strconvQuote(s string) string { b, _ := json.Marshal(s); return string(b) }

func TestIncidentSessionLooksThenRestartsThenNotes(t *testing.T) {
	mcp, calls := fakeMCP(t, map[string]string{"laib_inspect": "unit whoami.service failed exit=1", "laib_restart": "grün: whoami"})
	llm := fakeLLM(t, []Message{
		{Role: "assistant", ToolCalls: []ToolCall{call("1", "laib_inspect", "whoami")}},
		{Role: "assistant", ToolCalls: []ToolCall{call("2", "laib_restart", "whoami")}},
		{Role: "assistant", ToolCalls: []ToolCall{call("3", "laib_note", `--kind incident "whoami war rot (exit 1), Neustart um 03:12, jetzt grün."`)}},
		{Role: "assistant", Content: "fertig"},
	})
	op := &Operator{MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL, Model: "test"}, Home: t.TempDir()}
	s, err := op.Run(context.Background(), skillFor(t, Trigger{Kind: "event", Service: "whoami", Detail: "health red"}, ""), Trigger{Kind: "event", Service: "whoami", Detail: "health red"})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(*calls, "\n")
	for _, want := range []string{"laib_context", "laib_inspect whoami", "laib_restart whoami", "laib_note --kind incident"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in\n%s", want, got)
		}
	}
	if s.Outcome != "green" || len(s.Calls) != 3 {
		t.Fatalf("%+v", s)
	}
}

func TestBudgetAnswerTurnsTheSessionRedAndEscalationCounts(t *testing.T) {
	mcp, calls := fakeMCP(t, map[string]string{"laib_restart": "Budget für whoami erschöpft"})
	llm := fakeLLM(t, []Message{
		{Role: "assistant", ToolCalls: []ToolCall{call("1", "laib_restart", "whoami")}},
		{Role: "assistant", ToolCalls: []ToolCall{call("2", "laib_escalate", `whoami "Budget weg, Neustart hilft nicht"`)}},
		{Role: "assistant", Content: "eskaliert"},
	})
	op := &Operator{MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL, Model: "test"}, Home: t.TempDir()}
	s, err := op.Run(context.Background(), skillFor(t, Trigger{Kind: "event", Service: "whoami"}, ""), Trigger{Kind: "event", Service: "whoami"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Outcome != "escalated" {
		t.Fatalf("outcome %s", s.Outcome)
	}
	// the model ended without a note: the operator writes one itself
	if !strings.Contains(strings.Join(*calls, "\n"), "laib_note --kind incident") {
		t.Fatal("no note written for a silent end")
	}
}

func TestTooManyCallsEndsRed(t *testing.T) {
	mcp, _ := fakeMCP(t, nil)
	llm := fakeLLM(t, []Message{{Role: "assistant", ToolCalls: []ToolCall{call("x", "laib_status", "")}}}) // loops forever
	op := &Operator{MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL, Model: "test"}, Home: t.TempDir(), MaxCalls: 4}
	s, err := op.Run(context.Background(), skillFor(t, Trigger{Kind: "tick"}, ""), Trigger{Kind: "tick"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Outcome != "red" || len(s.Calls) != 4 {
		t.Fatalf("%+v", s)
	}
}

func TestWrongTokenIsAnError(t *testing.T) {
	mcp, _ := fakeMCP(t, nil)
	op := &Operator{MCP: &MCP{URL: mcp.URL, Token: "nope"}, LLM: &LLM{URL: "http://127.0.0.1:1", Model: "x"}}
	if _, err := op.Run(context.Background(), skillFor(t, Trigger{Kind: "tick"}, ""), Trigger{Kind: "tick"}); err == nil {
		t.Fatal("expected an auth error")
	}
}
