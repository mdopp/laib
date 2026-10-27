package lease

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type status785 struct {
	At       string
	Detail   string
	Findings []struct{ Severity, Detail string }
}

func read785(t *testing.T, cfg Config) status785 {
	t.Helper()
	var st status785
	b, err := os.ReadFile(filepath.Join(cfg.LeaseDir, "laib-status.json"))
	if err != nil || json.Unmarshal(b, &st) != nil {
		t.Fatalf("no status file: %v %s", err, b)
	}
	return st
}

// #785: llama's line on the Box page read „geprüft vor 7 h“: the lease wrote
// its status only at start and on a lease change, so its age said nothing.
// Now it measures the router on every refresh (which model is loaded, or that
// it does not answer) and the refresh runs periodically, so the age is the
// age of a measurement.
func TestTheLeaseStatusMeasuresTheRouterOnEveryRefresh(t *testing.T) {
	loaded := true
	router := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		st := "unloaded"
		if loaded {
			st = "loaded"
		}
		w.Write([]byte(`{"object":"list","data":[{"id":"gemma-4-e4b","status":{"value":"` + st + `"}},{"id":"gemma-4-12b","status":{"value":"unloaded"}}]}`))
	}))
	defer router.Close()
	cfg := config42(t, router.URL)
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	st := read785(t, cfg)
	if st.Detail != "Haushalt: nur gemma-4-e4b · geladen: gemma-4-e4b" {
		t.Fatalf("the start's status does not say what is loaded: %q", st.Detail)
	}
	// an old file: the refresh writes a new measurement
	old := `{"at":"2026-10-27T00:00:00Z","detail":"alt"}`
	os.WriteFile(filepath.Join(cfg.LeaseDir, "laib-status.json"), []byte(old), 0o644)
	loaded = false
	s.Refresh(context.Background())
	st = read785(t, cfg)
	at, err := time.Parse(time.RFC3339, st.At)
	if err != nil || time.Since(at) > time.Minute {
		t.Fatalf("the refresh did not write a fresh at: %q", st.At)
	}
	if st.Detail != "Haushalt: nur gemma-4-e4b · kein Modell geladen, das erste lädt bei der nächsten Frage" {
		t.Fatalf("unloaded router: %q", st.Detail)
	}
	// the router silent: not measured, and a yellow finding, never "geladen"
	router.Close()
	s.Refresh(context.Background())
	st = read785(t, cfg)
	if strings.Contains(st.Detail, "geladen") || !strings.Contains(st.Detail, "Router nicht gemessen") {
		t.Fatalf("a silent router must not read as measured: %q", st.Detail)
	}
	if len(st.Findings) != 1 || st.Findings[0].Severity != "gelb" || !strings.Contains(st.Findings[0].Detail, "Router antwortet nicht") {
		t.Fatalf("a silent router is a yellow finding: %+v", st.Findings)
	}
}
