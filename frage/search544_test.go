package frage

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// #544: the Operator reads the web through Frage's SearXNG — the hits
// alone, no model in between, in the language it asks for.
func TestSearchAnswersTheHitsWithoutTheModel(t *testing.T) {
	var lang string
	searx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lang = r.URL.Query().Get("language")
		json.NewEncoder(w).Encode(map[string]any{"results": []Hit{{Title: "Release 10.10.7", URL: "https://github.com/jellyfin/jellyfin/releases", Content: "Fixes a crash"}}})
	}))
	defer searx.Close()
	// a model that answers would be a summary: this one fails every call
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no model here", 500) }))
	defer model.Close()
	srv := httptest.NewServer(Handler(Config{SearxURL: searx.URL, ModelURL: model.URL + "/v1"}))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/search?q=jellyfin+crash&lang=all")
	if err != nil {
		t.Fatal(err)
	}
	var hits []Hit
	json.NewDecoder(resp.Body).Decode(&hits)
	if resp.StatusCode != 200 || len(hits) != 1 || hits[0].URL != "https://github.com/jellyfin/jellyfin/releases" || lang != "all" {
		t.Fatalf("%d %+v lang %q", resp.StatusCode, hits, lang)
	}
	searx.Close() // SearXNG away: an error, never an empty success
	if resp, _ := http.Get(srv.URL + "/search?q=x"); resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("searx down: %d", resp.StatusCode)
	}
}
