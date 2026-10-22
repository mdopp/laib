package operator

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// #290: the Operator hands the model the kernel's typed schemas and passes
// the model's arguments through as they are — no CLI string built here.
func TestStructuredArgumentsPassThrough(t *testing.T) {
	var got []string
	schema := map[string]any{"type": "object", "properties": map[string]any{
		"kind": map[string]any{"type": "string", "enum": []string{"watch", "incident", "report"}},
		"text": map[string]any{"type": "string"}}, "required": []string{"kind", "text"}}
	mcp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		res := map[string]any{}
		switch req.Method {
		case "tools/list":
			res["tools"] = []map[string]any{{"name": "laib_note", "inputSchema": schema}, {"name": "laib_status", "inputSchema": map[string]any{"type": "object"}}}
		case "tools/call":
			var p struct {
				Name string          `json:"name"`
				Args json.RawMessage `json:"arguments"`
			}
			json.Unmarshal(req.Params, &p)
			if p.Name != "laib_context" && p.Name != "laib_inbox" { // the starter bundle and the record (#544)
				got = append(got, p.Name+" "+string(p.Args))
			}
			res["content"] = []map[string]string{{"type": "text", "text": "ok"}}
		}
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": res})
	}))
	t.Cleanup(mcp.Close)
	var offered []ToolDef
	llm := fakeLLM(t, []Message{
		{Role: "assistant", ToolCalls: []ToolCall{tcall("1", "laib_note", `{"kind":"incident","text":"whoami: \"bind: in use\", Neustart 03:12"}`)}},
		{Role: "assistant", Content: "fertig"},
	})
	op := &Operator{MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL, Model: "test"}, Home: t.TempDir(), Typed: true}
	op.LLM.Client = &http.Client{Transport: tools(func(d []ToolDef) { offered = d })}
	sk := Skill{Name: "x", Status: "active", Tools: []string{"status", "note"}, Granted: []string{"status", "note"}, Output: "note", Prompt: "p"}
	s, err := op.Run(context.Background(), sk, Trigger{Kind: "tick"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != `laib_note {"kind":"incident","text":"whoami: \"bind: in use\", Neustart 03:12"}` {
		t.Fatalf("calls: %q", got)
	}
	var note *ToolDef
	for i := range offered {
		if offered[i].Function.Name == "laib_note" {
			note = &offered[i]
		}
	}
	if note == nil || !strings.Contains(mustJSON(note.Function.Parameters), `"enum":["watch","incident","report"]`) {
		t.Fatalf("the model did not get the kernel's schema: %+v", offered)
	}
	if s.Outcome != "green" {
		t.Fatalf("%+v", s)
	}
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

// tools is a transport that shows the tool definitions a request offers.
type tools func([]ToolDef)

func (f tools) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method != http.MethodPost {
		return http.DefaultTransport.RoundTrip(r) // the lease and the catalogue (#550)
	}
	var req struct {
		Tools []ToolDef `json:"tools"`
	}
	b, _ := readBody(r)
	json.Unmarshal(b, &req)
	f(req.Tools)
	return http.DefaultTransport.RoundTrip(r)
}

func readBody(r *http.Request) ([]byte, error) {
	b, err := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(b))
	return b, err
}
