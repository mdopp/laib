package lease

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// #567: Paperless' embeddings go through the proxy (owner, 2026-10-24),
// never llama-embed's own port, which stays on the host's loopback. The
// proxy hands /v1/embeddings to llama-embed whatever model it names (the
// router knows no embedding preset); while a window that stops model/embed
// stands it refuses with 409 and says until when; an embedding is no
// person's request.
func TestTheProxyHandsEmbeddingsToLlamaEmbed(t *testing.T) {
	router := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"from": "router"})
	}))
	defer router.Close()
	var got string
	embed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Method + " " + r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"from": "embed", "data": []map[string]any{{"embedding": []float64{0.1}}}})
	}))
	defer embed.Close()
	cfg := config42(t, router.URL)
	cfg.EmbedURL = embed.URL
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	code, out := post(t, srv.URL+"/v1/embeddings", `{"model":"nomic-embed-text","input":["Rechnung"]}`)
	if code != 200 || out["from"] != "embed" || got != "POST /v1/embeddings" {
		t.Fatalf("an embedding did not reach llama-embed: %d %v (embed saw %q)", code, out, got)
	}
	if _, out := call(t, "GET", srv.URL+"/api/model-lease", "", ""); out["last_human_at"] != nil {
		t.Fatalf("an embedding counted as a person's request: %v", out["last_human_at"])
	}

	if _, err := s.Acquire(context.Background(), "bote", "full", time.Hour); err != nil {
		t.Fatal(err)
	}
	got = ""
	code, out = post(t, srv.URL+"/v1/embeddings", `{"model":"nomic-embed-text","input":["Rechnung"]}`)
	msg, _ := out["error"].(map[string]any)["message"].(string)
	if code != 409 || got != "" || !strings.Contains(msg, "bote") {
		t.Fatalf("while full stops llama-embed an embedding must be refused naming the holder: %d %v (embed saw %q)", code, out, got)
	}
	s.Release(context.Background(), "bote")
	if code, _ := post(t, srv.URL+"/v1/embeddings", `{"input":["Rechnung"]}`); code != 200 {
		t.Fatalf("after the window an embedding is refused: %d", code)
	}
}
