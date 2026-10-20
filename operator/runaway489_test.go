package operator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// #489: a request without max_tokens let a model in a thinking loop hold a
// llama slot until the window was full (16,648 tokens, long after the
// Operator gave up at 300 s). Every request carries max_tokens and streams,
// so llama-server stops generating when the Operator closes the connection.
func TestChatCarriesMaxTokensAndStreams(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "text/event-stream")
		for _, ev := range []string{
			`{"choices":[{"delta":{"role":"assistant","content":"Ich "}}]}`,
			`{"choices":[{"delta":{"content":"schaue."}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"laib_status","arguments":"{\"ar"}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"gs\":\"\"}"}}]}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		} {
			fmt.Fprintf(w, "data: %s\n\n", ev)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	m, err := (&LLM{URL: srv.URL, Model: "test"}).Chat(context.Background(), []Message{{Role: "user", Content: "x"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := got["max_tokens"].(float64); n <= 0 {
		t.Errorf("request without max_tokens: %v", got)
	}
	if got["stream"] != true {
		t.Errorf("request does not stream: llama-server would not notice the Operator leaving: %v", got)
	}
	if m.Content != "Ich schaue." || len(m.ToolCalls) != 1 || m.ToolCalls[0].ID != "c1" ||
		m.ToolCalls[0].Function.Name != "laib_status" || m.ToolCalls[0].Function.Arguments != `{"args":""}` {
		t.Errorf("stream not assembled: %+v", m)
	}
}

func TestChatHonoursMaxTokensSetting(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": "ok"}, "finish_reason": "stop"}}})
	}))
	defer srv.Close()
	if _, err := (&LLM{URL: srv.URL, Model: "test", MaxTokens: 1234}).Chat(context.Background(), nil, nil); err != nil {
		t.Fatal(err)
	}
	if got["max_tokens"] != float64(1234) {
		t.Errorf("max_tokens = %v, want 1234", got["max_tokens"])
	}
}

// A reply cut at max_tokens is no answer: the session ends red with that reason,
// never as a silent "stopped without a note".
func TestChatCutAtMaxTokensIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hmm hmm\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"length\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()
	_, err := (&LLM{URL: srv.URL, Model: "test"}).Chat(context.Background(), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "max_tokens") {
		t.Errorf("a reply cut at max_tokens passed: %v", err)
	}
}

// On the timeout the Operator closes the connection, and the server sees it
// go: that is what makes llama-server free the slot.
func TestChatTimeoutClosesTheConnection(t *testing.T) {
	gone := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"denk\"}}]}\n\n")
		w.(http.Flusher).Flush()
		select { // a runaway generation: never ends on its own
		case <-r.Context().Done():
			close(gone)
		case <-time.After(10 * time.Second):
		}
	}))
	defer srv.Close()
	start := time.Now()
	_, err := (&LLM{URL: srv.URL, Model: "test", Timeout: 200 * time.Millisecond}).Chat(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("a runaway generation returned no error")
	}
	select {
	case <-gone:
	case <-time.After(3 * time.Second):
		t.Fatal("the connection stayed open after the timeout: llama-server keeps generating")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("the timeout took %v", d)
	}
}
