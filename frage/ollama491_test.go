package frage

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// #491: Home Assistant 2026.10 has no built-in conversation integration for an
// OpenAI-compatible server with its own base URL, but its Ollama integration
// takes any URL. Frage speaks the part of the Ollama API it uses — /api/tags
// (the config flow's check and the model list), /api/version, /api/chat
// (streamed as NDJSON, or one object) — the way Solaris's facade did.
func TestFrageSpeaksOllamaForHomeAssistant(t *testing.T) {
	s, m := fakes(t, []Hit{{Title: "1LIVE", URL: "https://www1.wdr.de/radio/1live/", Content: "1LIVE ist das junge Radio des WDR."}})
	srv := httptest.NewServer(Handler(Config{SearxURL: s, ModelURL: m + "/v1", ModelName: "test"}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/tags")
	if err != nil {
		t.Fatal(err)
	}
	var tags struct {
		Models []struct {
			Name, Model, Digest string
			ModifiedAt          string `json:"modified_at"`
		} `json:"models"`
	}
	json.NewDecoder(resp.Body).Decode(&tags)
	if len(tags.Models) != 1 || tags.Models[0].Name != "frage" || tags.Models[0].Model != "frage" || tags.Models[0].ModifiedAt == "" {
		t.Fatalf("/api/tags lists no model frage the Ollama client can parse: %+v", tags)
	}
	if r, err := http.Get(srv.URL + "/api/version"); err != nil || r.StatusCode != 200 {
		t.Fatalf("/api/version: %v %v", r, err)
	}

	// HA streams by default and resends the history with its own system prompt
	body := `{"model":"frage","messages":[{"role":"system","content":"Du bist ein Sprachassistent."},{"role":"user","content":"Hallo"},{"role":"assistant","content":"Hallo!"},{"role":"user","content":"was ist einslive"}],"stream":true}`
	resp, err = http.Post(srv.URL+"/api/chat", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/x-ndjson" {
		t.Errorf("streamed /api/chat is %q, not NDJSON", ct)
	}
	var text string
	var last map[string]any
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		var chunk struct {
			Message struct{ Role, Content string } `json:"message"`
			Done    bool                           `json:"done"`
		}
		if err := json.Unmarshal(sc.Bytes(), &chunk); err != nil {
			t.Fatalf("not one JSON object per line: %q", sc.Text())
		}
		text += chunk.Message.Content
		json.Unmarshal(sc.Bytes(), &last)
	}
	if !strings.Contains(text, "WDR") || last["done"] != true || last["done_reason"] != "stop" {
		t.Fatalf("streamed answer %q, last chunk %v", text, last)
	}

	resp, err = http.Post(srv.URL+"/api/chat", "application/json", strings.NewReader(`{"model":"frage:latest","stream":false,"messages":[{"role":"user","content":"was ist einslive"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var one struct {
		Model   string                         `json:"model"`
		Message struct{ Role, Content string } `json:"message"`
		Done    bool                           `json:"done"`
	}
	json.NewDecoder(resp.Body).Decode(&one)
	if !one.Done || one.Message.Role != "assistant" || !strings.Contains(one.Message.Content, "WDR") {
		t.Fatalf("/api/chat stream:false: %+v", one)
	}
}

// A search or model failure is a spoken sentence, never silence on the speaker.
func TestOllamaChatFailureIsASentence(t *testing.T) {
	srv := httptest.NewServer(Handler(Config{SearxURL: "http://127.0.0.1:1", ModelURL: "http://127.0.0.1:1/v1"}))
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/api/chat", "application/json", strings.NewReader(`{"model":"frage","stream":false,"messages":[{"role":"user","content":"was ist einslive"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var one struct {
		Message struct{ Content string } `json:"message"`
		Done    bool                     `json:"done"`
	}
	json.NewDecoder(resp.Body).Decode(&one)
	if !one.Done || !strings.Contains(one.Message.Content, "nicht ans Internet") { // the search is down (#864)
		t.Fatalf("%+v", one)
	}
}
