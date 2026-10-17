package operator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// #285: the window comes from llama-server's /props (through the lease
// proxy, for the model the Operator asks for).
func TestContextWindowFromProps(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/props" || r.URL.Query().Get("model") != "qwen3.6-35b-a3b" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"default_generation_settings":{"n_ctx":131072},"model_path":"/models/q.gguf"}`))
	}))
	t.Cleanup(srv.Close)
	l := &LLM{URL: srv.URL + "/v1", Model: "qwen3.6-35b-a3b"}
	if n, err := l.ContextWindow(context.Background()); err != nil || n != 131072 {
		t.Fatalf("%d %v", n, err)
	}
	if n, err := (&LLM{URL: srv.URL + "/v1", Model: "gibtsnicht"}).ContextWindow(context.Background()); err == nil || n != 0 {
		t.Fatalf("an unknown model gave a window: %d", n)
	}
}

// #285: with 131k tokens the middle of the context (where the stack trace
// is) and a long tool result reach the model whole; at 32k the small
// model's budgets stay as they were.
func TestALargeWindowKeepsTheMiddleAndWholeResults(t *testing.T) {
	var huge strings.Builder
	for i := 0; i < 600; i++ {
		huge.WriteString("- 12:00 ops note whoami info Routineblick, Zeile mit etwas Text darin\n")
		if i == 300 {
			huge.WriteString("panic: runtime error: STACKTRACE IN DER MITTE\n")
		}
	}
	big := &Operator{Typed: true, Window: 131072}
	if p := big.prompt(Skill{Prompt: "x"}, Trigger{Kind: "tick"}, huge.String()); !strings.Contains(p, "STACKTRACE IN DER MITTE") {
		t.Fatal("the middle of the context was cut at 131k")
	}
	small := &Operator{Typed: true, Window: 32768}
	if p := small.prompt(Skill{Prompt: "x"}, Trigger{Kind: "tick"}, huge.String()); len(p) > 7000 {
		t.Fatalf("a 32k model's context grew to %d chars", len(p))
	}

	logs := strings.Repeat("Oct 17 03:12 whoami[1]: eine lange Logzeile mit Kontext\n", 400) + "LETZTE ZEILE"
	mcp, _ := fakeMCP(t, map[string]string{"laib_logs": logs})
	var seen string
	llm := fakeLLM(t, []Message{
		{Role: "assistant", ToolCalls: []ToolCall{tcall("1", "laib_logs", `{"service":"whoami"}`)}},
		{Role: "assistant", ToolCalls: []ToolCall{tcall("2", "laib_note", `{"kind":"watch","text":"gelesen"}`)}},
		{Role: "assistant", Content: "fertig"},
	})
	op := &Operator{MCP: &MCP{URL: mcp.URL, Token: "tok"}, LLM: &LLM{URL: llm.URL, Model: "test"}, Home: t.TempDir(), Typed: true, Window: 131072}
	op.LLM.Client = &http.Client{Transport: lastTool(func(c string) { seen = c })}
	tr := Trigger{Kind: "event", Service: "whoami"}
	if _, err := op.Run(context.Background(), skillFor(t, tr, ""), tr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(seen, "LETZTE ZEILE") || strings.Contains(seen, "\n…\n") || len(seen) < len(logs) {
		t.Fatalf("a %d-char result was cut to %d at 131k", len(logs), len(seen))
	}
}

// lastTool is a transport that shows the first tool result a request carries.
type lastTool func(string)

func (f lastTool) RoundTrip(r *http.Request) (*http.Response, error) {
	var req struct {
		Messages []Message `json:"messages"`
	}
	b, _ := readBody(r)
	json.Unmarshal(b, &req)
	for _, m := range req.Messages {
		if m.Role == "tool" && strings.Contains(m.Content, "Logzeile") {
			f(m.Content)
		}
	}
	return http.DefaultTransport.RoundTrip(r)
}
