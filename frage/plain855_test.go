package frage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// #855: Frage spoke „Das konnte ich gerade nicht nachschlagen: modell: 409
// Conflict {…}“ aloud. A speaker says a sentence and the next step; the raw
// error goes to the container's log.
func TestAFailedLookupIsSpokenPlainly(t *testing.T) {
	searx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"results": []Hit{{Title: "1LIVE", URL: "https://www1.wdr.de/", Content: "Radio."}}})
	}))
	defer searx.Close()
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"the GPU is held by another lease"}`, http.StatusConflict)
	}))
	defer model.Close()
	for _, c := range []Config{{SearxURL: searx.URL, ModelURL: model.URL + "/v1"}, {SearxURL: "http://127.0.0.1:1", ModelURL: model.URL + "/v1"}} {
		said := c.spoken(context.Background(), "was ist einslive", "")
		for _, raw := range []string{"409", "Conflict", "modell:", "{", "dial tcp", "http"} {
			if strings.Contains(said, raw) {
				t.Errorf("spoken aloud: %q carries %q", said, raw)
			}
		}
		want := "nicht nachschlagen" // the model failed
		if c.SearxURL == "http://127.0.0.1:1" {
			want = "nicht ans Internet" // the search failed (#864)
		}
		if !strings.Contains(said, want) || !strings.Contains(said, "noch einmal") {
			t.Errorf("no sentence with %q and a next step: %q", want, said)
		}
	}
}
