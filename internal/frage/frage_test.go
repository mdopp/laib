package frage

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fakes(t *testing.T, hits []Hit) (string, string) {
	t.Helper()
	searx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("format") != "json" || r.URL.Query().Get("q") == "" {
			http.Error(w, "bad query", 400)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"results": hits})
	}))
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []map[string]string `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		user := req.Messages[len(req.Messages)-1]["content"]
		if !strings.Contains(user, "Treffer:") || !strings.Contains(user, "[1]") {
			http.Error(w, "no hits in prompt", 400)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": "Einslive ist ein Radiosender des WDR."}}}})
	}))
	t.Cleanup(searx.Close)
	t.Cleanup(model.Close)
	return searx.URL, model.URL
}

func TestAskSearchesThenSummarises(t *testing.T) {
	s, m := fakes(t, []Hit{{Title: "1LIVE", URL: "https://www1.wdr.de/radio/1live/", Content: "1LIVE ist das junge Radio des WDR."}})
	c := Config{SearxURL: s, ModelURL: m + "/v1", ModelName: "test"}
	srv := httptest.NewServer(Handler(c))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/ask?q=was+ist+einslive")
	if err != nil {
		t.Fatal(err)
	}
	var a Answer
	json.NewDecoder(resp.Body).Decode(&a)
	if !strings.Contains(a.Answer, "WDR") || len(a.Sources) != 1 {
		t.Fatalf("%+v", a)
	}
	// the OpenAI-compatible route: HA Assist's conversation agent and the Bote use this
	body := strings.NewReader(`{"messages":[{"role":"system","content":"x"},{"role":"user","content":"was ist einslive"}]}`)
	resp2, _ := http.Post(srv.URL+"/v1/chat/completions", "application/json", body)
	var out struct {
		Choices []struct {
			Message struct{ Content string } `json:"message"`
		} `json:"choices"`
	}
	json.NewDecoder(resp2.Body).Decode(&out)
	if len(out.Choices) != 1 || !strings.Contains(out.Choices[0].Message.Content, "WDR") {
		t.Fatalf("%+v", out)
	}
}

func TestNoHitsIsAnHonestSentence(t *testing.T) {
	s, m := fakes(t, nil)
	c := Config{SearxURL: s, ModelURL: m + "/v1"}
	a, err := c.Ask(t.Context(), "xyzzy")
	if err != nil || !strings.Contains(a.Answer, "nichts gefunden") {
		t.Fatalf("%+v %v", a, err)
	}
	if _, err := c.Ask(t.Context(), "  "); err == nil {
		t.Fatal("empty question accepted")
	}
}

// #54 (1): Frage is a tool the Bote can call — the same answer as /ask,
// served as MCP on /mcp so Hermes lists it like laib and automat.
func TestFrageIsAnMCPTool(t *testing.T) {
	s, m := fakes(t, []Hit{{Title: "1LIVE", URL: "https://www1.wdr.de/radio/1live/", Content: "1LIVE ist das junge Radio des WDR."}})
	c := Config{SearxURL: s, ModelURL: m + "/v1", ModelName: "test"}
	srv := httptest.NewServer(Handler(c))
	defer srv.Close()
	call := func(body string) map[string]any {
		resp, err := http.Post(srv.URL+"/mcp", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		json.NewDecoder(resp.Body).Decode(&out)
		return out
	}
	list := call(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	res, _ := list["result"].(map[string]any)
	tools, _ := res["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != "frage" {
		t.Fatalf("one tool, frage: %v", list)
	}
	out := call(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"frage","arguments":{"frage":"was ist einslive"}}}`)
	text, _ := json.Marshal(out["result"])
	if !strings.Contains(string(text), "WDR") || !strings.Contains(string(text), "www1.wdr.de") {
		t.Fatalf("the answer with its sources: %s", text)
	}
}
